#!/usr/bin/env bash
set -euo pipefail

PLUGIN_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

BOOTSTRAP_POSTURE="deny"
BOOTSTRAP_SOURCE="default deny"

apply_posture_file() {
  local file="$1" line normalized value=""
  [ -e "$file" ] || return 1
  if [ ! -r "$file" ]; then
    BOOTSTRAP_POSTURE="deny"
    BOOTSTRAP_SOURCE="unreadable config: $file"
    return 0
  fi
  while IFS= read -r line || [ -n "$line" ]; do
    normalized="$(printf '%s' "$line" | tr -d ' \t\r')"
    case "$normalized" in
      on_error=\"abstain\"*) value="abstain" ;;
      on_error=\"deny\"*)    value="deny" ;;
    esac
  done < "$file"
  if [ -n "$value" ]; then
    BOOTSTRAP_POSTURE="$value"
    BOOTSTRAP_SOURCE="on_error = \"$value\" in $file"
  fi
  return 0
}

apply_first_posture_file() {
  local file
  for file in "$@"; do
    if apply_posture_file "$file"; then
      return
    fi
  done
}

resolve_bootstrap_posture() {
  local config_home="${XDG_CONFIG_HOME:-${HOME:-}/.config}"
  apply_first_posture_file \
    "$config_home/gatekeeper/gatekeeper.toml" \
    "${HOME:-}/.claude/gatekeeper.toml"
  apply_first_posture_file \
    "$PWD/.gatekeeper/gatekeeper.toml" \
    "$PWD/.claude/gatekeeper.toml"
}

bootstrap_failure() {
  echo "Error: gatekeeper bootstrap exhausted binary, download, and Go build recovery paths." >&2
  echo "Remediation: install bin/claude-gatekeeper or rerun bin/install.sh after restoring release/network access." >&2
  if [ "${GATEKEEPER_BOOTSTRAP_ABSTAIN:-}" = "1" ]; then
    echo "WARNING: abstaining because GATEKEEPER_BOOTSTRAP_ABSTAIN=1; no gatekeeper policy is enforced." >&2
    exit 0
  fi
  resolve_bootstrap_posture
  if [ "$BOOTSTRAP_POSTURE" = "abstain" ]; then
    echo "WARNING: abstaining because $BOOTSTRAP_SOURCE; no gatekeeper policy is enforced." >&2
    exit 0
  fi
  echo "Blocking tool call because $BOOTSTRAP_SOURCE." >&2
  exit 2
}

# 1. Pre-built binary (from make build or install.sh download).
BINARY="$PLUGIN_ROOT/bin/claude-gatekeeper"
if [ -x "$BINARY" ]; then
  exec "$BINARY" "$@"
fi

# 2. Auto-download from GitHub Releases.
if [ -x "$PLUGIN_ROOT/bin/install.sh" ]; then
  echo "Downloading claude-gatekeeper binary..." >&2
  if "$PLUGIN_ROOT/bin/install.sh" 2>&1 >&2; then
    if [ -x "$BINARY" ]; then
      exec "$BINARY" "$@"
    fi
  fi
  echo "Download failed, trying go build..." >&2
fi

# 3. Fallback: build from source (requires Go).
if command -v go &>/dev/null; then
  echo "Building claude-gatekeeper..." >&2
  if (cd "$PLUGIN_ROOT" && go build -ldflags "-s -w" -o bin/claude-gatekeeper ./cmd/claude-gatekeeper) >&2; then
    if [ -x "$BINARY" ]; then
      exec "$BINARY" "$@"
    fi
  fi
fi

bootstrap_failure
