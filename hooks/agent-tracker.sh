#!/bin/bash
# Claude Code hook script for tracking Agent tool invocations.
# Installed by claude-status-go with --agents flag.
# Receives hook event JSON on stdin, updates agent counts in /tmp/claude/.

TRACK_DIR="/tmp/claude"
INPUT=$(cat)

TOOL_NAME=$(echo "$INPUT" | grep -o '"tool_name":"[^"]*"' | head -1 | cut -d'"' -f4 || true)

# Only track Agent tool invocations
if [ "$TOOL_NAME" != "Agent" ] && [ "$TOOL_NAME" != "Task" ]; then
    exit 0
fi

mkdir -p "$TRACK_DIR"

# Use PPID (Claude Code process) as the tracking key
TRACK_FILE="${TRACK_DIR}/agents-${PPID}.json"

# Initialize if missing
if [ ! -f "$TRACK_FILE" ]; then
    echo '{"active":0,"completed":0}' > "$TRACK_FILE"
fi

# Read current counts
ACTIVE=$(grep -o '"active":[0-9]*' "$TRACK_FILE" | cut -d: -f2 || echo "0")
COMPLETED=$(grep -o '"completed":[0-9]*' "$TRACK_FILE" | cut -d: -f2 || echo "0")
ACTIVE=${ACTIVE:-0}
COMPLETED=${COMPLETED:-0}

# Determine hook type from env or input
HOOK_TYPE="${CLAUDE_HOOK_EVENT:-}"
if [ -z "$HOOK_TYPE" ]; then
    HOOK_TYPE=$(echo "$INPUT" | grep -o '"hook_type":"[^"]*"' | head -1 | cut -d'"' -f4 || true)
fi

case "$HOOK_TYPE" in
    PreToolUse)
        ACTIVE=$((ACTIVE + 1))
        ;;
    PostToolUse)
        if [ "$ACTIVE" -gt 0 ]; then
            ACTIVE=$((ACTIVE - 1))
        fi
        COMPLETED=$((COMPLETED + 1))
        ;;
esac

echo "{\"active\":${ACTIVE},\"completed\":${COMPLETED}}" > "$TRACK_FILE"
