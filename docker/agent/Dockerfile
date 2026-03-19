FROM debian:bookworm-slim

ARG UID=1000
ARG GID=1000
ARG ALCATRAZ_DEP_PROFILES=""
ARG ALCATRAZ_APT_PACKAGES=""
ARG ALCATRAZ_NODE_PACKAGES=""
ARG ALCATRAZ_PYTHON_PACKAGES=""
ARG ALCATRAZ_GO_MODULES=""

ENV DEBIAN_FRONTEND=noninteractive
ENV NODE_PATH=/usr/local/lib/node_modules
ENV NPM_CONFIG_PREFIX=/usr/local
ENV BUN_INSTALL=/usr/local/bun
ENV PATH=/usr/local/bun/bin:${PATH}

# Install the requested toolchains and package caches before the agent starts.
RUN set -eu; \
    base_packages="bash ca-certificates curl git jq procps ripgrep tini unzip"; \
    apt_packages="$base_packages"; \
    profile_packages=""; \
    node_global_packages=""; \
    python_global_packages=""; \
    go_module_specs=""; \
    install_bun=0; \
    profiles="$(printf '%s' "${ALCATRAZ_DEP_PROFILES}" | tr ',' ' ')"; \
    for profile in $profiles; do \
        case "$profile" in \
            "") ;; \
            node) profile_packages="$profile_packages nodejs npm" ;; \
            typescript) profile_packages="$profile_packages nodejs npm"; node_global_packages="$node_global_packages typescript" ;; \
            bun) install_bun=1 ;; \
            go) profile_packages="$profile_packages golang" ;; \
            python) profile_packages="$profile_packages python3 python3-pip python3-venv" ;; \
            rust) profile_packages="$profile_packages cargo rustc build-essential pkg-config libssl-dev" ;; \
            postgresql) profile_packages="$profile_packages postgresql-client libpq-dev" ;; \
            redis) profile_packages="$profile_packages redis-tools" ;; \
            aws) profile_packages="$profile_packages awscli" ;; \
            *) echo "unknown dependency profile: $profile" >&2; exit 1 ;; \
        esac; \
    done; \
    extra_apt_packages="$(printf '%s' "${ALCATRAZ_APT_PACKAGES}" | tr ',' ' ')"; \
    if [ -n "$(printf '%s' "${ALCATRAZ_NODE_PACKAGES}${ALCATRAZ_NODE_PACKAGES:+ }${node_global_packages}" | tr -d '[:space:]')" ]; then \
        profile_packages="$profile_packages nodejs npm"; \
    fi; \
    if [ -n "$(printf '%s' "${ALCATRAZ_PYTHON_PACKAGES}" | tr -d '[:space:]')" ]; then \
        profile_packages="$profile_packages python3 python3-pip python3-venv"; \
    fi; \
    if [ -n "$(printf '%s' "${ALCATRAZ_GO_MODULES}" | tr -d '[:space:]')" ]; then \
        profile_packages="$profile_packages golang"; \
    fi; \
    apt_packages="$apt_packages $profile_packages $extra_apt_packages"; \
    apt-get update; \
    apt-get install -y --no-install-recommends $apt_packages; \
    rm -rf /var/lib/apt/lists/*; \
    if [ "$install_bun" -eq 1 ]; then \
        HOME=/root curl -fsSL https://bun.sh/install | bash; \
    fi; \
    node_global_packages="$(printf '%s %s' "${node_global_packages}" "${ALCATRAZ_NODE_PACKAGES}" | tr ',' ' ')"; \
    if [ -n "$(printf '%s' "${node_global_packages}" | tr -d '[:space:]')" ]; then \
        npm install -g -- $node_global_packages; \
    fi; \
    python_global_packages="$(printf '%s' "${ALCATRAZ_PYTHON_PACKAGES}" | tr ',' ' ')"; \
    if [ -n "$(printf '%s' "${python_global_packages}" | tr -d '[:space:]')" ]; then \
        python3 -m pip install --no-cache-dir -- $python_global_packages; \
    fi; \
    go_module_specs="$(printf '%s' "${ALCATRAZ_GO_MODULES}" | tr ',' ' ')"; \
    if [ -n "$(printf '%s' "${go_module_specs}" | tr -d '[:space:]')" ]; then \
        mkdir -p /tmp/alcatraz-go-deps; \
        cd /tmp/alcatraz-go-deps; \
        go mod init alcatraz-deps >/dev/null 2>&1 || true; \
        for spec in $go_module_specs; do \
            case "$spec" in \
                *@*) module_spec="$spec" ;; \
                *) module_spec="${spec}@latest" ;; \
            esac; \
            go mod download "$module_spec"; \
        done; \
    fi

RUN groupadd --gid "${GID}" agent \
    && useradd --uid "${UID}" --gid "${GID}" --create-home --shell /bin/bash agent \
    && mkdir -p /workspace /home/agent/.cache /home/agent/.config \
    && chown -R agent:agent /workspace /home/agent \
    && rm -rf /var/lib/apt/lists/*

COPY docker/agent/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

WORKDIR /workspace
USER agent

ENV HOME=/home/agent

ENTRYPOINT ["/usr/bin/tini", "--", "/bin/sh", "/usr/local/bin/docker-entrypoint.sh"]
CMD ["sleep", "infinity"]
