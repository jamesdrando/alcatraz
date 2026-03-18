#!/bin/sh
set -eu

ALLOWLIST_FILE=/tmp/allowed-domains.acl
: > "${ALLOWLIST_FILE}"

for domain in ${SQUID_ALLOWED_DOMAINS:-api.openai.com chatgpt.com auth.openai.com files.oaiusercontent.com deb.debian.org security.debian.org registry.npmjs.org pypi.org files.pythonhosted.org crates.io index.crates.io static.crates.io bun.com bun.sh}; do
    printf '%s\n' "${domain}" >> "${ALLOWLIST_FILE}"
done

exec squid -N -f /etc/squid/squid.conf
