# alcatraz

`alcatraz` exists to let coding agents work freely inside isolated containers while the host keeps coordination, ownership, and integration deterministic.

This project is intentionally about isolation and lifecycle management, not autonomous orchestration policy. The container is where an agent can act aggressively. The host repository is where run metadata, scope claims, recovery, and integration stay explicit.

## Intent

Alcatraz is for this use case:

- one git repository
- one or more agents
- one isolated worktree and container per agent run
- explicit host-side control over what each run may change
- eventual integration back into a known branch

Alcatraz is not trying to decide:

- how to split tasks
- whether a semantic dependency is acceptable
- whether shared editing is wise
- when many finished runs should be integrated

Those are orchestrator decisions.

## Core Model

These terms are normative for the CLI, the MCP server, and the generated `SKILL.md` files.

`run`

- one git worktree
- one dedicated git branch
- one dedicated Docker Compose project
- one metadata file under `.git/alcatraz/runs/`

`worktree`

- a repo checkout created specifically for one run
- mounted into the agent container at `/workspace`

`merge_target`

- the branch a run should integrate back into later
- recorded when the run is created
- not inferred from whatever branch happens to be checked out later

`owned_paths`

- the repo-relative paths a run is allowed to edit

`claim_mode`

- `exclusive`: overlapping subtree claims are rejected
- `shared`: overlapping subtree claims are allowed only with other `shared` claims

`coordination_paths`

- repo-relative paths reserved as globally coordinated resources
- always exclusive
- intended for cross-cutting files such as `go.mod`, lockfiles, schema roots, or generated-code roots

`completion status`

- `ready`
- `blocked`
- `ready_with_assumptions`

`harness`

- the executable Alcatraz launches inside the container
- conceptually this answers "which agent are we running?"
- Codex is the default harness today
- provider/API choices belong to the harness, not to Alcatraz

## Repository Layout

At runtime, Alcatraz uses these locations:

- `.git/alcatraz/runs/` for run metadata
- `.git/alcatraz/assets/` for staged bundled Docker/Compose assets
- `.alcatraz/worktrees/` for per-run worktrees

That split is deliberate:

- metadata stays local to git state
- bundled assets stay local and versioned by Alcatraz
- worktrees stay outside `.git`, which avoids common git/editor edge cases

## Installation

For local development inside this repository:

```shell
./alcatraz help
```

For a normal install:

```shell
go install github.com/jamesdrando/alcatraz/cmd/alcatraz@latest
go install github.com/jamesdrando/alcatraz/cmd/alcatraz-mcp@latest
```

The installed binaries embed their Docker/Compose assets. They do not require you to copy `compose.yaml`, Dockerfiles, or entrypoint scripts into the target repository.

## Harnesses

Alcatraz is harness-oriented.

The main question is:

- what executable should Alcatraz run inside the container?

Not:

- which model API should Alcatraz itself understand?

Alcatraz launches a harness. The harness may then talk to OpenAI, Bedrock, or another backend. That provider logic belongs to the harness.

Alcatraz itself owns:

- the worktree
- the container lifecycle
- the compose project
- env/bootstrap convenience
- host-side coordination and recovery

The harness owns:

- how it talks to models
- which SDK or API it uses
- which provider it targets
- its own runtime behavior inside the container

### Codex Convenience

Codex is the only harness family with built-in convenience automation today.

That means Alcatraz can currently help with:

- mounting the host Codex binary
- seeding a logged-in `.codex` directory when ChatGPT auth is used
- using the existing Codex-specific compose overlays
- choosing the right network preflight target for Codex auth mode

If you replace Codex with your own harness, Alcatraz should mostly be thought of as:

- run this command in the container
- pass env through in a predictable way
- keep lifecycle, worktree, and coordination explicit

## `alcatraz init`

`alcatraz init` writes explicit project-local Alcatraz scaffolding.

Today that means:

- `.alcatraz/config.json`
- optional project-local skills

By default, the project-local skill convention used by Alcatraz is:

```text
.codex/skills/
```

That is a repository-local convention chosen by this project. It is not meant to imply that every agent ecosystem uses that exact path globally.

### What `init` does

`alcatraz init`:

- requires an existing git repository
- resolves the repository root from the current working directory or `--repo`
- writes `.alcatraz/config.json`
- sets `default_base_ref` to the current branch if one is available, otherwise `HEAD`
- writes a default Codex `harness_command`
- writes the generated skill files unless `--no-skills` is used
- keeps existing generated files unless `--force` is used

It does not:

- create a run
- create a worktree
- start Docker
- modify git history

### Interactive use

```shell
alcatraz init
```

When stdin is interactive, `init` prints a banner, version info, and prompts for project-local skill generation.

The current prompt flow is:

```text
alcatraz init

<banner>

Welcome to Alcatraz.

Would you like to add project-local Alcatraz skills for this repository?
  1. Yes, create skills at .codex/skills (recommended)
  2. Yes, but I will choose the path
  3. No
> 
```

### Non-interactive use

```shell
alcatraz init --non-interactive
alcatraz init --non-interactive --no-skills
alcatraz init --non-interactive --skill-dir .codex/skills
alcatraz init --non-interactive --force
```

In non-interactive mode, skills are written to `.codex/skills` by default unless `--no-skills` is set.

### `init` flags

`--repo`

- path inside the target git repository
- defaults to the current repository

`--config-path`

- path for the generated config file
- relative to the repo root unless absolute
- default: `.alcatraz/config.json`

`--skill-dir`

- directory for generated project-local skills
- relative to the repo root unless absolute

`--no-skills`

- do not generate any project-local skills

`--non-interactive`

- do not prompt
- use deterministic defaults

`--force`

- overwrite generated files if they already exist

## Generated Skill Files

`alcatraz init` currently generates two project-local skills:

- `.codex/skills/alcatraz-orchestrator/SKILL.md`
- `.codex/skills/alcatraz-worker/SKILL.md`

Their purposes are intentionally different.

`alcatraz-orchestrator`

- for the host-side coordinator
- defines how to split tasks, assign claims, create runs, and integrate results
- explains the harness abstraction and when Codex-specific convenience applies

`alcatraz-worker`

- for the agent doing one concrete coding task inside one run
- defines what the worker may edit, when to stop, and how to report completion
- explains that the harness is the executable running inside the container, not a provider contract Alcatraz must understand

Those skills are meant to remove ambiguity for future agents. They should be treated as operating instructions, not as marketing copy.

## Generated Config

`alcatraz init` writes an explicit config file instead of relying on defaults hidden in code.

The generated config is a JSON form of the current built-in defaults, with `default_base_ref` resolved from the current branch when possible.

Typical generated config:

```json
{
  "branch_prefix": "alcatraz",
  "compose_project_prefix": "alcatraz",
  "default_base_ref": "main",
  "allow_dirty": false,
  "env_file": ".env",
  "compose_files": [
    "compose.yaml",
    "compose.codex.yaml"
  ],
  "chatgpt_compose_file": "compose.chatgpt.yaml",
  "harness_command": [
    "codex",
    "--dangerously-bypass-approvals-and-sandbox",
    "-C",
    "/workspace"
  ],
  "dependency_profiles": [],
  "apt_packages": [],
  "node_packages": [],
  "python_packages": [],
  "go_modules": []
}
```

`harness_command` is the preferred config name for the executable Alcatraz launches inside the container.

`agent_command` is still accepted as a compatibility alias.

If you are building your own external harness, this is the main field you change.

Example:

```json
{
  "harness_command": ["my-harness", "--workspace", "/workspace"]
}
```

When you switch away from Codex, the Codex-specific convenience automation no longer applies automatically. In that case, auth and provider configuration belong to the harness contract.

Config discovery order remains:

1. `.alcatraz.json`
2. `.alcatraz/config.json`
3. `alcatraz.json`

You may also pass an explicit config file:

```shell
alcatraz run --config path/to/config.json
```

## Quick Start

Initialize the repository:

```shell
alcatraz init
```

Create a run for one agent:

```shell
alcatraz run \
  --base-ref main \
  --merge-target main \
  --claim-mode exclusive \
  --owned-paths internal/mcp \
  --coordination-paths go.mod \
  -- --no-alt-screen
```

Pass extra harness arguments after `--`:

```shell
alcatraz run -- --resume-last-session
alcatraz run -- --model gpt-5
```

Inspect the run:

```shell
alcatraz list --json
alcatraz status --json
alcatraz diff --stat
```

Finish without merging:

```shell
alcatraz finish \
  --status ready \
  --summary "implemented the MCP lifecycle change"
```

Integrate and clean:

```shell
alcatraz finish \
  --merge \
  --into main \
  --clean \
  --delete-branch
```

## Run Lifecycle

The lifecycle is explicit.

### 1. Create

`alcatraz run` or `alcatraz_run`:

- resolves a base ref
- resolves and stores the exact base commit
- records the explicit merge target
- validates the run's claim
- creates the git worktree and branch
- records metadata under `.git/alcatraz/runs/`
- starts the compose project

If `extra_agent_args` are passed through MCP, Alcatraz also executes the configured harness command inside the new container after startup.

Important: `alcatraz_run` still returns run metadata, not the worker command's stdout payload.

In harness terms, `extra_agent_args` are appended to the configured harness command.

### 2. Work

The worker operates inside the container with `/workspace` mounted from the run worktree.

The worker may:

- inspect files
- edit in-scope files
- run tests and linters
- prepare a structured finish state

### 3. Inspect

Use:

```shell
alcatraz status
alcatraz status --json
alcatraz diff
alcatraz diff --stat
```

The diff is computed relative to the run's recorded `base_commit`, not the current host branch tip.

### 4. Finish

`alcatraz finish` and `alcatraz_finish_run`:

- commit worktree changes if present
- compute `touched_paths`
- reject out-of-claim edits
- optionally record structured completion state
- optionally merge into the run's stored `merge_target` or an explicit override
- optionally clean the run

### 5. Clean

`alcatraz clean` removes the run worktree and metadata, and optionally deletes the run branch.

## Claim and Coordination Model

This is the core of multi-agent determinism.

### Deterministic overlap rule

Path overlap is checked by canonical repo-relative path prefix.

These overlap:

- `internal/mcp`
- `internal/mcp/server.go`

These do not:

- `internal/mcp`
- `internal/runs`

This makes filesystem overlap deterministic.

It does not make semantic coupling deterministic. Two runs can still affect each other logically while editing different files. That is a real limit of the model and is intentional.

### Claim rules

`exclusive`

- may not overlap any active blocking claim

`shared`

- may overlap only with other `shared` subtree claims
- may not be used without explicit `owned_paths`

`coordination_paths`

- are always exclusive
- override subtree sharing
- are intended for files that should be globally serialized

### Whole-repo claims

A run with no `owned_paths` is treated as claiming the whole repository.

That is allowed, but it blocks any other overlapping scoped work until the run is released.

### Claim release

A run releases its claim only when it reaches `ready`.

These keep the claim active:

- `blocked`
- `ready_with_assumptions`
- unfinished runs

### Finish-time enforcement

At finish time, Alcatraz computes `touched_paths` and rejects edits outside the run's claimed scope.

That means path ownership is checked twice:

- when the run is created
- when the run is finished

### Practical guidance

Use `coordination_paths` for files like:

- `go.mod`
- `go.sum`
- `package.json`
- lockfiles
- schema files
- generated-code roots

If a worker discovers it needs a cross-scope change, do not let it silently drift. It should finish as `blocked` or `ready_with_assumptions` and record the needed change explicitly.

## Completion Status

`ready`

- the assigned work is complete
- the claim is released
- integration may proceed

`blocked`

- work cannot continue without another action
- the claim remains active
- do not treat the run as ready for merge

`ready_with_assumptions`

- local work is complete
- one or more external assumptions remain unresolved
- the claim remains active
- do not merge until the assumption is resolved or explicitly waived

## CLI Reference

`alcatraz`

- same as `alcatraz run`

`alcatraz init`

- write explicit repo-local config and optional skill files

`alcatraz run`

- create a run
- create a worktree and branch
- start the isolated compose project
- launch the configured harness interactively

Examples:

```shell
alcatraz run
alcatraz run --base-ref main --merge-target main -- --no-alt-screen
alcatraz run --branch feature/mcp --merge-target main
alcatraz run --claim-mode exclusive --owned-paths internal/mcp
alcatraz run --claim-mode shared --owned-paths docs
alcatraz run --coordination-paths go.mod,go.sum
alcatraz run --deps typescript,python --node-packages hono --python-packages fastapi,uv
alcatraz run -- --resume-last-session
```

`alcatraz list`

- list known runs and their worktrees

Examples:

```shell
alcatraz list
alcatraz list --json
```

`alcatraz status`

- show one run
- defaults to the most recent run if no run ID is given

Examples:

```shell
alcatraz status
alcatraz status 20260318-000001-abcd --json
```

`alcatraz diff`

- show the current diff for one run

Examples:

```shell
alcatraz diff
alcatraz diff --stat
alcatraz diff 20260318-000001-abcd
```

`alcatraz finish`

- commit worktree changes
- optionally record structured completion data
- optionally merge
- optionally clean

Examples:

```shell
alcatraz finish --status ready --summary "implemented the assigned task"
alcatraz finish --status blocked --summary "requires schema update" --needs-change db/schema.sql:Add the new table.
alcatraz finish --status ready_with_assumptions --summary "local work complete" --assumption "go.mod will be updated by another run"
alcatraz finish --merge --into main
alcatraz finish --merge --clean --delete-branch
alcatraz finish --json
```

`alcatraz clean`

- stop and remove one run or all runs
- optionally delete branches

Examples:

```shell
alcatraz clean
alcatraz clean --delete-branch
alcatraz clean --all --delete-branch
```

`alcatraz config`

- print the effective config after discovery and defaults

Example:

```shell
alcatraz config
```

## MCP Server

`alcatraz-mcp` is a separate host-side binary. It uses the same internal runtime as the human CLI. It does not shell out to the CLI.

Run it over stdio:

```shell
go run ./cmd/alcatraz-mcp
```

The MCP layer is intentionally thin. It reuses the same repository discovery, config discovery, metadata layout, worktree layout, env handling, and Docker/Compose boundary as the CLI.

It launches whatever harness command is configured for the repository. It does not try to understand the model-provider semantics behind that harness.

## MCP Tool Contract

Each tool returns structured data plus a text JSON mirror for compatibility.

### `alcatraz_run`

Purpose:

- create a run
- validate claims
- start the compose project
- optionally execute the configured harness command inside the new run

Important:

- the successful result is run metadata
- it is not a streamed transcript or captured stdout from the worker command

Input:

- `config_path?`
- `base_ref?`
- `branch_name?`
- `merge_target?`
- `claim_mode?`
- `owned_paths?`
- `coordination_paths?`
- `allow_dirty?`
- `extra_agent_args?`

Example:

```json
{
  "name": "alcatraz_run",
  "arguments": {
    "base_ref": "main",
    "merge_target": "main",
    "claim_mode": "exclusive",
    "owned_paths": ["internal/mcp"],
    "coordination_paths": ["go.mod"],
    "extra_agent_args": ["Implement the assigned MCP change."]
  }
}
```

Result fields:

- `run_id`
- `branch_name`
- `base_commit`
- `merge_target`
- `claim_mode`
- `owned_paths`
- `coordination_paths`
- `worktree_path`
- `compose_project`
- `auth_mode`

### `alcatraz_diff_run`

Purpose:

- return the current diff for a run relative to its recorded base commit

Input:

- `run_id`
- `stat?`

Example:

```json
{
  "name": "alcatraz_diff_run",
  "arguments": {
    "run_id": "20260318-000001-abcd",
    "stat": true
  }
}
```

### `alcatraz_list_runs`

Purpose:

- list all known runs and their status

Example:

```json
{
  "name": "alcatraz_list_runs",
  "arguments": {}
}
```

### `alcatraz_get_run`

Purpose:

- return one run by ID

Example:

```json
{
  "name": "alcatraz_get_run",
  "arguments": {
    "run_id": "20260318-000001-abcd"
  }
}
```

### `alcatraz_finish_run`

Purpose:

- commit changes
- record structured completion state
- optionally merge
- optionally clean

Input:

- `run_id`
- `commit_message?`
- `status?`
- `summary?`
- `needs_changes?`
- `assumptions?`
- `suggested_followups?`
- `merge?`
- `merge_into?`
- `clean?`
- `delete_branch?`

Example:

```json
{
  "name": "alcatraz_finish_run",
  "arguments": {
    "run_id": "20260318-000001-abcd",
    "status": "ready_with_assumptions",
    "summary": "Local work is complete. Another run must update go.mod.",
    "needs_changes": [
      {
        "path": "go.mod",
        "description": "Add the required dependency.",
        "blocking": true
      }
    ],
    "assumptions": [
      "Another run will update go.mod before integration."
    ]
  }
}
```

Result fields include:

- `commit_created`
- `commit_sha`
- `touched_paths`
- `completion_saved`
- `merged`
- `merge_target`

### `alcatraz_clean_run`

Purpose:

- stop and remove one run
- optionally delete the branch

Example:

```json
{
  "name": "alcatraz_clean_run",
  "arguments": {
    "run_id": "20260318-000001-abcd",
    "delete_branch": true
  }
}
```

### `alcatraz_clean_all`

Purpose:

- stop and remove all runs
- optionally delete branches

Example:

```json
{
  "name": "alcatraz_clean_all",
  "arguments": {
    "delete_branch": true
  }
}
```

### `alcatraz_get_config`

Purpose:

- return the effective config after discovery and defaults

Example:

```json
{
  "name": "alcatraz_get_config",
  "arguments": {}
}
```

### Current MCP omissions

The MCP surface is intentionally narrow today.

It does not currently expose a first-class tool for:

- container logs
- live exec streaming after `alcatraz_run`
- direct orchestration policy

## Recovery and Diagnostics

The important principle is: do not guess. Inspect the run state explicitly.

### If run creation fails

Do this:

```shell
alcatraz list --json
alcatraz status --json
alcatraz diff --stat
```

Then decide:

- keep the run and inspect it
- or clean it explicitly

Example:

```shell
alcatraz clean <run-id> --delete-branch
```

This matters because run creation is not all-or-nothing from the perspective of host state. Metadata, worktree, or branch state may exist even if container startup fails.

### If a worker needs an unclaimed file

Do not merge by convention alone.

Use one of these:

- create a new run with the correct claim
- finish the run as `blocked`
- finish as `ready_with_assumptions` only if the local task is actually complete without the external change

### If a finish fails because of path ownership

That means the worker touched something outside the claim.

Do this:

```shell
alcatraz diff
alcatraz finish --status blocked --summary "touched unclaimed paths"
```

Then either:

- remove the accidental edits
- or create a correctly scoped replacement run

### If a run is stale

Inspect first:

```shell
alcatraz status
alcatraz diff --stat
```

Then either:

- integrate if it is still valid
- or clean it

### If many agents are working in parallel

Use these host-side rules:

- every run SHOULD have explicit `owned_paths`
- use `exclusive` by default
- use `shared` only when shared editing is intentional
- reserve `coordination_paths` for cross-cutting files
- do not treat disjoint paths as proof of semantic independence

## Secrets and Environment

Alcatraz keeps secrets out of committed config by design.

Use:

- `OPENAI_API_KEY`
- or `HOST_CODEX_HOME` pointing to a logged-in local `.codex` directory
- optionally `OPENAI_BASE_URL` if your harness talks to an OpenAI-compatible endpoint

Keep secrets in `.env`, not in `config.json`.

If the configured env file does not exist, Alcatraz can bootstrap it with locally discoverable values and mark it ignored in `.git/info/exclude` when appropriate.

For Codex, the important local values are usually:

- `OPENAI_API_KEY`
- `HOST_CODEX_HOME`
- `HOST_CODEX_BIN`

For a custom harness, Alcatraz should generally not need to understand your full provider config. It should only need to make the relevant env available inside the container.

## Dependency Layers

Alcatraz can bake lean dependency layers into the agent image.

Supported dependency profiles:

- `node`
- `typescript`
- `bun`
- `go`
- `python`
- `rust`
- `postgresql`
- `redis`
- `aws`

You may also specify:

- `node_packages`
- `python_packages`
- `go_modules`
- `apt_packages`

Example:

```json
{
  "dependency_profiles": ["typescript", "python"],
  "node_packages": ["hono", "decimal.js"],
  "python_packages": ["fastapi", "sqlmodel", "uv"]
}
```

CLI example:

```shell
alcatraz run --deps typescript,python --node-packages hono,decimal.js --python-packages fastapi,sqlmodel,uv
```

## Security Model

The container boundary is the sandbox.

Important defaults:

- non-root `agent` user
- read-only root filesystem
- `cap_drop: [ALL]`
- `no-new-privileges`
- explicit writable mounts and `tmpfs`
- default-deny outbound model via the egress proxy

The `egress-proxy` container is the only container with outbound access. It permits only domains listed in `SQUID_ALLOWED_DOMAINS`.

Inside the container, the agent itself is started with its internal sandbox bypass enabled on purpose. The outer boundary is Docker plus the configured runtime, with `runsc` preferred when available.

## Current Limits

Alcatraz is intentionally explicit about these limits:

- path overlap is deterministic; semantic coupling is not
- the MCP layer is lifecycle-oriented, not a full orchestration brain
- a failed run start can leave inspectable host state behind
- shared claims require human or orchestrator judgment

That explicitness is a feature. The goal is predictable host coordination around isolated agent work, not hidden automation.
