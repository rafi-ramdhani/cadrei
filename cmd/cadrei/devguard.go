//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// devHomeRefusal says why a development build won't run, or "": it runs
// only with HOME in a temporary folder, so trying a change never moves or
// rewrites a real setup's files, unless CADREI_DEV_REAL_HOME=1 says the
// real HOME is meant. Release and Homebrew builds always run.
func devHomeRefusal(kind string) string {
	if kind != "dev" || os.Getenv("CADREI_DEV_REAL_HOME") == "1" {
		return ""
	}
	const fix = "Run it with HOME set to a scratch folder (for example HOME=$(mktemp -d)), or set CADREI_DEV_REAL_HOME=1 if you mean your real setup. " +
		"To use cadrei, install a release instead (see the README)."
	// Without a HOME, or with a relative one, the programs cadrei starts
	// find the real home on their own.
	home := os.Getenv("HOME")
	if !filepath.IsAbs(home) {
		return "This is a development build of cadrei, and HOME isn't set to a full path, so the programs it starts would use your real setup. " + fix
	}
	if !scratch(home) {
		return "This is a development build of cadrei, and HOME (" + paths.Real(home) + ") isn't a temporary folder, so it would change your real setup. " + fix
	}
	// These point cadrei, or its runtime, at a setup whatever HOME is.
	vars := []string{"CADREI_HOME"}
	if rt, err := runtime.Get(runtimeName()); err == nil {
		vars = append(vars, rt.SetupVars()...)
	}
	for _, v := range vars {
		if dir := os.Getenv(v); dir != "" && (!filepath.IsAbs(dir) || !scratch(dir)) {
			return "This is a development build of cadrei, and " + v + " (" + dir + ") isn't in a temporary folder, so it would change your real setup. " +
				"Unset it, or set it inside your scratch HOME."
		}
	}
	return ""
}

// tempRoots are the system's temporary folders. TMPDIR isn't one of them:
// set to / or a parent of the real home, it would make any folder count.
var tempRoots = []string{"/tmp", "/private/tmp", "/var/folders", "/private/var/folders"}

// scratch reports whether dir is inside a temporary folder (not the
// folder itself), as a real path.
func scratch(dir string) bool {
	dir = paths.Real(dir)
	for _, tmp := range tempRoots {
		if t := paths.Real(tmp); paths.Within(dir, t) && dir != t {
			return true
		}
	}
	return false
}

// devGuard stops a development build run on a real HOME (devHomeRefusal).
// One that runs keeps to a tmux server of its own (devSocket) unless a
// socket is named: the default server is where the user's own teams run.
func devGuard() bool {
	kind := framework.Kind(version)
	if why := devHomeRefusal(kind); why != "" {
		os.Stderr.WriteString("cadrei: " + why + "\n")
		return false
	}
	if s := devSocket(kind); s != "" {
		os.Setenv("CADREI_TMUX_SOCKET", s)
	}
	return true
}

// devSocket is the tmux socket a development build on a scratch HOME uses
// when none is named, or "": its sessions never share the server of the
// user's own teams.
func devSocket(kind string) string {
	if kind != "dev" || os.Getenv("CADREI_DEV_REAL_HOME") == "1" || os.Getenv("CADREI_TMUX_SOCKET") != "" {
		return ""
	}
	return "cadrei-dev"
}

// readsOnly are the commands a user expects only to read: they never move
// 0.2.0's notes in, which writes and commits in each crew.
var readsOnly = map[string]bool{"help": true, "--version": true, "--check": true, groupHelpName: true}

// movesIn reports whether a command moves 0.2.0's notes in first: every
// one but the hooks (which run on their own, often, and must be quick) and
// the read-only ones.
func movesIn(name string) bool {
	return !strings.HasPrefix(name, "hook ") && !readsOnly[name]
}
