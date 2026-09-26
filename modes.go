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
	case "sidebar":
		return cockpit.Run(arg(1))
	case "transcript": // transcript <path> <title>
		return cockpit.Transcript(arg(1), arg(2))
	case "cockpit": // cockpit <session> <client window id> <client pane id>
		t := tmux.New()
		session := arg(1)
		if session == "" {
			var err error
			if session, err = cockpit.CurrentSession(t); err != nil {
				return err
			}
		}
		return cockpit.New(t, session).Toggle(arg(2), arg(3))
	case "placeholder":
		return cockpit.Placeholder(arg(1))
	case "count":
		cockpit.Count()
		return nil
	}
	return errNotCockpit
}
