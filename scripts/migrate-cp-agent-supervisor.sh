#!/usr/bin/env bash
# migrate-cp-agent-supervisor.sh — Convert an already-installed Mac to the
# resident-wrapper agent layout (issue #39, ADR-035 §3).
#
# The fleet's Macs were installed on the OLD layout, where the
# com.uknomi.agent LaunchDaemon runs /usr/local/bin/uknomi-agent DIRECTLY.
# This one-shot migrates such a Mac to the resident-wrapper layout — the same
# conversion done by hand on the bench — WITHOUT re-enrolling: the device's
# cert, key, and agent-config under /var/uknomi are left untouched. It:
#   1. Lays out AGENT_DIR (/var/uknomi/agent-update) and installs the bundled
#      version-stamped agent binary as AGENT_DIR/current. The stamp is
#      load-bearing — an unstamped self-updating agent reports a stale version
#      and CP re-pushes forever (issue #39), so we install the packaged stamped
#      binary rather than reusing the old on-disk one.
#   2. Installs the supervisor wrapper at /usr/local/bin/uknomi-agent-supervisor.
#   3. Rewrites the com.uknomi.agent plist so launchd supervises the WRAPPER
#      (ProgramArguments) with AGENT_DIR/AGENT_ARGS in EnvironmentVariables.
#   4. Reloads the LaunchDaemon (unload → load).
#
# Fleet Macs have no AWS creds, so the binary comes from the install package /
# operator push (CP_AGENT_BIN_SRC), never `aws s3` on-device.
#
# Required env:
#   CP_AGENT_BIN_SRC   Path to the version-stamped uknomi-agent binary to install
#                      as AGENT_DIR/current (an agent-dist release build).
#   CP_SUPERVISOR_SRC  Path to uknomi-agent-supervisor.sh (the resident wrapper).
#
# Optional env (mostly test hooks):
#   CP_ROOT   Root prefix for /var, /usr/local, /Library (default empty = real /)
set -euo pipefail

: "${CP_AGENT_BIN_SRC:?CP_AGENT_BIN_SRC is required}"
: "${CP_SUPERVISOR_SRC:?CP_SUPERVISOR_SRC is required}"

ROOT="${CP_ROOT:-}"

runtime_dir="${ROOT}/var/uknomi"
agent_config="${runtime_dir}/agent-config.json"
agent_dir="${runtime_dir}/agent-update"
bin_dir="${ROOT}/usr/local/bin"
supervisor_dest="${bin_dir}/uknomi-agent-supervisor"
plist_label="com.uknomi.agent"
plist_path="${ROOT}/Library/LaunchDaemons/${plist_label}.plist"
out_log="/var/log/uknomi-agent.log"
err_log="/var/log/uknomi-agent-error.log"

if [[ ! -x "$CP_AGENT_BIN_SRC" ]]; then
    echo "agent binary not found / not executable at ${CP_AGENT_BIN_SRC}" >&2
    exit 1
fi
if [[ ! -f "$CP_SUPERVISOR_SRC" ]]; then
    echo "supervisor source not found at ${CP_SUPERVISOR_SRC}" >&2
    exit 1
fi

# Refuse to migrate a Mac that was never enrolled — without an existing
# agent-config the resident wrapper has no device identity to run, and a
# half-migrated daemon would crash-loop. Re-image such a Mac with the full
# installer (module 11) instead.
if [[ ! -f "$agent_config" ]]; then
    echo "no existing install: ${agent_config} not found — run the full installer, not the migration" >&2
    exit 1
fi

# ── 1. Lay out AGENT_DIR/current from the packaged stamped binary ────────────
mkdir -p "$agent_dir"
install -m 0755 "$CP_AGENT_BIN_SRC" "${agent_dir}/current"

# ── 2. Install the supervisor wrapper ────────────────────────────────────────
mkdir -p "$bin_dir"
install -m 0755 "$CP_SUPERVISOR_SRC" "$supervisor_dest"

# ── 3. Rewrite the LaunchDaemon to supervise the wrapper ─────────────────────
# launchd runs the WRAPPER, not the agent: AGENT_DIR + AGENT_ARGS are the
# wrapper's contract (it word-splits AGENT_ARGS into the agent's argv).
# StandardErrorPath keeps the agent's stderr where the fleet already tails it.
mkdir -p "$(dirname "$plist_path")"
cat > "$plist_path" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${plist_label}</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/uknomi-agent-supervisor</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>AGENT_DIR</key>
        <string>/var/uknomi/agent-update</string>
        <key>AGENT_ARGS</key>
        <string>--config /var/uknomi/agent-config.json</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>${out_log}</string>
    <key>StandardErrorPath</key>
    <string>${err_log}</string>
</dict>
</plist>
PLIST
chmod 644 "$plist_path"
# launchd refuses to load a LaunchDaemon plist not owned by root:wheel. Guard
# on EUID so the sandboxed (non-root) test still passes; in prod the operator
# runs this under sudo and the ownership is applied.
if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
    chown root:wheel "$plist_path"
fi

# ── 4. Reload the LaunchDaemon ───────────────────────────────────────────────
# Unload the running (old-layout) daemon first, then load the rewritten plist
# so launchd picks up the supervisor Program. unload tolerates "not loaded".
launchctl unload "$plist_path" 2>/dev/null || true
launchctl load "$plist_path"

echo "migrated ${plist_label} to the resident-wrapper layout"
