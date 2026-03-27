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

# Use session_id as the canonical key (shared across all subagents in the same session)
SESSION_ID=$(echo "$INPUT" | grep -o '"session_id":"[^"]*"' | head -1 | cut -d'"' -f4 || true)
if [ -z "$SESSION_ID" ]; then
    SESSION_ID="ppid-${PPID}"
fi

TRACK_FILE="${TRACK_DIR}/agents-session-${SESSION_ID}.json"

# Create a PPID symlink so the status line binary can find this file via os.Getppid()
PPID_LINK="${TRACK_DIR}/agents-pid-${PPID}.link"
echo "$TRACK_FILE" > "$PPID_LINK"

# Extract agent description from tool_input
DESC=$(echo "$INPUT" | grep -o '"description":"[^"]*"' | head -1 | cut -d'"' -f4 || true)

# Detect hook type via hook_event_name field (set by Claude Code)
HOOK_TYPE=$(echo "$INPUT" | grep -o '"hook_event_name":"[^"]*"' | head -1 | cut -d'"' -f4 || true)

# Initialize if missing
if [ ! -f "$TRACK_FILE" ]; then
    echo '{"active":0,"completed":0,"running_names":[],"done_names":[]}' > "$TRACK_FILE"
fi

# Use jq for array manipulation if available, otherwise just track counts
if command -v jq >/dev/null 2>&1; then
    case "$HOOK_TYPE" in
        PreToolUse)
            jq --arg desc "$DESC" \
                '.active = (.active + 1) | .running_names = (.running_names + [$desc])' \
                "$TRACK_FILE" > "${TRACK_FILE}.tmp" && mv "${TRACK_FILE}.tmp" "$TRACK_FILE"
            ;;
        PostToolUse)
            jq --arg desc "$DESC" \
                '.active = ([.active - 1, 0] | max) | .completed = (.completed + 1) | .running_names = (.running_names - [$desc]) | .done_names = (.done_names + [$desc])' \
                "$TRACK_FILE" > "${TRACK_FILE}.tmp" && mv "${TRACK_FILE}.tmp" "$TRACK_FILE"
            ;;
    esac
else
    ACTIVE=$(grep -o '"active":[0-9]*' "$TRACK_FILE" | cut -d: -f2 || echo "0")
    COMPLETED=$(grep -o '"completed":[0-9]*' "$TRACK_FILE" | cut -d: -f2 || echo "0")
    ACTIVE=${ACTIVE:-0}
    COMPLETED=${COMPLETED:-0}
    case "$HOOK_TYPE" in
        PreToolUse)
            ACTIVE=$((ACTIVE + 1))
            ;;
        PostToolUse)
            if [ "$ACTIVE" -gt 0 ]; then ACTIVE=$((ACTIVE - 1)); fi
            COMPLETED=$((COMPLETED + 1))
            ;;
    esac
    echo "{\"active\":${ACTIVE},\"completed\":${COMPLETED},\"running_names\":[],\"done_names\":[]}" > "$TRACK_FILE"
fi
