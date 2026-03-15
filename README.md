# claude-status-go

A fast, single-binary statusline for [Claude Code](https://claude.ai/code). Displays hostname, model info, context usage, cache hit ratio, rate limits, git stats, session duration, and extra usage burn rate — all rendered with ANSI colors.

```
💻 Newton │ Claude Opus 4.6 │ 🪟 76.5k/200k (38%) 💾 82% cache │ ⏱ 42m │ ● high

📂 ~/project (main* +3 ~2)

current  ━━━───────  30%  ⟳  2:30pm
weekly   ━━────────  20%  ⟳  mar 18, 9:00am
extra    ━─────────  $4.50/$20.00  ⟳  apr 1  📈 $0.64/day → $19.84 proj
```

## Install

### One-liner (requires `gh`)

```bash
gh release download --repo xgarcia/claude-status-go --pattern "install.sh" --output - | bash
```

### From source

```bash
git clone https://github.com/xgarcia/claude-status-go.git
cd claude-status-go
make install
```

### Custom install with theme and bar style

```bash
make install THEME=dracula BAR=block
```

This bakes the flags into `~/.claude/settings.json` so they apply automatically.

`make install` builds the binary and installs it to `~/.claude/claude-status-go`, then updates `~/.claude/settings.json` with the statusLine config. Restart Claude Code to activate.

## Themes

Set via `--theme <name>` flag, `CLAUDE_STATUSLINE_THEME` env var, or `make install THEME=<name>`.

| Theme | Description |
|-------|-------------|
| `default` | Vibrant colors — blue, green, orange |
| `dracula` | Purple-tinted dark theme |
| `catppuccin` | Soft pastel palette |
| `solarized` | Ethan Schoonover's classic palette |
| `mono` | Grayscale, no color distractions |

List all themes:

```bash
./claude-status-go --themes
```

## Bar Styles

Set via `--bar <name>` flag, `CLAUDE_STATUSLINE_BAR` env var, or `make install BAR=<name>`.

| Name | Filled | Empty | Example |
|------|--------|-------|---------|
| `line` (default) | `━` | `─` | `━━━───────` |
| `classic` | `●` | `○` | `●●●○○○○○○○` |
| `block` | `█` | `░` | `███░░░░░░░` |
| `braille` | `⣿` | `⠀` | `⣿⣿⣿⠀⠀⠀⠀⠀⠀⠀` |
| `geometric` | `▰` | `▱` | `▰▰▰▱▱▱▱▱▱▱` |

List all styles:

```bash
./claude-status-go --bars
```

## Usage

Claude Code pipes JSON to stdin with model, context window, session, and cwd data. The binary parses it and outputs ANSI-colored text.

Run without a pipe to see a standalone demo with sample data:

```bash
./claude-status-go
```

## Flags

| Flag | Description |
|------|-------------|
| `--version`, `-v` | Print version info |
| `--update` | Self-update to the latest release |
| `--theme <name>` | Set color theme |
| `--themes` | List available themes |
| `--bar <name>` | Set bar style |
| `--bars` | List available bar styles |

## What it shows

**Line 1** — Hostname, model name, token usage with context percentage, cache hit ratio, session duration, effort level

**Line 2** — Working directory with git branch, dirty indicator, and file counts (added/modified)

**Rate limits** — 5-hour and 7-day usage bars with reset times, plus extra usage credits with daily burn rate and projected monthly spend

### Cache hit ratio

Shows what percentage of input tokens came from cache reads. Color-coded: green (>=70%), yellow (>=40%), dim otherwise. Hidden when there's no cache activity.

### Git stats

Parses `git status --porcelain` to show:
- `*` — dirty indicator (red)
- `+N` — added/untracked files (green)
- `~N` — modified files (yellow)

### Burn rate

For extra usage, shows daily spend rate and projected monthly total. Only displayed after day 1 of the month. Projection color: green (<80% of limit), yellow (80-100%), red (over limit).

## Requirements

- `jq` — used by the installer to update `settings.json`
- `git` — for branch and status info
- `curl` — for rate limit API calls

## OAuth token resolution

The binary looks for an Anthropic OAuth token in this order:

1. `CLAUDE_CODE_OAUTH_TOKEN` environment variable
2. macOS Keychain (`security find-generic-password`)
3. `~/.claude/.credentials.json`
4. Linux `secret-tool`

Rate limit data is fetched from the Anthropic usage API and cached to `/tmp/claude/statusline-usage-cache.json` with a 60-second TTL.

## Makefile targets

| Target | Description |
|--------|-------------|
| `make install` | Build, copy to `~/.claude/`, update settings |
| `make install THEME=dracula BAR=block` | Install with custom theme and bar style |
| `make build` | Build the binary only |
| `make uninstall` | Remove binary, restore backups, clean settings |
| `make test` | Run tests |
| `make clean` | Remove local binary |
| `make release-snapshot` | Build a goreleaser snapshot |

## Uninstall

```bash
make uninstall
```

This restores any previous statusline configuration from backup.
