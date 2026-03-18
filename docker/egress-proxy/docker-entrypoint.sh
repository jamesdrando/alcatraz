#!/bin/sh
set -eu

ALLOWLIST_FILE=/tmp/allowed-domains.acl
: > "${ALLOWLIST_FILE}"

for domain in ${SQUID_ALLOWED_DOMAINS:-api.openai.com files.oaiusercontent.com}; do
    case "${domain}" in
        .*)
            printf '%s\n' "${domain}" >> "${ALLOWLIST_FILE}"
            ;;
        *)
            printf '%s\n' "${domain}" >> "${ALLOWLIST_FILE}"
            printf '.%s\n' "${domain}" >> "${ALLOWLIST_FILE}"
            ;;
    esac
done

exec squid -N -f /etc/squid/squid.conf
