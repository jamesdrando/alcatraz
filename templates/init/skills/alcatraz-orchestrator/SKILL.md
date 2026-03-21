---
name: alcatraz-orchestrator
description: Use when coordinating one or more coding agents with Alcatraz. Covers repo initialization, run creation, MCP and CLI usage, claim assignment, recovery, integration, and the rules required to run agents freely inside isolated containers while keeping host-side coordination deterministic.
---

# Alcatraz Orchestrator

## Purpose

Use Alcatraz to run coding agents inside isolated containers, in parallel, with deterministic host-side coordination.

The orchestrator owns:

- task decomposition
- run creation
- scope assignment
- coordination-path assignment
- integration order
- recovery and cleanup

The worker does not own those decisions unless explicitly told.

## Harness Model

A harness is the executable Alcatraz launches inside the container.

Treat "which agent are we running?" as a harness-selection question, not as an Alcatraz provider-integration question.

Alcatraz SHOULD stay focused on:

- lifecycle
- isolation
- worktrees
- claims
- env/bootstrap convenience for a small known list of harnesses

If the orchestrator uses a custom external harness, the harness owns its own provider logic.

## Model

1. A run is a git worktree plus a dedicated branch plus a dedicated compose project.
2. The worktree is mounted into the container at `/workspace`.
3. The container is the place where the agent may work freely.
4. The host repo remains the source of truth for coordination, status, and integration.
5. `merge_target` is explicit. Integration MUST NOT depend on the branch currently checked out in the host repo.

## Primary Intent

The point of Alcatraz is not "sandbox for its own sake."

The point is:

- let agents modify code aggressively inside isolated containers
- let multiple agents work in parallel
- keep ownership, coordination, and integration explicit on the host

Codex is a built-in convenience harness, not the only possible harness.

## Skill Location

`alcatraz init` writes project-local skills to `.codex/skills` by default.

That is the repository-local convention used by Alcatraz. If your environment expects another project-local path, set it explicitly during `alcatraz init`.

## Required Invariants

The orchestrator MUST enforce these:

1. Every run SHOULD have explicit `owned_paths`.
2. `claim_mode=shared` MUST NOT be used without explicit `owned_paths`.
3. `coordination_paths` MUST be used for cross-cutting files such as `go.mod`, lockfiles, schema roots, or generated-code roots.
4. Workers MUST NOT edit outside their claimed scope.
5. Workers MUST NOT merge unless explicitly instructed to do so.
6. A run marked `ready` is considered complete for claim-release purposes.
7. A run marked `blocked` or `ready_with_assumptions` is not complete for claim-release purposes.
8. Codex-specific auth/bootstrap rules SHOULD be treated as harness-specific conveniences, not as global Alcatraz behavior.

## Claim Rules

`claim_mode=exclusive`

- overlapping subtree claims are rejected

`claim_mode=shared`

- overlapping subtree claims are allowed only with other shared claims
- use this only for intentional collaboration on the same tree

`coordination_paths`

- always exclusive
- overlap is rejected regardless of subtree claim mode
- use for files whose change should be globally serialized

Examples:

- Worker A owns `internal/mcp`, exclusive
- Worker B owns `internal/runs`, exclusive
- Worker C owns `docs`, shared
- Worker D owns `docs`, shared
- Worker E owns `pkg/api`, exclusive, plus `coordination_paths=["go.mod"]`

## CLI Commands

Initialize a repo:

```shell
alcatraz init
alcatraz init --non-interactive
alcatraz init --skill-dir .codex/skills
```

Create an isolated run:

```shell
alcatraz run \
  --base-ref main \
  --merge-target main \
  --claim-mode exclusive \
  --owned-paths internal/mcp \
  --coordination-paths go.mod \
  -- --no-alt-screen
```

Switch the harness by editing `.alcatraz/config.json`:

```json
{
  "harness_command": ["my-harness", "--workspace", "/workspace"]
}
```

Inspect state:

```shell
alcatraz list --json
alcatraz status --json
alcatraz diff --stat
```

Record completion without merging:

```shell
alcatraz finish \
  --status ready \
  --summary "implemented MCP lifecycle updates"
```

Integrate and clean:

```shell
alcatraz finish \
  --merge \
  --into main \
  --clean \
  --delete-branch
```

## MCP Tools

Create a run:

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

`alcatraz_run` returns run metadata. It does not return the worker's stdout on success.

Get a run:

```json
{
  "name": "alcatraz_get_run",
  "arguments": {
    "run_id": "20260318-000001-abcd"
  }
}
```

Get a diff:

```json
{
  "name": "alcatraz_diff_run",
  "arguments": {
    "run_id": "20260318-000001-abcd",
    "stat": true
  }
}
```

Record completion:

```json
{
  "name": "alcatraz_finish_run",
  "arguments": {
    "run_id": "20260318-000001-abcd",
    "status": "ready_with_assumptions",
    "summary": "Implemented the local change. Another run must update go.mod.",
    "needs_changes": [
      {
        "path": "go.mod",
        "description": "Add the new module dependency.",
        "blocking": true
      }
    ],
    "assumptions": [
      "A later run will add the module dependency before integration."
    ]
  }
}
```

Integrate:

```json
{
  "name": "alcatraz_finish_run",
  "arguments": {
    "run_id": "20260318-000001-abcd",
    "merge": true,
    "clean": true,
    "delete_branch": true
  }
}
```

## Recommended Workflow

1. Initialize the repo with `alcatraz init`.
2. Choose the task slice.
3. Choose `merge_target`.
4. Choose `owned_paths`.
5. Choose `coordination_paths`.
6. Create the run.
7. Dispatch the worker with run-specific instructions.
8. Poll status and diff.
9. Require the worker to finish with a structured status.
10. Integrate only when the run is actually ready.

## Dispatch Rules

When prompting a worker, include all of the following:

- run ID
- merge target
- claim mode
- owned paths
- coordination paths
- which harness is running
- whether merge is allowed

Do not assume the worker can infer those from context.

If the harness is custom, do not assume Codex conventions apply.

## Status Meanings

`ready`

- work is complete for the assigned scope
- claim is released
- integration may proceed

`blocked`

- work cannot continue without a host-side or cross-scope change
- claim remains active
- do not start overlapping work unless you deliberately re-plan

`ready_with_assumptions`

- local work is complete
- one or more external assumptions remain unresolved
- claim remains active
- do not merge until the assumption is resolved or explicitly waived

## Recovery

If `alcatraz_run` fails:

1. call `alcatraz_list_runs`
2. inspect the reported run
3. inspect logs or diff as needed
4. clean the partial run if it is not recoverable

If a worker reports path-claim overlap:

1. split the task differently, or
2. use `shared` only if shared editing is intentional, or
3. move the disputed file into `coordination_paths`

If a worker touches an unclaimed file:

1. do not merge
2. treat that as a coordination failure
3. either create a new run with the correct claim, or finish the current run as `blocked`

If a run is complete but stale:

1. inspect diff
2. integrate if still valid
3. otherwise clean it explicitly

## Non-Goals

Alcatraz does not automatically decide:

- how to split tasks
- whether shared editing is wise
- whether assumptions are acceptable
- merge order across many runs

Those remain orchestrator decisions.
