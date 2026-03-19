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

CONFIG_PATH="${CODEX_HOME}/config.toml"
WORKSPACE_PROJECT='[projects."/workspace"]'
if [ ! -f "${CONFIG_PATH}" ]; then
    : > "${CONFIG_PATH}"
    chmod 600 "${CONFIG_PATH}"
fi
if ! grep -Fq "${WORKSPACE_PROJECT}" "${CONFIG_PATH}"; then
    printf '\n%s\ntrust_level = "trusted"\n' "${WORKSPACE_PROJECT}" >> "${CONFIG_PATH}"
fi

exec "$@"
