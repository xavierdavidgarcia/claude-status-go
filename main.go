package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/xgarcia/claude-status-go/pkg"
	"github.com/xgarcia/claude-status-go/pkg/update"
	"github.com/xgarcia/claude-status-go/pkg/version"
)

// ── ANSI helpers ────────────────────────────────────────
const (
	dim   = "\033[2m"
	reset = "\033[0m"
)

func rgb(r, g, b int) string {
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

// ── Bar Style ──────────────────────────────────────────
type BarStyle struct {
	Filled string
	Empty  string
}

var barStyles = map[string]BarStyle{
	"classic":   {Filled: "●", Empty: "○"},
	"block":     {Filled: "█", Empty: "░"},
	"braille":   {Filled: "⣿", Empty: "⠀"},
	"line":      {Filled: "━", Empty: "─"},
	"geometric": {Filled: "▰", Empty: "▱"},
}

func resolveBarStyle() string {
	// 1. CLI flag: --bar <name>
	for i, arg := range os.Args[1:] {
		if arg == "--bar" && i+1 < len(os.Args[1:]) {
			next := os.Args[i+2]
			if _, ok := barStyles[next]; ok {
				return next
			}
		}
		if strings.HasPrefix(arg, "--bar=") {
			name := strings.TrimPrefix(arg, "--bar=")
			if _, ok := barStyles[name]; ok {
				return name
			}
		}
	}
	// 2. Environment variable
	if name := os.Getenv("CLAUDE_STATUSLINE_BAR"); name != "" {
		if _, ok := barStyles[name]; ok {
			return name
		}
	}
	return "line"
}

func barStyleNames() []string {
	names := make([]string, 0, len(barStyles))
	for name := range barStyles {
		names = append(names, name)
	}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[i] > names[j] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return names
}

// ── Theme ───────────────────────────────────────────────
type Theme struct {
	Blue, Orange, Green, Cyan, Red, Yellow, White, Magenta string
}

var themes = map[string]Theme{
	"default": {
		Blue:    rgb(0, 153, 255),
		Orange:  rgb(255, 176, 85),
		Green:   rgb(0, 175, 80),
		Cyan:    rgb(86, 182, 194),
		Red:     rgb(255, 85, 85),
		Yellow:  rgb(230, 200, 0),
		White:   rgb(220, 220, 220),
		Magenta: rgb(180, 140, 255),
	},
	"dracula": {
		Blue:    rgb(189, 147, 249),
		Orange:  rgb(255, 184, 108),
		Green:   rgb(80, 250, 123),
		Cyan:    rgb(139, 233, 253),
		Red:     rgb(255, 85, 85),
		Yellow:  rgb(241, 250, 140),
		White:   rgb(248, 248, 242),
		Magenta: rgb(255, 121, 198),
	},
	"catppuccin": {
		Blue:    rgb(137, 180, 250),
		Orange:  rgb(250, 179, 135),
		Green:   rgb(166, 227, 161),
		Cyan:    rgb(148, 226, 213),
		Red:     rgb(243, 139, 168),
		Yellow:  rgb(249, 226, 175),
		White:   rgb(205, 214, 244),
		Magenta: rgb(203, 166, 247),
	},
	"solarized": {
		Blue:    rgb(38, 139, 210),
		Orange:  rgb(203, 75, 22),
		Green:   rgb(133, 153, 0),
		Cyan:    rgb(42, 161, 152),
		Red:     rgb(220, 50, 47),
		Yellow:  rgb(181, 137, 0),
		White:   rgb(147, 161, 161),
		Magenta: rgb(108, 113, 196),
	},
	"mono": {
		Blue:    rgb(170, 170, 170),
		Orange:  rgb(153, 153, 153),
		Green:   rgb(204, 204, 204),
		Cyan:    rgb(187, 187, 187),
		Red:     rgb(136, 136, 136),
		Yellow:  rgb(153, 153, 153),
		White:   rgb(204, 204, 204),
		Magenta: rgb(170, 170, 170),
	},
}

// Resolved theme colors — set in main()
var (
	blue, orange, green, cyan, red, yellow, white, magenta string
	sep                                                     string
)

func resolveTheme() string {
	// 1. CLI flag: --theme <name>
	for i, arg := range os.Args[1:] {
		if arg == "--theme" && i+1 < len(os.Args[1:])-0 {
			next := os.Args[i+2]
			if _, ok := themes[next]; ok {
				return next
			}
		}
		if strings.HasPrefix(arg, "--theme=") {
			name := strings.TrimPrefix(arg, "--theme=")
			if _, ok := themes[name]; ok {
				return name
			}
		}
	}
	// 2. Environment variable
	if name := os.Getenv("CLAUDE_STATUSLINE_THEME"); name != "" {
		if _, ok := themes[name]; ok {
			return name
		}
	}
	return "default"
}

func themeNames() []string {
	names := make([]string, 0, len(themes))
	for name := range themes {
		names = append(names, name)
	}
	// Sort for stable output
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[i] > names[j] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return names
}

func applyTheme(name string) {
	t := themes[name]
	blue = t.Blue
	orange = t.Orange
	green = t.Green
	cyan = t.Cyan
	red = t.Red
	yellow = t.Yellow
	white = t.White
	magenta = t.Magenta
	sep = " " + dim + "│" + reset + " "
}

// ── Input JSON structures ───────────────────────────────
type Input struct {
	Model         ModelInfo     `json:"model"`
	ContextWindow ContextWindow `json:"context_window"`
	Session       Session       `json:"session"`
	Cost          CostInfo      `json:"cost"`
	Cwd           string        `json:"cwd"`
}

type CostInfo struct {
	TotalCostUSD      float64 `json:"total_cost_usd"`
	TotalDurationMs   int64   `json:"total_duration_ms"`
	TotalApiDurationMs int64  `json:"total_api_duration_ms"`
	TotalLinesAdded   int     `json:"total_lines_added"`
	TotalLinesRemoved int     `json:"total_lines_removed"`
}

type ModelInfo struct {
	DisplayName string `json:"display_name"`
}

type ContextWindow struct {
	ContextWindowSize int          `json:"context_window_size"`
	TotalOutputTokens int          `json:"total_output_tokens"`
	CurrentUsage      CurrentUsage `json:"current_usage"`
}

type CurrentUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type Session struct {
	StartTime string `json:"start_time"`
}

// ── Usage API structures ────────────────────────────────
type UsageResponse struct {
	FiveHour   UsageBucket `json:"five_hour"`
	SevenDay   UsageBucket `json:"seven_day"`
	ExtraUsage ExtraUsage  `json:"extra_usage"`
}

type UsageBucket struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type ExtraUsage struct {
	IsEnabled    bool    `json:"is_enabled"`
	Utilization  float64 `json:"utilization"`
	UsedCredits  float64 `json:"used_credits"`
	MonthlyLimit float64 `json:"monthly_limit"`
}

// ── Helpers ─────────────────────────────────────────────
func formatTokens(num int) string {
	if num >= 1000000 {
		return fmt.Sprintf("%.1fm", float64(num)/1000000)
	}
	if num >= 1000 {
		return fmt.Sprintf("%.1fk", float64(num)/1000)
	}
	return fmt.Sprintf("%d", num)
}

func colorForPct(pct int) string {
	if pct >= 90 {
		return red
	}
	if pct >= 70 {
		return yellow
	}
	if pct >= 50 {
		return orange
	}
	return green
}

func buildBar(pct, width int, style BarStyle) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := pct * width / 100
	empty := width - filled
	barColor := colorForPct(pct)

	return barColor + strings.Repeat(style.Filled, filled) + dim + strings.Repeat(style.Empty, empty) + reset
}

func parseISO(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05Z",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time: %s", s)
}

func format12Hour(t time.Time) (int, string) {
	h := t.Hour() % 12
	if h == 0 {
		h = 12
	}
	ampm := "am"
	if t.Hour() >= 12 {
		ampm = "pm"
	}
	return h, ampm
}

func formatResetTime(isoStr, style string) string {
	if isoStr == "" || isoStr == "null" {
		return ""
	}
	t, err := parseISO(isoStr)
	if err != nil {
		return ""
	}
	t = t.Local()

	switch style {
	case "time":
		h, ampm := format12Hour(t)
		return fmt.Sprintf("%d:%02d%s", h, t.Minute(), ampm)
	case "datetime":
		h, ampm := format12Hour(t)
		return fmt.Sprintf("%s %d, %d:%02d%s",
			strings.ToLower(t.Format("Jan")), t.Day(), h, t.Minute(), ampm)
	default:
		return fmt.Sprintf("%s %d", strings.ToLower(t.Format("Jan")), t.Day())
	}
}

// ── Git info ────────────────────────────────────────────
func gitBranch(cwd string) string {
	cmd := exec.Command("git", "-C", cwd, "symbolic-ref", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type GitStats struct {
	Dirty    bool
	Added    int
	Modified int
}

func gitStats(cwd string) GitStats {
	cmd := exec.Command("git", "-C", cwd, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return GitStats{}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var stats GitStats
	for _, line := range lines {
		if len(line) < 2 {
			continue
		}
		stats.Dirty = true
		code := line[:2]
		if code == "??" || code[0] == 'A' {
			stats.Added++
		} else {
			stats.Modified++
		}
	}
	return stats
}

// ── OAuth token resolution ──────────────────────────────
func getOAuthToken() string {
	// 1. Environment variable
	if token := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); token != "" {
		return token
	}

	// 2. macOS Keychain
	if runtime.GOOS == "darwin" {
		if blob, err := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output(); err == nil {
			if token := extractAccessToken(blob); token != "" {
				return token
			}
		}
	}

	// 3. Credentials file
	home, _ := os.UserHomeDir()
	credsFile := filepath.Join(home, ".claude", ".credentials.json")
	if data, err := os.ReadFile(credsFile); err == nil {
		if token := extractAccessToken(data); token != "" {
			return token
		}
	}

	// 4. Linux secret-tool
	if runtime.GOOS == "linux" {
		if blob, err := exec.Command("secret-tool", "lookup", "service", "Claude Code-credentials").Output(); err == nil {
			if token := extractAccessToken(blob); token != "" {
				return token
			}
		}
	}

	return ""
}

func extractAccessToken(data []byte) string {
	var creds map[string]map[string]interface{}
	if json.Unmarshal(data, &creds) == nil {
		if oauth, ok := creds["claudeAiOauth"]; ok {
			if token, ok := oauth["accessToken"].(string); ok && token != "" {
				return token
			}
		}
	}
	return ""
}

// ── Usage data fetch with cache ─────────────────────────
const (
	cacheDir    = "/tmp/claude"
	cacheFile   = "/tmp/claude/statusline-usage-cache.json"
	cacheMaxAge = 60 // seconds
)

func fetchUsageData() *UsageResponse {
	_ = os.MkdirAll(cacheDir, 0755)

	// Check cache
	if info, err := os.Stat(cacheFile); err == nil {
		age := time.Since(info.ModTime()).Seconds()
		if age < cacheMaxAge {
			if data, err := os.ReadFile(cacheFile); err == nil {
				var usage UsageResponse
				if json.Unmarshal(data, &usage) == nil {
					return &usage
				}
			}
		}
	}

	// Fetch from API
	token := getOAuthToken()
	if token != "" {
		client := &http.Client{Timeout: 5 * time.Second}
		req, err := http.NewRequest("GET", "https://api.anthropic.com/api/oauth/usage", nil)
		if err == nil {
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("anthropic-beta", "oauth-2025-04-20")
			req.Header.Set("User-Agent", "claude-code/2.1.34")

			if resp, err := client.Do(req); err == nil {
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				var usage UsageResponse
				if json.Unmarshal(body, &usage) == nil {
					_ = os.WriteFile(cacheFile, body, 0644)
					return &usage
				}
			}
		}
	}

	// Fallback to stale cache
	if data, err := os.ReadFile(cacheFile); err == nil {
		var usage UsageResponse
		if json.Unmarshal(data, &usage) == nil {
			return &usage
		}
	}

	return nil
}

// ── Effort level from settings ──────────────────────────
func getEffortLevel() string {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return "default"
	}
	var settings map[string]interface{}
	if json.Unmarshal(data, &settings) != nil {
		return "default"
	}
	if effort, ok := settings["effortLevel"].(string); ok && effort != "" {
		return effort
	}
	return "default"
}

// ── Agent status tracking ───────────────────────────────
type AgentStatus struct {
	Active    int `json:"active"`
	Completed int `json:"completed"`
}

func getAgentStatus(pid int) AgentStatus {
	trackFile := fmt.Sprintf("/tmp/claude/agents-%d.json", pid)
	data, err := os.ReadFile(trackFile)
	if err != nil {
		return AgentStatus{}
	}
	var status AgentStatus
	if json.Unmarshal(data, &status) != nil {
		return AgentStatus{}
	}
	return status
}

// ── Burn rate calculation ───────────────────────────────
func calcBurnRate(usedCents float64) (dailyRate float64, projected float64, show bool) {
	now := time.Now()
	dayOfMonth := now.Day()
	if dayOfMonth < 2 {
		return 0, 0, false
	}
	dailyRate = usedCents / float64(dayOfMonth)
	daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, now.Location()).Day()
	projected = dailyRate * float64(daysInMonth)
	return dailyRate, projected, true
}

// ── Next month first day ────────────────────────────────
func nextMonthFirstDay() string {
	now := time.Now()
	first := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
	return fmt.Sprintf("%s %d", strings.ToLower(first.Format("Jan")), first.Day())
}

func main() {
	// Resolve and apply theme early so colors are available everywhere
	themeName := resolveTheme()
	applyTheme(themeName)

	// Resolve bar style
	barName := resolveBarStyle()
	bar := barStyles[barName]

	// Handle CLI flags
	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--version", "-v":
			pkg.PrintVersion()
			return
		case "--update":
			if err := update.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "--themes":
			fmt.Printf("Available themes: %s\n", strings.Join(themeNames(), ", "))
			return
		case "--bars":
			fmt.Printf("Available bar styles: %s\n", strings.Join(barStyleNames(), ", "))
			return
		case "--theme":
			i++ // skip the theme name argument
		case "--bar":
			i++ // skip the bar name argument
		}
	}

	// If stdin is a TTY (no pipe), run in standalone demo mode
	standalone := false
	var data Input

	if fileInfo, _ := os.Stdin.Stat(); fileInfo.Mode()&os.ModeCharDevice != 0 {
		standalone = true
		cwd, _ := os.Getwd()
		data = Input{
			Model:   ModelInfo{DisplayName: "Claude Opus 4.6"},
			Session: Session{StartTime: time.Now().Add(-42 * time.Minute).UTC().Format(time.RFC3339)},
			Cost: CostInfo{
				TotalCostUSD:       3.45,
				TotalLinesAdded:    482,
				TotalLinesRemoved:  37,
				TotalApiDurationMs: 198000,
			},
			Cwd:     cwd,
			ContextWindow: ContextWindow{
				ContextWindowSize: 200000,
				TotalOutputTokens: 24500,
				CurrentUsage: CurrentUsage{
					InputTokens:              65000,
					CacheCreationInputTokens: 8000,
					CacheReadInputTokens:     53000,
				},
			},
		}
	} else {
		input, _ := io.ReadAll(os.Stdin)
		if len(strings.TrimSpace(string(input))) == 0 {
			fmt.Print("Claude")
			return
		}
		// Dump raw JSON for debugging
		for _, arg := range os.Args[1:] {
			if arg == "--dump" {
				_ = os.WriteFile("/tmp/claude/statusline-stdin-dump.json", input, 0644)
				break
			}
		}
		if err := json.Unmarshal(input, &data); err != nil {
			fmt.Print("Claude")
			return
		}
	}

	// ── Agent status ───────────────────────────────────
	var agentStatus AgentStatus
	if standalone {
		// Demo data: show sample agent activity
		agentStatus = AgentStatus{Active: 2, Completed: 5}
	} else {
		agentStatus = getAgentStatus(os.Getppid())
	}

	// ── Parse input fields ──────────────────────────────
	modelName := data.Model.DisplayName
	if modelName == "" {
		modelName = "Claude"
	}

	size := data.ContextWindow.ContextWindowSize
	if size == 0 {
		size = 200000
	}

	current := data.ContextWindow.CurrentUsage.InputTokens +
		data.ContextWindow.CurrentUsage.CacheCreationInputTokens +
		data.ContextWindow.CurrentUsage.CacheReadInputTokens

	pctUsed := 0
	if size > 0 {
		pctUsed = current * 100 / size
	}

	effort := getEffortLevel()

	// ── Directory and git ───────────────────────────────
	cwd := data.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	// shorten home prefix to ~
	home, _ := os.UserHomeDir()
	displayCwd := cwd
	if home != "" && strings.HasPrefix(cwd, home) {
		displayCwd = "~" + cwd[len(home):]
	}

	branch := gitBranch(cwd)
	gStats := gitStats(cwd)

	// ── Session duration ────────────────────────────────
	sessionDuration := ""
	if data.Session.StartTime != "" {
		if startTime, err := parseISO(data.Session.StartTime); err == nil {
			elapsed := int(time.Since(startTime).Seconds())
			if elapsed >= 3600 {
				sessionDuration = fmt.Sprintf("%dh%dm", elapsed/3600, (elapsed%3600)/60)
			} else if elapsed >= 60 {
				sessionDuration = fmt.Sprintf("%dm", elapsed/60)
			} else {
				sessionDuration = fmt.Sprintf("%ds", elapsed)
			}
		}
	}

	// ── LINE 1: Hostname │ Model │ Context % │ Session │ Effort ────
	hostname, _ := os.Hostname()
	if idx := strings.Index(hostname, "."); idx != -1 {
		hostname = hostname[:idx]
	}
	pctColor := colorForPct(pctUsed)

	line1 := "💻 " + magenta + hostname + reset
	line1 += sep
	line1 += blue + modelName + reset
	line1 += sep
	line1 += "🪟 " + white + formatTokens(current) + dim + "/" + reset + white + formatTokens(size) + reset + " " + pctColor + fmt.Sprintf("(%d%%)", pctUsed) + reset

	// Cache hit ratio
	totalInput := data.ContextWindow.CurrentUsage.InputTokens + data.ContextWindow.CurrentUsage.CacheReadInputTokens
	if totalInput > 0 {
		cacheRatio := data.ContextWindow.CurrentUsage.CacheReadInputTokens * 100 / totalInput
		if cacheRatio > 0 {
			cacheColor := dim
			if cacheRatio >= 70 {
				cacheColor = green
			} else if cacheRatio >= 40 {
				cacheColor = yellow
			}
			line1 += " " + cacheColor + fmt.Sprintf("💾 %d%% cache", cacheRatio) + reset
		}
	}

	if sessionDuration != "" {
		line1 += sep
		line1 += dim + "⏱ " + reset + white + sessionDuration + reset
	}

	if data.Cost.TotalCostUSD > 0 {
		line1 += sep
		costColor := green
		if data.Cost.TotalCostUSD >= 10 {
			costColor = red
		} else if data.Cost.TotalCostUSD >= 5 {
			costColor = yellow
		} else if data.Cost.TotalCostUSD >= 2 {
			costColor = orange
		}
		line1 += costColor + fmt.Sprintf("💰 $%.2f", data.Cost.TotalCostUSD) + reset
	}

	line1 += sep
	switch effort {
	case "high":
		line1 += magenta + "● " + effort + reset
	case "medium":
		line1 += dim + "◑ " + effort + reset
	case "low":
		line1 += dim + "◔ " + effort + reset
	default:
		line1 += dim + "◑ " + effort + reset
	}

	// ── LINE 2: Directory (branch) ──────────────────────
	line2 := "📂 " + cyan + displayCwd + reset

	if branch != "" {
		dirtyStr := ""
		if gStats.Dirty {
			dirtyStr = red + "*"
		}
		statsStr := ""
		if gStats.Added > 0 {
			statsStr += " " + green + fmt.Sprintf("+%d", gStats.Added) + reset
		}
		if gStats.Modified > 0 {
			statsStr += " " + yellow + fmt.Sprintf("~%d", gStats.Modified) + reset
		}
		line2 += " " + green + "(" + branch + dirtyStr + green + statsStr + green + ")" + reset
	}

	// ── LINE 3: Session stats ──────────────────────────
	line3 := ""
	hasStats := data.Cost.TotalLinesAdded > 0 || data.Cost.TotalLinesRemoved > 0 ||
		data.ContextWindow.TotalOutputTokens > 0 || data.Cost.TotalApiDurationMs > 0

	if hasStats {
		parts := []string{}

		if data.Cost.TotalLinesAdded > 0 || data.Cost.TotalLinesRemoved > 0 {
			parts = append(parts, green+fmt.Sprintf("+%d", data.Cost.TotalLinesAdded)+reset+
				" "+red+fmt.Sprintf("-%d", data.Cost.TotalLinesRemoved)+reset+dim+" lines"+reset)
		}

		if data.ContextWindow.TotalOutputTokens > 0 {
			parts = append(parts, white+formatTokens(data.ContextWindow.TotalOutputTokens)+reset+dim+" tokens out"+reset)
		}

		if data.Cost.TotalApiDurationMs > 0 {
			apiSecs := int(data.Cost.TotalApiDurationMs / 1000)
			apiTime := ""
			if apiSecs >= 3600 {
				apiTime = fmt.Sprintf("%dh%dm", apiSecs/3600, (apiSecs%3600)/60)
			} else if apiSecs >= 60 {
				apiTime = fmt.Sprintf("%dm%ds", apiSecs/60, apiSecs%60)
			} else {
				apiTime = fmt.Sprintf("%ds", apiSecs)
			}
			parts = append(parts, white+apiTime+reset+dim+" api"+reset)
		}

		line3 = "📊 " + strings.Join(parts, sep)
	}

	// ── LINE 4: Agent status ───────────────────────────
	agentLine := ""
	if agentStatus.Active > 0 || agentStatus.Completed > 0 {
		parts := []string{}
		if agentStatus.Active > 0 {
			parts = append(parts, orange+fmt.Sprintf("%d running", agentStatus.Active)+reset)
		}
		if agentStatus.Completed > 0 {
			parts = append(parts, green+fmt.Sprintf("%d done", agentStatus.Completed)+reset)
		}
		agentLine = "🤖 " + strings.Join(parts, sep)
	}

	// ── Rate limit lines ────────────────────────────────
	usageData := fetchUsageData()
	rateLines := ""

	if usageData != nil {
		barWidth := 10

		fiveHourPct := int(math.Round(usageData.FiveHour.Utilization))
		fiveHourReset := formatResetTime(usageData.FiveHour.ResetsAt, "time")
		fiveHourBar := buildBar(fiveHourPct, barWidth, bar)
		fiveHourPctColor := colorForPct(fiveHourPct)

		rateLines += fmt.Sprintf("%scurrent%s %s %s%3d%%%s %s⟳%s  %s%s%s",
			white, reset, fiveHourBar, fiveHourPctColor, fiveHourPct, reset, dim, reset, white, fiveHourReset, reset)

		sevenDayPct := int(math.Round(usageData.SevenDay.Utilization))
		sevenDayReset := formatResetTime(usageData.SevenDay.ResetsAt, "datetime")
		sevenDayBar := buildBar(sevenDayPct, barWidth, bar)
		sevenDayPctColor := colorForPct(sevenDayPct)

		rateLines += fmt.Sprintf("\n%sweekly%s  %s %s%3d%%%s %s⟳%s  %s%s%s",
			white, reset, sevenDayBar, sevenDayPctColor, sevenDayPct, reset, dim, reset, white, sevenDayReset, reset)

		if usageData.ExtraUsage.IsEnabled {
			extraPct := int(math.Round(usageData.ExtraUsage.Utilization))
			extraUsed := fmt.Sprintf("%.2f", usageData.ExtraUsage.UsedCredits/100)
			extraLimit := fmt.Sprintf("%.2f", usageData.ExtraUsage.MonthlyLimit/100)
			extraBar := buildBar(extraPct, barWidth, bar)
			extraPctColor := colorForPct(extraPct)
			extraResetStr := nextMonthFirstDay()

			extraLine := fmt.Sprintf("\n%sextra%s   %s %s$%s%s/%s%s$%s%s %s⟳%s  %s%s%s",
				white, reset, extraBar, extraPctColor, extraUsed, dim, reset, white, extraLimit, reset, dim, reset, white, extraResetStr, reset)

			if dailyRate, projected, show := calcBurnRate(usageData.ExtraUsage.UsedCredits); show && usageData.ExtraUsage.MonthlyLimit > 0 {
				projColor := green
				projRatio := projected / usageData.ExtraUsage.MonthlyLimit * 100
				if projRatio >= 100 {
					projColor = red
				} else if projRatio >= 80 {
					projColor = yellow
				}
				extraLine += fmt.Sprintf("  📈 %s$%.2f/day%s → %s$%.2f proj%s",
					dim, dailyRate/100, reset, projColor, projected/100, reset)
			}

			rateLines += extraLine
		}
	}

	// ── Output ──────────────────────────────────────────
	if standalone {
		fmt.Printf("\n  %sclaude-status-go%s %s(v%s)%s\n", blue, reset, dim, pkg.BuildVersion, reset)
		fmt.Printf("  %s─────────────────────────────────────────%s\n\n", dim, reset)
		fmt.Print("  ")
	}

	fmt.Print(line1)
	if standalone {
		fmt.Print("\n\n  " + line2)
		if line3 != "" {
			fmt.Print("\n  " + line3)
		}
		if agentLine != "" {
			fmt.Print("\n  " + agentLine)
		}
	} else {
		fmt.Print("\n\n" + line2)
		if line3 != "" {
			fmt.Print("\n" + line3)
		}
		if agentLine != "" {
			fmt.Print("\n" + agentLine)
		}
	}
	if rateLines != "" {
		if standalone {
			fmt.Print("\n\n  ")
			fmt.Print(strings.ReplaceAll(rateLines, "\n", "\n  "))
		} else {
			fmt.Print("\n\n" + rateLines)
		}
	}

	if standalone {
		fmt.Printf("\n\n  %s─────────────────────────────────────────%s\n", dim, reset)
		fmt.Printf("  %sThis is a preview with sample data.%s\n", dim, reset)
		fmt.Printf("  %sIn Claude Code, real data is piped via stdin.%s\n\n", dim, reset)
		fmt.Printf("  %sTheme:%s %s\n", white, reset, themeName)
		fmt.Printf("  %sBar:%s   %s\n\n", white, reset, barName)
		fmt.Printf("  %sFlags:%s\n", white, reset)
		fmt.Printf("    --version        Show version info\n")
		fmt.Printf("    --update         Self-update to latest release\n")
		fmt.Printf("    --theme <name>   Set color theme\n")
		fmt.Printf("    --themes         List available themes\n")
		fmt.Printf("    --bar <name>     Set bar style\n")
		fmt.Printf("    --bars           List available bar styles\n\n")
	}

	// ── Update notification (non-blocking, silent on error) ──
	if newVersion, available := version.CheckForUpdate(pkg.BuildVersion); available {
		if standalone {
			fmt.Printf("  %s⬆ Update available: v%s → v%s%s %s(run with --update)%s\n\n",
				yellow, pkg.BuildVersion, newVersion, reset, dim, reset)
		} else {
			fmt.Printf("\n\n%s⬆ Update available: v%s → v%s%s %s(run with --update)%s",
				yellow, pkg.BuildVersion, newVersion, reset, dim, reset)
		}
	}
}
