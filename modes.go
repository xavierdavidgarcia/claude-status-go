package main

import (
	"errors"

	"github.com/xgarcia/claude-status-go/pkg/cockpit"
	"github.com/xgarcia/claude-status-go/pkg/tmux"
)

var errNotCockpit = errors.New("not a cockpit mode")

func runCockpitMode(args []string) error {
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch args[0] {
	case "popup":
		return cockpit.Run(cockpit.Popup)
	case "sidebar":
		return cockpit.Run(cockpit.Sidebar)
	case "cockpit":
		return cockpit.New(tmux.New()).Open(arg(1))
	case "placeholder":
		return cockpit.Placeholder()
	case "count":
		cockpit.Count()
		return nil
	}
	return errNotCockpit
}
