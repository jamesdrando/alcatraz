# alcatraz

`alcatraz` is a Go CLI for running coding agents inside a hardened Docker boundary.

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
```

## Commands

```bash
alcatraz run
alcatraz list
alcatraz status
alcatraz clean
alcatraz config
```

Useful examples:

```bash
alcatraz run
alcatraz run --base-ref main -- --no-alt-screen
alcatraz run --branch feature/sandbox-hardening
alcatraz list --json
alcatraz status --json
alcatraz clean --all --delete-branch
```

## Config

`alcatraz run` works without a config file and uses built-in defaults.

If a repo wants explicit config, the CLI looks for these files in order:

1. `.alcatraz.json`
2. `.alcatraz/config.json`
3. `alcatraz.json`

You can also pass one directly:

```bash
alcatraz run --config path/to/config.json
```

A sample config lives at [alcatraz.example.json](/home/drandall/alcatraz/alcatraz.example.json).

Config is intentionally non-secret. Keep secrets in a local `.env`, not in the config file.

## Runtime Layout

To keep public repos clean, runtime state is stored under `.git/alcatraz/`, not in the working tree:

- `.git/alcatraz/worktrees/`
- `.git/alcatraz/runs/`

That means:

- worktrees do not clutter the repo root
- run metadata stays local
- nothing under that runtime path is at risk of being committed accidentally

## Secrets

This repo keeps secrets out of git by design:

- `.env.example` is safe to commit
- `.env` is gitignored
- config files should not contain credentials

Auth can come from either:

- `OPENAI_API_KEY`
- `HOST_CODEX_HOME` pointing to a local logged-in `.codex` directory

## Security Model

The container runtime keeps the important restrictions:

- non-root `agent` user
- read-only root filesystem
- `cap_drop: [ALL]`
- `no-new-privileges`
- explicit writable mounts and `tmpfs`
- default-deny outbound model via the egress proxy

The `egress-proxy` is the only container with outbound access, and it only permits domains from `SQUID_ALLOWED_DOMAINS`.

Inside the container, the agent itself is launched with internal sandbox bypass enabled on purpose. The Docker boundary is the sandbox.

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
- `alcatraz clean` removes worktrees and can optionally delete the run branches too.
- If you need to inspect arbitrary external websites from inside the container, those domains must still be allowed explicitly.
