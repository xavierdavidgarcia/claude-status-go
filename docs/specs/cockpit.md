# Cockpit: pilot every Claude session from tmux

Status: draft, 2026-09-25

## Goal

One place to see every Claude Code session, know which ones need you, and act
on them without hunting through ~50 windows.

A cockpit window (`prefix+g`, toggles), styled after herdr (Catppuccin Mocha):

- **spaces**: tmux sessions, each with its most urgent agent state and branch;
- **agents**: the Claude sessions of the selected space, with tab and age;
- **git**: changes / log / worktrees of the selection; Enter opens a diff,
  commit (popup) or the worktree's agent.

The right side follows the cursor: the selected agent's real pane (or the
space's current pane) is swapped in after a short settle delay.

Plus a tmux status segment (`⚠ 2 ● 5`). A `prefix+G` popup was tried and
dropped: the panel does the same job better.

## Non-goals

- Replacing tmux (herdr's route).
- Auto-approving permissions, broadcasting to all agents by default,
  force-deleting worktrees.
- Talking to Claude's internal messaging socket.

## Modes

`claude-status-go` keeps rendering the statusline with no arguments. New modes:

| Command | Purpose |
|---|---|
| `claude-status-go cockpit` | create or focus the cockpit window |
| `claude-status-go sidebar` | the list pane inside the cockpit window |
| `claude-status-go count` | status segment for `status-right` |
| `claude-status-go hook <event>` | Claude Code hook handler (reads JSON on stdin) |

Code layout: `pkg/agents` (discovery + state), `pkg/tmux` (command wrapper behind
an interface so it can be faked in tests), `pkg/cockpit` (Bubble Tea TUI shared
by the sidebar), `pkg/hook`. `main.go` dispatches on `os.Args[1]`; the
statusline path imports none of the TUI code.

## Discovery

1. Config dirs: every `/run/user/$UID/cc-socks/*.sock` → pid →
   `CLAUDE_CONFIG_DIR` from `/proc/<pid>/environ` (default `~/.claude`), plus any
   dirs listed in the config file. Today that's three dirs, ~28 processes.
2. For each dir read `sessions/<pid>.json`. Drop entries whose pid is dead or
   whose `/proc/<pid>/stat` start time doesn't match `procStart` (pid reuse).
3. Pane: the registry's `tmux` field (`1:@51.%99`); the pane id `%99` is the
   stable key. Fallback: walk `/proc/<pid>/stat` ppids up to a `pane_pid` from
   `tmux list-panes -a`. Sessions outside tmux are listed but can't be jumped to.
4. One `tmux list-panes -a -F '#{pane_id} #{session_name} #{window_index} #{window_name}'`
   per refresh gives each pane's current location (it changes when swapped).

The registry schema is internal to Claude Code: parse leniently, never fail on
unknown or missing fields.

## State

| State | Source |
|---|---|
| working | registry `busy` |
| idle | registry `idle`, or `Stop` hook |
| needs you | registry `waiting` (+`waitingFor`), or `PermissionRequest` hook |
| error | `StopFailure` hook |
| shell | registry `shell` (user is in a `!` command) |
| stale | nothing newer than N minutes and pid alive but unknown status |

Hook handler writes `$XDG_RUNTIME_DIR/claude-cockpit/<session_id>.json`
atomically (tmp + rename): last event, time, tool name + command preview for
permission requests, last assistant message for Stop. When both sources exist,
the newer timestamp wins. The handler must finish in ~10 ms and never block
Claude: no network, no tmux calls. It also sends `notify-send` on
PermissionRequest and on Stop after a long turn.

Row shows: state glyph, name, project/branch, age in state, last message or
pending command.

## Cockpit window

A tmux window named `cockpit`: left pane runs `sidebar`, right pane is the slot.

- **Select** (Enter or mouse click): if an agent is already borrowed, swap it
  back first; then `swap-pane -s <agent %id> -t <slot>`. The agent's home
  window now holds the slot pane, which runs `claude-status-go placeholder`
  ("infra-2 is in the cockpit — Enter to bring it back").
- **g**: return the agent to its home window and jump there.
- **p**: prompt the selected agent(s). **space**: multi-select.
- **n**: new agent: pick repo, name → `new-window -c <repo> 'claude -w <name> -n <name>'`.
- **/**: filter. **q**: return any borrowed pane, then quit.

The borrowed pair (agent pane, slot pane) is saved to the runtime dir so a
restarted sidebar can restore it.

## Sending prompts

`tmux send-keys -t %id -l "<text>"` then `send-keys -t %id Enter`.
Right before sending, re-read state: refuse when the target is `needs you`,
`shell`, or `stale` (typing into a permission dialog can pick an option).
Multi-send shows the recipient list and reports per-target success.

## tmux setup (installed by `make install`, opt-in)

Prefix here is `C-a`; `a` is taken (last-window), `g`/`G` are free.
`run-shell` expands formats, so the binding passes the session and window.

```tmux
bind g run-shell 'claude-status-go cockpit "#{session_name}" "#{window_id}" "#{pane_id}"'
set -ag status-right ' #(claude-status-go count)'
```

## Phases

**0. Verify — done 2026-09-25:** on a Bash permission prompt the registry shows
`status: "waiting"`, `waitingFor: "permission prompt"` within ~1 s and returns
to `idle` once answered; `procStart` is field 22 of `/proc/<pid>/stat`; the file
is removed on exit and absent until the folder is trusted. The MVP needs no
hooks for "needs you".

**1. MVP:** discovery + state from registry, count
segment, cockpit window with swap-in and placeholder, guarded prompt to one
agent, spawn in worktree.

**2.** Hook handler (permission preview, last message, errors, notifications),
multi-select prompt, cost/context columns via a snapshot the statusline writes.

**3.** Codex adapter (capture-pane patterns, as herdr does), worktree cleanup,
remote hosts.

## Risks

- **Killing the cockpit window kills the borrowed agent.** Mitigate: sidebar
  traps SIGHUP/SIGTERM and swaps back; `q` swaps back; `cockpit` on start
  restores any pair left in the runtime dir. Document "use q, not kill-window".
- **Registry schema changes** (`tmux`, `waiting` are undocumented): lenient
  parsing, hooks as a second source.
- **Wrong-target input**: pane ids only, never names or cwd; revalidate before send.
- **Hook latency**: Go binary, no shell, no tmux calls in the hot path.
- **Anthropic ships this natively** (`claude agents` covers background sessions
  today): keep the tool thin.

## Testing

- `pkg/agents`: fixture registry files + fake `/proc` reader → state merge,
  pid reuse, missing fields, multiple config dirs.
- `pkg/tmux`: fake runner records commands; test swap / swap-back / restore
  sequences and the send guard.
- Manual: run against the real ~50-window setup before calling phase 1 done.
