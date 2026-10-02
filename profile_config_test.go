package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileCredentialsNeverFallBackToDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"default-synthetic"}}`), 0600)
	profile := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", profile)
	if got := getOAuthToken(); got != "" {
		t.Fatal("profile fell back to default account")
	}
	os.WriteFile(filepath.Join(profile, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"profile-synthetic"}}`), 0600)
	if got := getOAuthToken(); got != "profile-synthetic" {
		t.Fatal("profile credentials were not selected")
	}
}

func TestUsageCachesStayWithinProfile(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	first := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", first)
	firstPath := usageCachePath()
	os.MkdirAll(filepath.Dir(firstPath), 0700)
	os.WriteFile(firstPath, []byte(`{"five_hour":{"utilization":11}}`), 0600)
	usage := fetchUsageData()
	if usage == nil {
		t.Fatal("profile cache not read")
	}
	encoded, _ := json.Marshal(usage)
	if !strings.Contains(string(encoded), "11") {
		t.Fatal("unexpected cache contents")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if usageCachePath() == firstPath {
		t.Fatal("cache path shared across profiles")
	}
	if fetchUsageData() != nil {
		t.Fatal("other profile cache was used")
	}
}
