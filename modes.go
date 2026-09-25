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
		return cockpit.Run(cockpit.Popup, arg(1))
	case "sidebar":
		return cockpit.Run(cockpit.Sidebar, arg(1))
	case "cockpit": // cockpit <session> <client window id>
		t := tmux.New()
		session := arg(1)
		if session == "" {
			var err error
			if session, err = cockpit.CurrentSession(t); err != nil {
				return err
			}
		}
		return cockpit.New(t, session).Toggle(arg(2))
	case "placeholder":
		return cockpit.Placeholder(arg(1))
	case "count":
		cockpit.Count()
		return nil
	}
	return errNotCockpit
}
