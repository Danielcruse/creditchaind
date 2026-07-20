#!/usr/bin/env bash
set -uo pipefail

# Catches exactly the failure modes named in the production-readiness
# plan's Phase 4 (monitoring/alerting): process down, disk filling up,
# block height not advancing, and this node's chain hash diverging from
# its peer's - see audit/KNOWN_GAPS.md's "no monitoring/alerting
# infrastructure yet" entry, which this closes.
#
# Deliberately does NOT depend on any new external service/signup to be
# useful today: every failure is logged clearly (journalctl -u
# creditchain-healthcheck.service shows it), which is already better than
# the current "caught manually/reactively" state. If HEALTHCHECK_PING_URL
# is set (a free "heartbeat"/"dead man's switch" monitor - e.g.
# healthchecks.io or UptimeRobot's heartbeat monitor type - both have free
# tiers), this script pings it on a clean run; if the script ever stops
# running at all (box down, process crash-looped), the external monitor's
# own "no heartbeat received" alert is what actually pages a human - this
# script cannot page anyone by itself without paid infrastructure, so this
# heartbeat pattern is the practical way to get real alerting today.
#
# Required environment (set via /etc/creditchain-healthcheck.env, same
# pattern as creditchain-backup.env):
#   HEALTHCHECK_LOCAL_URL   - this node's own API base (e.g. http://localhost:8080)
#   HEALTHCHECK_PEER_URL    - the OTHER node's API base, for divergence checks
#   HEALTHCHECK_STATE_FILE  - where to remember the last-seen block height
#                             (e.g. /var/lib/creditchain-healthcheck/state)
# Optional:
#   HEALTHCHECK_PING_URL       - heartbeat URL, pinged on a clean run
#   HEALTHCHECK_DISK_PATH      - filesystem to check (default: DATA_DIR or /)
#   HEALTHCHECK_DISK_THRESHOLD - percent-used alert threshold (default: 85)
#   HEALTHCHECK_STALL_SECONDS  - how long block height may sit still before
#                                it's treated as stalled (default: 300)

: "${HEALTHCHECK_LOCAL_URL:?HEALTHCHECK_LOCAL_URL must be set}"
: "${HEALTHCHECK_STATE_FILE:?HEALTHCHECK_STATE_FILE must be set}"
DISK_PATH="${HEALTHCHECK_DISK_PATH:-${DATA_DIR:-/}}"
DISK_THRESHOLD="${HEALTHCHECK_DISK_THRESHOLD:-85}"
STALL_SECONDS="${HEALTHCHECK_STALL_SECONDS:-300}"

FAILURES=()

# --- 1. Is the backend process actually running? ---------------------------
if ! systemctl is-active --quiet creditchain.service; then
    FAILURES+=("creditchain.service is not active")
fi

# --- 2. Disk space --------------------------------------------------------
DISK_USED=$(df --output=pcent "$DISK_PATH" 2>/dev/null | tail -1 | tr -dc '0-9')
if [ -n "$DISK_USED" ] && [ "$DISK_USED" -ge "$DISK_THRESHOLD" ]; then
    FAILURES+=("disk usage at ${DISK_USED}% on $DISK_PATH (threshold ${DISK_THRESHOLD}%)")
fi

# --- 3. Query this node's own /network -------------------------------------
LOCAL_JSON=$(curl -sk -m 10 "${HEALTHCHECK_LOCAL_URL}/network" 2>/dev/null)
if [ -z "$LOCAL_JSON" ]; then
    FAILURES+=("could not reach ${HEALTHCHECK_LOCAL_URL}/network at all")
else
    LOCAL_HEIGHT=$(echo "$LOCAL_JSON" | grep -o '"blockHeight":[0-9]*' | grep -o '[0-9]*')
    LOCAL_HASH=$(echo "$LOCAL_JSON" | grep -o '"latestBlockHash":"[^"]*"' | cut -d'"' -f4)

    # --- 4. Block height stall detection ---
    NOW=$(date +%s)
    PREV_HEIGHT=""
    PREV_SINCE="$NOW"
    if [ -f "$HEALTHCHECK_STATE_FILE" ]; then
        read -r PREV_HEIGHT PREV_SINCE < "$HEALTHCHECK_STATE_FILE" 2>/dev/null || true
    fi

    if [ "$LOCAL_HEIGHT" = "$PREV_HEIGHT" ]; then
        STALLED_FOR=$((NOW - PREV_SINCE))
        if [ "$STALLED_FOR" -ge "$STALL_SECONDS" ]; then
            FAILURES+=("block height stuck at $LOCAL_HEIGHT for ${STALLED_FOR}s (threshold ${STALL_SECONDS}s)")
        fi
    else
        PREV_SINCE="$NOW"
    fi

    mkdir -p "$(dirname "$HEALTHCHECK_STATE_FILE")"
    echo "$LOCAL_HEIGHT $PREV_SINCE" > "$HEALTHCHECK_STATE_FILE"

    # --- 5. Cross-node divergence check ---
    if [ -n "${HEALTHCHECK_PEER_URL:-}" ]; then
        PEER_JSON=$(curl -sk -m 10 "${HEALTHCHECK_PEER_URL}/network" 2>/dev/null)
        if [ -z "$PEER_JSON" ]; then
            FAILURES+=("could not reach peer at ${HEALTHCHECK_PEER_URL}/network")
        else
            PEER_HEIGHT=$(echo "$PEER_JSON" | grep -o '"blockHeight":[0-9]*' | grep -o '[0-9]*')
            PEER_HASH=$(echo "$PEER_JSON" | grep -o '"latestBlockHash":"[^"]*"' | cut -d'"' -f4)

            # Same height, different hash = an actual fork - the highest-
            # severity failure mode this whole script exists to catch (see
            # audit/HARDENING_HISTORY.md's two real production forks).
            if [ "$LOCAL_HEIGHT" = "$PEER_HEIGHT" ] && [ "$LOCAL_HASH" != "$PEER_HASH" ]; then
                FAILURES+=("FORK DETECTED: same height ($LOCAL_HEIGHT) but different hash from peer (local=$LOCAL_HASH peer=$PEER_HASH)")
            fi
        fi
    fi
fi

# --- Report ------------------------------------------------------------
if [ ${#FAILURES[@]} -eq 0 ]; then
    echo "healthcheck OK: height=${LOCAL_HEIGHT:-?} hash=${LOCAL_HASH:-?}"
    if [ -n "${HEALTHCHECK_PING_URL:-}" ]; then
        curl -sk -m 10 "$HEALTHCHECK_PING_URL" >/dev/null 2>&1 || true
    fi
    exit 0
else
    echo "healthcheck FAILED:"
    for f in "${FAILURES[@]}"; do
        echo "  - $f"
    done
    exit 1
fi
