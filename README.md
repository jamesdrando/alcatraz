# alcatraz

Secure Docker baseline for running coding agents inside a container with:

- a hardened agent runtime
- default-deny outbound networking
- an explicit HTTPS allowlist for the domains the agent actually needs
- support for either API-key auth or a seeded ChatGPT/Codex login

## Why this shape

Raw Docker networking is good at isolating containers, but it is not a great domain allowlist mechanism by itself. Domains resolve to changing IPs, CDNs move traffic around, and simple IP rules get brittle fast.

For a greenfield agent setup, the safest practical pattern is:

1. Put the agent on an `internal` Docker network so it has no direct internet route.
2. Give it a single egress path through a dedicated proxy container.
3. Make that proxy allow only a short list of domains such as `api.openai.com`.

That gives us a useful security property: even if the agent tries to reach arbitrary hosts, it only has one network path available, and that path enforces the allowlist.

## Files

- `alcatraz`: repo-local launcher for `init` and `run`
- `compose.yaml`: main runtime definition
- `compose.codex.yaml`: mounts the local Codex CLI into the agent container
- `docker/agent/Dockerfile`: minimal non-root agent image
- `docker/egress-proxy/Dockerfile`: tiny Squid-based allowlist proxy
- `docker/egress-proxy/docker-entrypoint.sh`: builds the runtime allowlist file
- `docker/egress-proxy/squid.conf`: locked-down proxy config
- `.env.example`: required environment variables

## Quick start

1. Copy `.env.example` to `.env`.
2. Choose one auth mode:

```bash
# Option A: API key
OPENAI_API_KEY=...

# Option B: reuse an existing Codex/ChatGPT login
HOST_CODEX_HOME=/absolute/path/to/your/.codex
```

3. Adjust `SQUID_ALLOWED_DOMAINS` only if the agent truly needs more outbound access.
4. Start the stack:

API key mode:

```bash
docker compose up --build -d
```

ChatGPT login reuse mode:

```bash
docker compose -f compose.yaml -f compose.chatgpt.yaml up --build -d
```

5. Open a shell inside the agent container:

```bash
docker compose exec agent bash
```

The repository is mounted at `/workspace`, and the agent home directory lives in a Docker volume.

## Alcatraz workflow

For day-to-day use, the intended entrypoint is the repo-local launcher:

```bash
./alcatraz init
./alcatraz run
```

`./alcatraz init` creates:

- `.alcatraz/config.env`
- `.alcatraz/worktrees/`
- `.alcatraz/logs/`
- `.alcatraz/state/`
- `.env` from `.env.example` if you do not already have one

`./alcatraz run` then:

1. checks that your current checkout is clean by default
2. creates a fresh git worktree under `.alcatraz/worktrees/<run-id>`
3. creates a branch like `alcatraz/20260318-123456-abcd`
4. starts Docker with that worktree mounted as `/workspace`
5. mounts your local static `codex` binary into the container
6. launches Codex inside the container with the container as the trust boundary

Because the worktree is a real git checkout, branch and file changes are immediately visible on the host. There is no separate copy-back step.

Examples:

```bash
./alcatraz run
./alcatraz run -- "Fix the failing tests"
./alcatraz run --base-ref main -- --no-alt-screen
```

The generated worktree and branch are preserved after the session so you can inspect, diff, or merge them locally.

## Auth modes

You can run this setup in either of these modes:

- `OPENAI_API_KEY`: good for API-first automation and service-style usage
- `HOST_CODEX_HOME`: good when your local Codex setup is already logged in with ChatGPT

When you start with `compose.chatgpt.yaml`, the container mounts `HOST_CODEX_HOME` read-only at startup and copies `auth.json` and `config.toml` into the container's own `CODEX_HOME` volume if they are not already present. After that, the container keeps its own auth state under `/home/agent/.codex`.

That gives us a safer default than mounting your whole live `~/.codex` directory read-write into the container.

## Security model

The `agent` service is intentionally constrained:

- non-root user
- `cap_drop: [ALL]`
- `no-new-privileges`
- read-only root filesystem
- writable paths only through explicit mounts and `tmpfs`
- no direct connection to the public internet

Inside the container, `alcatraz run` starts Codex with internal sandbox bypass enabled on purpose. The container is the outer sandbox, so this avoids stacking a second restrictive sandbox inside the first one.

The `egress-proxy` service is the only container with outbound access. It accepts requests only from the private Docker network and only forwards requests to domains on the allowlist.

## Default allowlist

The starter allowlist is intentionally small:

- `api.openai.com`
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

This is now a practical starter set for OpenAI access plus common package installation flows in Debian, npm, pip, Cargo, and Bun.

Notes:

- `bun install` uses `registry.npmjs.org` by default, so Bun package installs are already covered by the npm registry entry.
- `bun.com` and `bun.sh` are included for installing the Bun runtime itself.
- `crates.io`, `index.crates.io`, and `static.crates.io` cover the common Cargo crates.io flow. `static.crates.io` is included defensively because crate downloads may be served from that host.

If you later need git dependencies, private registries, or language-specific mirrors, do not open the internet broadly. Add only the exact domains you need and document why each one exists.

## Important caveats

- This design assumes the agent runtime honors `HTTP_PROXY` / `HTTPS_PROXY`.
- ChatGPT login reuse is practical for Codex-style tooling, but ordinary OpenAI API client libraries still expect API credentials rather than a ChatGPT web login.
- `./alcatraz run` refuses to start from a dirty checkout unless you pass `--allow-dirty`, because worktrees are created from committed git state.
- If you want package installation, prefer internal mirrors for npm, PyPI, crates, apt, and git hosting rather than opening general outbound access.
- Do not mount the host Docker socket into the agent container.
- Do not run the container as `privileged`.
- For stronger isolation on Linux hosts, combine this with rootless Docker or Podman and user namespaces.

## Next tightening steps

If you want to push this further later, the next upgrades I would make are:

1. Add a custom seccomp profile after observing the syscall set your workflow actually needs.
2. Move package retrieval behind internal mirrors and add only those mirror domains to the allowlist.
3. Add CI checks that fail if the Compose config regresses on `cap_drop`, `read_only`, or network layout.
