package version

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/blang/semver/v4"
	"github.com/creativeprojects/go-selfupdate"
)

const cacheTTL = 24 * time.Hour

const (
	repoOwner = "xgarcia"
	repoName  = "claude-status-go"
)

type versionCache struct {
	LatestVersion string    `json:"latest_version"`
	CheckedAt     time.Time `json:"checked_at"`
}

// CheckForUpdate returns the latest version string and whether it's newer than current.
// Caches the result for 24 hours. Silently returns ("", false) on any error.
func CheckForUpdate(currentVersion string) (string, bool) {
	if currentVersion == "" || currentVersion == "dev" {
		return "", false
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	cacheDir := filepath.Join(home, ".claude", "claude-status-go")
	_ = os.MkdirAll(cacheDir, 0755)
	cacheFile := filepath.Join(cacheDir, "version-check.json")

	if cache, err := loadCache(cacheFile); err == nil {
		if time.Since(cache.CheckedAt) < cacheTTL {
			return compareVersions(cache.LatestVersion, currentVersion)
		}
	}

	latest, err := fetchLatest()
	if err != nil {
		return "", false
	}

	_ = saveCache(cacheFile, &versionCache{
		LatestVersion: latest,
		CheckedAt:     time.Now(),
	})

	return compareVersions(latest, currentVersion)
}

func compareVersions(latest, current string) (string, bool) {
	if latest == "" {
		return "", false
	}
	latestVer, err := semver.ParseTolerant(latest)
	if err != nil {
		return "", false
	}
	currentVer, err := semver.ParseTolerant(current)
	if err != nil {
		return "", false
	}
	if latestVer.GT(currentVer) {
		return latest, true
	}
	return "", false
}

func fetchLatest() (string, error) {
	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		return "", err
	}

	updater, err := selfupdate.NewUpdater(selfupdate.Config{Source: source})
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	release, found, err := updater.DetectLatest(ctx, selfupdate.NewRepositorySlug(repoOwner, repoName))
	if err != nil || !found {
		return "", err
	}

	return release.Version(), nil
}

func loadCache(path string) (*versionCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cache versionCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	return &cache, nil
}

func saveCache(path string, cache *versionCache) error {
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
