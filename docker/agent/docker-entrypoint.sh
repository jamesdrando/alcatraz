#!/bin/sh
set -eu

CODEX_HOME="${CODEX_HOME:-/home/agent/.codex}"
SEED_DIR=/seed-codex

mkdir -p "${CODEX_HOME}"

if [ -f "${SEED_DIR}/auth.json" ] && [ ! -f "${CODEX_HOME}/auth.json" ]; then
    cp "${SEED_DIR}/auth.json" "${CODEX_HOME}/auth.json"
    chmod 600 "${CODEX_HOME}/auth.json"
fi

if [ -f "${SEED_DIR}/config.toml" ] && [ ! -f "${CODEX_HOME}/config.toml" ]; then
    cp "${SEED_DIR}/config.toml" "${CODEX_HOME}/config.toml"
    chmod 600 "${CODEX_HOME}/config.toml"
fi

exec "$@"
