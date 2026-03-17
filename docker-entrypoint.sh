#!/bin/sh
set -e

# Set up writable runtime directories for agent-installed packages.
# Rootfs is read-only; /app/data is a writable Docker volume.
RUNTIME_DIR="/app/data/.runtime"
mkdir -p "$RUNTIME_DIR/pip" "$RUNTIME_DIR/npm-global/lib"

# Python: allow agent to pip install to writable target dir
export PYTHONPATH="$RUNTIME_DIR/pip:${PYTHONPATH:-}"
export PIP_TARGET="$RUNTIME_DIR/pip"
export PIP_BREAK_SYSTEM_PACKAGES=1
export PIP_CACHE_DIR="$RUNTIME_DIR/pip-cache"
mkdir -p "$RUNTIME_DIR/pip-cache"

# Node.js: allow agent to npm install -g to writable prefix
# NODE_PATH includes both pre-installed system globals and runtime-installed globals.
export NPM_CONFIG_PREFIX="$RUNTIME_DIR/npm-global"
export NODE_PATH="/usr/local/lib/node_modules:$RUNTIME_DIR/npm-global/lib/node_modules:${NODE_PATH:-}"
export PATH="$RUNTIME_DIR/npm-global/bin:$RUNTIME_DIR/pip/bin:$PATH"

# System packages: re-install on-demand packages persisted across recreates.
# Runs as root (entrypoint) — no doas needed.
APK_LIST="$RUNTIME_DIR/apk-packages"
if [ -f "$APK_LIST" ] && [ -s "$APK_LIST" ]; then
  echo "Re-installing persisted system packages..."
  # shellcheck disable=SC2046
  apk add --no-cache $(cat "$APK_LIST" | sort -u) 2>/dev/null || \
    echo "Warning: some packages failed to install"
fi

# Start the root-privileged package helper (listens on /tmp/pkg.sock).
# It handles runtime apk install/uninstall requests from the non-root app process.
if [ -x /app/pkg-helper ]; then
  /app/pkg-helper &
fi

case "${1:-serve}" in
  serve)
    # Auto-upgrade (schema migrations + data hooks) before starting.
    if [ -n "$GOCLAW_POSTGRES_DSN" ]; then
      echo "Running database upgrade..."
      su-exec goclaw /app/goclaw upgrade || \
        echo "Upgrade warning (may already be up-to-date)"
    fi
    exec su-exec goclaw /app/goclaw
    ;;
  upgrade)
    shift
    exec su-exec goclaw /app/goclaw upgrade "$@"
    ;;
  migrate)
    shift
    exec su-exec goclaw /app/goclaw migrate "$@"
    ;;
  onboard)
    exec su-exec goclaw /app/goclaw onboard
    ;;
  version)
    exec su-exec goclaw /app/goclaw version
    ;;
  *)
    exec su-exec goclaw /app/goclaw "$@"
    ;;
esac
