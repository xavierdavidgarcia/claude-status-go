package pkg

import (
	"fmt"
	"strconv"
	"time"
)

var (
	// BuildVersion is set at compile time via ldflags.
	BuildVersion = "dev"
	// BuildCommit is set at compile time via ldflags.
	BuildCommit = ""
	// BuildTime is set at compile time via ldflags (unix timestamp).
	BuildTime = ""
)

func PrintVersion() {
	fmt.Printf("Version: %s\n", BuildVersion)
	if BuildCommit != "" {
		fmt.Printf("Commit: %s\n", BuildCommit)
	}
	if BuildTime != "" {
		if ts, err := strconv.ParseInt(BuildTime, 10, 64); err == nil {
			BuildTime = time.Unix(ts, 0).String()
		}
		fmt.Printf("Build Time: %s\n", BuildTime)
	}
}
