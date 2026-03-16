package update

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/creativeprojects/go-selfupdate"

	"github.com/xgarcia/claude-status-go/pkg"
)

const (
	repoOwner = "xavierdavidgarcia"
	repoName  = "claude-status-go"
)

func Run() error {
	currentVersion := pkg.BuildVersion
	if currentVersion == "dev" {
		return fmt.Errorf("cannot update a development build — install a released version first")
	}

	fmt.Printf("Current version: %s (%s/%s)\n", currentVersion, runtime.GOOS, runtime.GOARCH)
	fmt.Println("Checking for updates...")

	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		return fmt.Errorf("create update source: %w", err)
	}

	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source: source,
		Validator: &selfupdate.ChecksumValidator{
			UniqueFilename: "checksums.txt",
		},
	})
	if err != nil {
		return fmt.Errorf("create updater: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	latest, found, err := updater.DetectLatest(ctx, selfupdate.NewRepositorySlug(repoOwner, repoName))
	if err != nil {
		return fmt.Errorf("detect latest version: %w", err)
	}

	if !found {
		return fmt.Errorf("no releases found for %s/%s", repoOwner, repoName)
	}

	if !latest.GreaterThan(currentVersion) {
		fmt.Printf("Already up to date (v%s)\n", currentVersion)
		return nil
	}

	fmt.Printf("Updating to v%s...\n", latest.Version())

	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}

	if err := updater.UpdateTo(ctx, latest, exe); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("Successfully updated to v%s\n", latest.Version())
	return nil
}
