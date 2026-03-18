FROM debian:bookworm-slim

ARG UID=1000
ARG GID=1000

ENV DEBIAN_FRONTEND=noninteractive
ENV HOME=/home/agent

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        curl \
        git \
        jq \
        procps \
        ripgrep \
        tini \
    && groupadd --gid "${GID}" agent \
    && useradd --uid "${UID}" --gid "${GID}" --create-home --shell /bin/bash agent \
    && mkdir -p /workspace /home/agent/.cache /home/agent/.config \
    && chown -R agent:agent /workspace /home/agent \
    && rm -rf /var/lib/apt/lists/*

COPY docker/agent/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

WORKDIR /workspace
USER agent

ENTRYPOINT ["/usr/bin/tini", "--", "/bin/sh", "/usr/local/bin/docker-entrypoint.sh"]
CMD ["sleep", "infinity"]
