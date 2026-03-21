---
name: alcatraz-worker
description: Use when executing one scoped coding task inside an Alcatraz run. Covers the worker contract, allowed actions, required finish states, cross-scope escalation, CLI and MCP examples, and recovery steps inside the isolated container/worktree model.
---

# Alcatraz Worker

## Purpose

Use Alcatraz to execute one scoped task inside one isolated container and one isolated git worktree.

The worker owns:

- understanding the assigned task
- modifying code inside the assigned scope
- checking the local result
- reporting completion in a structured way

The worker does not own:

- task decomposition
- scope allocation
- coordination-path allocation
- merge policy
- integration order across runs

## Harness Model

A harness is the executable Alcatraz launches inside the container.

The worker may be running under Codex or under another harness chosen by the orchestrator.

Do not assume that Alcatraz itself understands the harness's model provider or API semantics.

## Skill Location

`alcatraz init` writes this skill to `.codex/skills` by default.

Treat that as the project-local convention chosen by Alcatraz, not as proof that every environment uses the same location.

## Model

1. The orchestrator creates the run.
2. The host creates a dedicated branch and worktree for that run.
3. The worktree is mounted inside the container at `/workspace`.
4. The worker may work freely inside the container.
5. The host-side run metadata remains the source of truth for status, claims, and integration.

## Required Inputs

Before starting work, the worker MUST know:

- run ID
- merge target
- claim mode
- owned paths
- coordination paths
- which harness is running
- whether merge is allowed
- the specific task to complete

If any of that is missing, the worker SHOULD stop and ask the orchestrator to restate the assignment explicitly.

If the harness is not Codex, do not assume Codex-specific behavior, auth, or flags.

## Primary Rule

Edit only what was assigned.

If the worker was not given a path claim, the worker MUST treat that as an orchestration problem and ask for clarification before broad edits.

## Allowed Actions

The worker MAY:

- inspect files under `/workspace`
- run repo-local tests and linters
- edit files inside `owned_paths`
- edit files inside `coordination_paths` only if those paths were explicitly assigned
- commit and finish the run when instructed
- receive extra harness-specific arguments after `--` when the run is launched

The worker MUST NOT:

- edit files outside the assigned claim
- switch the host repository branch
- change `merge_target`
- merge into the merge target unless explicitly instructed
- silently assume another run will fix a blocking cross-scope dependency

## Cross-Scope Rules

When work requires a change outside the assigned claim, choose exactly one of these paths:

`blocked`

- use when progress cannot continue without the external change
- stop editing
- record the needed change in `needs_changes`

`ready_with_assumptions`

- use when local work is complete but integration still depends on an external change
- record the assumption explicitly
- do not merge unless the orchestrator accepts the assumption

Do not hide cross-scope work in prose only. Record it in structured completion state.

## Status Meanings

`ready`

- assigned work is complete
- no unresolved external dependency remains

`blocked`

- the task cannot proceed without another action outside the assigned claim

`ready_with_assumptions`

- local work is complete
- integration still depends on an unresolved assumption or another run

## Recommended Workflow

1. Read the task and the claim.
2. Inspect the relevant files.
3. Make only in-scope changes.
4. Run the smallest useful verification.
5. Inspect the diff.
6. Finish with a structured status.

## CLI Commands

Inspect the current run:

```shell
alcatraz status --json
alcatraz diff --stat
```

Finish a clean, ready run:

```shell
alcatraz finish \
  --status ready \
  --summary "implemented the requested MCP validation change"
```

Finish as blocked:

```shell
alcatraz finish \
  --status blocked \
  --summary "cannot finish without a schema change outside the assigned scope" \
  --needs-change db/schema.sql:Add the new table required by this feature.
```

Finish with assumptions:

```shell
alcatraz finish \
  --status ready_with_assumptions \
  --summary "local implementation is complete; another run must update go.mod" \
  --needs-change go.mod:Add the required module dependency. \
  --assumption "A later run will update go.mod before integration."
```

## MCP Tools

Inspect the current run:

```json
{
  "name": "alcatraz_get_run",
  "arguments": {
    "run_id": "20260318-000001-abcd"
  }
}
```

Inspect the diff:

```json
{
  "name": "alcatraz_diff_run",
  "arguments": {
    "run_id": "20260318-000001-abcd",
    "stat": true
  }
}
```

Finish with structured completion:

```json
{
  "name": "alcatraz_finish_run",
  "arguments": {
    "run_id": "20260318-000001-abcd",
    "status": "blocked",
    "summary": "Implementation depends on a schema file outside the assigned claim.",
    "needs_changes": [
      {
        "path": "db/schema.sql",
        "description": "Add the new table required by this task.",
        "blocking": true
      }
    ]
  }
}
```

## Recovery

If the container session is interrupted:

1. inspect the run with `alcatraz status`
2. inspect local changes with `alcatraz diff`
3. continue working if the run still exists
4. ask the orchestrator to clean and recreate the run if the environment is no longer usable

If finish rejects the change because of path ownership:

1. do not force the merge
2. review `touched_paths`
3. remove accidental out-of-scope edits if possible
4. otherwise finish as `blocked` and report the missing claim

If the task requires another run to modify a shared resource such as `go.mod`, a lockfile, or a schema:

1. do not take that file unless it was explicitly assigned
2. report the change in `needs_changes`
3. choose `blocked` or `ready_with_assumptions` based on whether the local task is otherwise complete

## Non-Goals

The worker should not decide:

- how the repo should be partitioned
- whether a scope should become shared
- whether assumptions are acceptable
- when multiple ready runs should be integrated
