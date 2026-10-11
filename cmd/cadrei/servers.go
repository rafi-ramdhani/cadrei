//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// defaultServers are the sockets of the tmux servers cadrei uses when the
// environment names none: tmux's default one, and a development build's
// (devSocket), as their paths are with no TMUX_TMPDIR. A development build
// on a scratch HOME never looks at the default one, where the user's own
// teams run: its own teams are on its own server. Tests, which may never
// reach the user's own server, set none.
var defaultServers = defaultSockets

// defaultSockets is defaultServers' list, by install kind.
func defaultSockets() []string {
	dir := filepath.Join("/tmp", fmt.Sprintf("tmux-%d", os.Getuid()))
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	if devSocket(installKind()) != "" || (installKind() == "dev" && os.Getenv("CADREI_DEV_REAL_HOME") != "1") {
		return []string{filepath.Join(dir, "cadrei-dev")}
	}
	return []string{filepath.Join(dir, "default"), filepath.Join(dir, "cadrei-dev")}
}

// servers are every tmux server a crew's team may run on, whatever the
// environment names now: the one it names, each one a crew's sessions
// were started on (cadreis.RecordServer), and the default ones. A member
// can name another server, or a folder with none in it, but not move its
// own team off the server it runs on.
func servers(list []cadreis.Cadrei) []session.Tmux {
	out := []session.Tmux{session.Default()}
	seen := map[string]bool{out[0].Path(): true}
	add := func(path string) {
		if path != "" && !seen[path] {
			seen[path] = true
			out = append(out, session.At(path))
		}
	}
	for _, c := range list {
		for _, s := range cadreis.Servers(c) {
			add(s)
		}
	}
	for _, s := range defaultServers() {
		add(s)
	}
	return out
}

// recordServer notes the server a session of the crew is started on. A
// crew whose notes can't take it isn't started: the commands that move or
// delete crews couldn't find that team.
func (e *env) recordServer(c cadreis.Cadrei, t session.Tmux) bool {
	if err := cadreis.RecordServer(c, t.Path()); err != nil {
		e.failWith("Couldn't note which tmux server runs this crew's sessions: "+sentence(err.Error()), "Nothing was started.")
		return false
	}
	return true
}
