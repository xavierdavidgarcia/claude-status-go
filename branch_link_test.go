package main

import "testing"

func TestRepoWebURL(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:acme/app.git\n":        "https://github.com/acme/app",
		"https://github.com/acme/app.git":      "https://github.com/acme/app",
		"https://user:tok@github.com/acme/app": "https://github.com/acme/app",
		"ssh://git@github.com/acme/app.git":    "https://github.com/acme/app",
		"/srv/git/app.git":                     "",
	} {
		if got := repoWebURL(remote); got != want {
			t.Errorf("repoWebURL(%q) = %q, want %q", remote, got, want)
		}
	}
}
