# alcatraz

`alcatraz` is a Go CLI for running coding agents inside a hardened gVisor-backed Docker boundary.

It is intentionally scoped to the containerization layer:

- secure-ish local agent runtime
- git worktree and branch isolation
- lifecycle commands with a machine-friendly contract
- no secrets stored in committed config

Orchestration is meant to sit on top of this later, not inside it.

## Install

For repo-local development:

```bash
./alcatraz help
```

That wrapper simply runs the Go CLI from source.

For a real install:

```bash
go install github.com/jamesdrando/alcatraz/cmd/alcatraz@latest
go install github.com/jamesdrando/alcatraz/cmd/alcatraz-mcp@latest
```

The installed binaries are self-contained for Docker/Compose assets, and `alcatraz run` can be launched from another git repo without copying `compose.yaml`, Dockerfiles, or entrypoint scripts into that target repo. Alcatraz prefers `runsc` when your Docker daemon has it registered, falls back to Docker's default runtime otherwise, and still lets you override the choice with `ALCATRAZ_CONTAINER_RUNTIME`.

## Commands

```bash
alcatraz run
alcatraz list
alcatraz status
alcatraz diff
alcatraz finish
alcatraz clean
alcatraz config
```

Useful examples:

```bash
alcatraz run
alcatraz run --base-ref main -- --no-alt-screen
alcatraz run --branch feature/sandbox-hardening
alcatraz diff --stat
alcatraz finish --merge --clean --delete-branch
alcatraz list --json
alcatraz status --json
alcatraz clean --all --delete-branch
```

## Config

`alcatraz run` works without a config file and uses built-in defaults.

Config discovery still happens in the target repo. The installed binary stages its bundled Docker/Compose assets under `.git/alcatraz/assets/` and uses those staged files at runtime. The runtime boundary is still host-managed through Docker Compose, and Alcatraz will use gVisor's `runsc` runtime when Docker exposes it.

If a repo wants explicit config, the CLI looks for these files in order:

1. `.alcatraz.json`
2. `.alcatraz/config.json`
3. `alcatraz.json`

You can also pass one directly:

```bash
alcatraz run --config path/to/config.json
```

A sample config lives at [alcatraz.example.json](/workspace/alcatraz.example.json).

Config is intentionally non-secret. Keep secrets in a local `.env`, not in the config file.

If you set `compose_files` or `chatgpt_compose_file`, those values now refer to bundled asset names such as `compose.yaml`, `compose.codex.yaml`, and `compose.chatgpt.yaml`, not arbitrary files in the target repo.

On first `alcatraz run`, if the configured env file is missing, Alcatraz will create it for you with any local values it can already discover, such as `HOST_CODEX_BIN`, `HOST_CODEX_HOME`, and `OPENAI_API_KEY`. It writes the file with `0600` permissions and adds it to `.git/info/exclude` when the env file lives inside the repo, so existing repos stay local-first without needing a committed setup file.

## Dependency Layers

When you know the stack ahead of time, you can ask Alcatraz to bake a lean dependency layer into the agent image instead of having the agent install everything ad hoc.

Supported dependency profiles are:

- `node`
- `typescript`
- `bun`
- `go`
- `python`
- `rust`
- `postgresql`
- `redis`
- `aws`

For small project-specific additions, the config also accepts:

- `node_packages` for global Node.js packages such as `hono`, `decimal.js`, or `@aws-sdk/client-s3`
- `python_packages` for global Python packages
- `go_modules` for Go module cache prefetching
- `apt_packages` for any other Debian packages you know you need

That keeps the default image lean while still letting you opt into prebuilt tooling when you already know the shape of the repo.

Example:

```json
{
  "dependency_profiles": ["typescript", "python"],
  "node_packages": ["hono", "decimal.js"],
  "python_packages": ["fastapi", "sqlmodel", "uv"]
}
```

The same thing works from the command line:

```bash
alcatraz run --deps typescript,python --node-packages hono,decimal.js --python-packages fastapi,sqlmodel,uv
```

For Go and Rust, the lean default is to bake in the toolchain and let the repo's own manifest control library dependencies. If you already know a Go module you want prefetched, `go_modules` lets you name it explicitly.

## Runtime Layout

To keep public repos clean while avoiding Git/tooling edge cases, Alcatraz splits runtime data in two places:

- `.git/alcatraz/runs/`
- `.git/alcatraz/assets/`
- `.alcatraz/worktrees/`

That means:

- run metadata stays local under `.git`
- bundled Docker/Compose assets stay local under `.git`
- worktrees stay outside `.git`, which keeps Git and editor tooling happier
- the worktree path is still hidden and gitignored, so it does not clutter the normal repo view

## Secrets

This repo keeps secrets out of git by design:

- `.env.example` is safe to commit
- `.env` is gitignored
- config files should not contain credentials

Auth can come from either:

- `OPENAI_API_KEY`
- `HOST_CODEX_HOME` pointing to a local logged-in `.codex` directory

## MCP Server

`alcatraz-mcp` is a separate host-side binary. It uses the same internal runtime as the human CLI and does not shell out to `alcatraz`.

Run it locally over stdio:

```bash
go run ./cmd/alcatraz-mcp
```

The MCP layer stays thin:

- it discovers config the same way as the CLI
- it stages the same bundled Docker/Compose assets under `.git/alcatraz/assets/`
- it keeps metadata under `.git/alcatraz/` and worktrees under `.alcatraz/worktrees/`
- it reuses `.env` and the same auth resolution rules
- it does not require an existing Alcatraz container to already be running

`alcatraz_run` creates the git worktree, branch, metadata, compose project, and detached container set for a run. It starts `egress-proxy` and `agent` on the host. If `extra_agent_args` are provided, it also executes the configured agent command inside the fresh `agent` container after startup.

## MCP Tool Contract

All tool responses return concise structured payloads plus a text JSON mirror for client compatibility.

`alcatraz_run`

- input: `config_path?`, `base_ref?`, `branch_name?`, `allow_dirty?`, `extra_agent_args?`
- result: `run_id`, `branch_name`, `worktree_path`, `compose_project`, `auth_mode`

`alcatraz_list_runs`

- result: `{ "runs": [RunStatus...] }`

`alcatraz_get_run`

- input: `run_id`
- result: `RunStatus`

`alcatraz_clean_run`

- input: `run_id`, `delete_branch`
- result: `{ "runs": [CleanupResult] }`

`alcatraz_clean_all`

- input: `delete_branch`
- result: `{ "runs": [CleanupResult...] }`

`alcatraz_get_config`

- input: `config_path?`
- result: effective config JSON

`RunStatus` includes:

- `id`, `branch_name`, `base_ref`, `worktree_path`, `compose_project`, `auth_mode`
- `status`, `running`, `worktree_exists`, `branch_exists`, `dirty`

## Security Model

The container runtime keeps the important restrictions:

- non-root `agent` user
- read-only root filesystem
- `cap_drop: [ALL]`
- `no-new-privileges`
- explicit writable mounts and `tmpfs`
- default-deny outbound model via the egress proxy

The `egress-proxy` is the only container with outbound access, and it only permits domains from `SQUID_ALLOWED_DOMAINS`.

Inside the container, the agent itself is launched with internal sandbox bypass enabled on purpose. The gVisor boundary is the sandbox.

## Default Allowlist

The default allowlist includes:

- `api.openai.com`
- `chatgpt.com`
- `auth.openai.com`
- `files.oaiusercontent.com`
- `deb.debian.org`
- `security.debian.org`
- `registry.npmjs.org`
- `pypi.org`
- `files.pythonhosted.org`
- `crates.io`
- `index.crates.io`
- `static.crates.io`
- `bun.com`
- `bun.sh`

That covers ChatGPT-authenticated Codex/OpenAI traffic plus common package installation flows for Debian, npm, pip, Cargo, and Bun.

## Notes

- The CLI mounts a fresh git worktree into `/workspace` and creates a new branch automatically unless you provide one.
- `alcatraz list` and `alcatraz status` are intended to be easy for humans and orchestration layers to consume.
- `alcatraz diff <run-id>` lets you inspect a run without navigating into the worktree yourself.
- `alcatraz finish <run-id>` stages and commits run changes for you, and can also merge into your current branch and clean up the run.
- If `finish` reports `No new worktree changes to commit`, that only means it did not create an extra commit from the worktree. With `--merge`, `--clean`, or `--delete-branch`, it still continues with those requested actions.
- `alcatraz clean` removes worktrees and can optionally delete the run branches too.
- If you need to inspect arbitrary external websites from inside the container, those domains must still be allowed explicitly.
