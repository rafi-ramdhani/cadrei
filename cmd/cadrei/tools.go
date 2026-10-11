//go:build !windows

package main

import (
	"runtime"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// toolCheck is one tool's row of the checks (3.5): its mark, what it says
// and how to fix it. The first run, cadrei --check and up all check git
// and tmux with these, so they say the same thing.
type toolCheck struct {
	mark      mark
	what, fix string
}

// gitCheck checks git. /usr/bin/git on macOS is there even without the
// Command Line Tools: only a git that answers counts.
func gitCheck() toolCheck {
	v, err := probeWithin("git", "--version")
	if err != nil {
		hint := "Install it with your package manager (for example " + cmd("sudo apt install git") + ")"
		if runtime.GOOS == "darwin" {
			hint = "Install it with " + cmd("xcode-select --install")
		}
		return toolCheck{markFail, "git isn't installed.", hint}
	}
	v = strings.TrimPrefix(v, "git version ")
	v, _, _ = strings.Cut(v, " ")
	return toolCheck{markDone, "git " + v, ""}
}

// tmuxCheck checks tmux: missing, or older than 3.2 (cadrei runs member
// commands without a shell, with their environment given by new-session
// -e, which tmux 3.2 added), fails. A version cadrei can't read is only a
// warning: it may well be newer.
func tmuxCheck() toolCheck {
	t := session.Default()
	text, err := t.VersionText()
	if err != nil {
		return toolCheck{markFail, "tmux isn't installed. Your team runs in it.", installHint("tmux")}
	}
	major, minor, err := t.Version()
	switch {
	case err != nil:
		return toolCheck{markWarn, "Cadrei can't tell which tmux this is (" + text + "). It needs 3.2 or newer.", upgradeHint("tmux")}
	case major < 3 || (major == 3 && minor < 2):
		return toolCheck{markFail, "tmux " + text + " is too old. Cadrei needs 3.2 or newer.", upgradeHint("tmux")}
	}
	return toolCheck{markDone, "tmux " + text, ""}
}
