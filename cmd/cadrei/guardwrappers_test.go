//go:build !windows

package main

import (
	"strings"
	"testing"
)

// The guard reads a wrapper's options as the wrapper does: an option's
// value (env -u TMUX) is never taken for the program, and env's split
// string is the command env runs, read as a shell's code. The program's
// name stays case-folded: on macOS's disk, TMUX runs tmux.
func TestTheGuardReadsWrapperOptions(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	const member = "work-dev-engineer"
	const compactRefused = "Team members can't run cadrei compact. Ask the user to do it from the orchestrator."
	for _, who := range []string{"", member} {
		for _, command := range []string{
			// How the engineer and the reviewer keep tests off the user's
			// tmux server.
			`env -u CADREI_HOME -u CADREI_MEMBER -u TMUX -u CLAUDE_CONFIG_DIR TMUX_TMPDIR=/tmp/t go test ./cmd/cadrei -run 'TestCadreiPull$' -count=1`,
			`env -u TMUX go test ./...`, `env -u TMUX -u CLAUDE_CONFIG_DIR go test ./...`, `env -u TMUX -v go test ./...`,
			`env -u TMUX -L x go build ./...`, `env -iu TMUX go test ./...`, `env --unset TMUX -u CLAUDE_CONFIG_DIR go test ./...`,
			`env -C /tmp -u TMUX go test ./...`, `timeout -s KILL 60 env -u TMUX go test ./...`,
			`sudo -u tmux -u x true`, `sudo -u tmux echo hi`, `doas -u tmux echo hi`, `nice -n 5 go test ./...`,
			`stdbuf -o L go test ./...`, `caffeinate -t 60 go test ./...`,
			`env -S 'echo hi'`, `env -S'go test ./...'`, `env --split-string='echo hi' there`,
		} {
			if code, out, errOut := guardAs(t, c, who, "default", command); code != 0 || out != "" || errOut != "" {
				t.Errorf("%q, member %t: exit %d, %q %q", command, who != "", code, out, errOut)
			}
		}
		for _, command := range []string{
			`env -u TMUX tmux send-keys -t x 1`, `env -u TMUX TMUX send-keys -t x 1`, `TMUX send-keys -t x 1`,
			`env -u A -S 'tmux send-keys -t x 1'`, `env -S'tmux send-keys -t x 1'`, `env -iS'tmux send-keys -t x 1'`,
			`env -S'tmux' send-keys -t x 1`, `env --split-string='tmux send-keys -t x 1'`, `env --split-string 'tmux send-keys -t x 1'`,
			`env -u A --split-string='sh -c "tmux send-keys -t x 1"'`,
			`sudo -u me -g staff tmux send-keys -t x 1`, `timeout -k 5 -s KILL 10 tmux send-keys -t x 1`,
			`nice -n 5 tmux send-keys -t x 1`, `env -u A -u B tmux send-keys -t x 1`,
		} {
			if code, out, errOut := guardAs(t, c, who, "default", command); code != 2 || out != "" || strings.TrimSpace(errOut) != typingRefused {
				t.Errorf("%q, member %t: exit %d, %q %q", command, who != "", code, out, errOut)
			}
		}
	}
	for _, command := range []string{
		`env --split-string='cadrei compact dev/engineer'`, `env -S'cadrei compact dev/engineer'`,
		`env -S 'cadrei' compact dev/engineer`, `env -u TMUX cadrei compact dev/engineer`,
		`sudo -u tmux cadrei compact dev/engineer`, `timeout -s KILL 5 cadrei compact dev/engineer`,
	} {
		if code, out, errOut := guardAs(t, c, member, "default", command); code != 2 || out != "" || strings.TrimSpace(errOut) != compactRefused {
			t.Errorf("member %q: exit %d, %q %q", command, code, out, errOut)
		}
	}
}
