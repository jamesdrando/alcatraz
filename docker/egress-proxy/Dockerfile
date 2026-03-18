FROM debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        squid \
        tini \
    && rm -rf /var/lib/apt/lists/*

COPY docker/egress-proxy/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY docker/egress-proxy/squid.conf /etc/squid/squid.conf

ENTRYPOINT ["/usr/bin/tini", "--", "/bin/sh", "/usr/local/bin/docker-entrypoint.sh"]
