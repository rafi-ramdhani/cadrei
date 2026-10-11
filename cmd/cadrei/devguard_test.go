//go:build !windows

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
)

// Review of fix round 4 (the 04:39 move): a development build runs only
// with HOME in a temporary folder, unless CADREI_DEV_REAL_HOME=1; release
// and Homebrew builds always run.
func TestADevBuildNeedsAScratchHome(t *testing.T) {
	sandbox(t) // a temporary HOME
	if why := devHomeRefusal("dev"); why != "" {
		t.Errorf("a temporary HOME: %s", why)
	}
	t.Setenv("HOME", "/Users/someone")
	if why := devHomeRefusal("dev"); !strings.Contains(why, "isn't a temporary folder") {
		t.Errorf("a real HOME: %q", why)
	}
	for _, kind := range []string{"release", "homebrew"} {
		if why := devHomeRefusal(kind); why != "" {
			t.Errorf("%s: %s", kind, why)
		}
	}
	t.Setenv("CADREI_DEV_REAL_HOME", "1")
	if why := devHomeRefusal("dev"); why != "" {
		t.Errorf("CADREI_DEV_REAL_HOME=1: %s", why)
	}
}

// Review of fix round 5 (bugs 6a to 6c): a scratch HOME isn't enough when
// CADREI_HOME or CLAUDE_CONFIG_DIR points at a real setup, TMPDIR can't
// make any folder temporary, and a HOME that's unset or relative is
// refused, since the programs cadrei starts would find the real one.
func TestTheDevGuardSeesEveryWayToTheRealSetup(t *testing.T) {
	home := sandbox(t)
	for _, v := range []string{"CADREI_HOME", "CLAUDE_CONFIG_DIR"} {
		for _, dir := range []string{"/Users/someone/.cadrei/work", "/Users/someone/.claude", "relative/.claude"} {
			t.Setenv(v, dir)
			if why := devHomeRefusal("dev"); !strings.Contains(why, v+" ("+dir+") isn't in a temporary folder") {
				t.Errorf("%s=%s: %q", v, dir, why)
			}
		}
		t.Setenv(v, home+"/.cadrei/work")
		if why := devHomeRefusal("dev"); why != "" {
			t.Errorf("%s in the scratch HOME: %s", v, why)
		}
		t.Setenv(v, "")
	}
	for _, tmpdir := range []string{"/", "/Users"} {
		t.Setenv("TMPDIR", tmpdir)
		t.Setenv("HOME", "/Users/someone")
		if why := devHomeRefusal("dev"); !strings.Contains(why, "isn't a temporary folder") {
			t.Errorf("TMPDIR=%s: %q", tmpdir, why)
		}
	}
	for _, h := range []string{"", "relative/home"} {
		t.Setenv("HOME", h)
		if why := devHomeRefusal("dev"); !strings.Contains(why, "HOME isn't set to a full path") {
			t.Errorf("HOME=%q: %q", h, why)
		}
	}
	for _, h := range []string{"/tmp", "/private/tmp", "/var/folders"} {
		t.Setenv("HOME", h)
		if why := devHomeRefusal("dev"); why == "" {
			t.Errorf("HOME=%s, a temporary folder itself", h)
		}
	}
	// A link in a temporary folder to a real one is the real one (review
	// of fix round 6).
	link := home + "/to-usr"
	os.Symlink("/usr", link)
	t.Setenv("HOME", link)
	if why := devHomeRefusal("dev"); !strings.Contains(why, "HOME (/usr) isn't a temporary folder") {
		t.Errorf("HOME through a link: %q", why)
	}
}

// Review of fix round 6 (dev guard): a development build on a scratch HOME
// keeps to a tmux server of its own unless one is named; a release, or a
// real HOME meant, keeps the default.
func TestADevBuildKeepsToItsOwnTmuxServer(t *testing.T) {
	t.Setenv("CADREI_TMUX_SOCKET", "")
	if s := devSocket("dev"); s != "cadrei-dev" {
		t.Errorf("dev: %q", s)
	}
	for _, kind := range []string{"release", "homebrew"} {
		if s := devSocket(kind); s != "" {
			t.Errorf("%s: %q", kind, s)
		}
	}
	t.Setenv("CADREI_DEV_REAL_HOME", "1")
	if s := devSocket("dev"); s != "" {
		t.Errorf("a real HOME meant: %q", s)
	}
	t.Setenv("CADREI_DEV_REAL_HOME", "")
	t.Setenv("CADREI_TMUX_SOCKET", "mine")
	if s := devSocket("dev"); s != "" {
		t.Errorf("a socket named: %q", s)
	}
}

// help, --version and --check only read: 0.2.0's notes stay where 0.2.0
// reads them until a command that works with the crew runs.
func TestReadOnlyCommandsDontMoveNotesIn(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	os.MkdirAll(home+"/.cadrei/config/places", 0o700)
	old := home + "/.cadrei/config/places/work.json"
	os.WriteFile(old, []byte(`{"app": "~/src/app"}`), 0o600)
	// A group word alone only shows that group's help (review of fix round
	// 5, bugs 6e).
	for _, args := range [][]string{{"help"}, {"help", "advanced"}, {"--version"}, {"--check"}, {"project"}} {
		call(args...)
		if _, err := os.Stat(old); err != nil {
			t.Fatalf("%v moved 0.2.0's notes in", args)
		}
	}
	call("ls")
	w, _ := cadreis.Find("work")
	if _, err := os.Stat(old); err == nil || cadreis.Place(w, "app") != home+"/src/app" {
		t.Error("ls didn't move them in")
	}
}
