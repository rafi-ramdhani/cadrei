//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// Tests never reach the user's own tmux server: the servers the guards
// look at besides the environment's are only the ones crews recorded.
func init() { defaultServers = func() []string { return nil } }

// Review of fix round 7 (must-fix A): a member that names another tmux
// server, by CADREI_TMUX_SOCKET or TMUX_TMPDIR, is still found in its own
// window, and no teardown goes ahead while a team runs on the server it
// was started on. The reviewer's probe, both ways.
func TestAnotherTmuxServerHidesNoMember(t *testing.T) {
	for _, variant := range []string{"socket", "tmpdir"} {
		t.Run(variant, func(t *testing.T) {
			home := sandbox(t)
			socket := withTmux(t, home)
			allSetup(t, home)
			must(t, "up", "dev")
			got := cadreis.Servers(cadreis.Cadrei{Name: "work", Path: home + "/.cadrei/work"})
			if len(got) != 1 || !strings.HasSuffix(got[0], "/"+socket) {
				t.Fatalf("up recorded %v", got)
			}
			server := got[0]
			tmuxIn(socket, "new-window", "-d", "-t", "=cadrei-work-dev:", "-n", "shell", "sh -c 'sleep 60; :'")
			tmuxIn(socket, "set-option", "-w", "-t", "=cadrei-work-dev:shell", "@cadrei_member", "work-dev-engineer")
			shell, _ := strconv.Atoi(tmuxIn(socket, "display", "-p", "-t", "=cadrei-work-dev:shell", "#{pane_pid}"))
			var child int
			for range 50 {
				if out, err := exec.Command("pgrep", "-P", strconv.Itoa(shell)).Output(); err == nil {
					child, _ = strconv.Atoi(strings.TrimSpace(string(out)))
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if child == 0 {
				t.Fatal("no shell")
			}
			selfPID = func() int { return child }
			t.Cleanup(func() { selfPID = os.Getpid })
			if variant == "socket" {
				t.Setenv("CADREI_TMUX_SOCKET", "cadrei-probe-nothing-here")
			} else {
				// Short: a socket path over 104 bytes is an error of its own.
				dir, err := os.MkdirTemp("/tmp", "cadrei-probe-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.RemoveAll(dir) })
				t.Setenv("TMUX_TMPDIR", dir)
			}
			for _, args := range [][]string{{"uninstall", "--all", "--confirm", deletePhrase}, {"uninstall", "--yes"}, {"uninstall", "--archive", "--yes"}, {"restore"}} {
				refused(t, "This runs in the window of the member work-dev-engineer", args...)
			}
			// The user, with the same stray value: their teams run on the
			// server they were started on, which this can't stop.
			selfPID = os.Getpid
			for _, args := range [][]string{{"uninstall", "--all", "--confirm", deletePhrase}, {"uninstall", "--yes"}, {"uninstall", "--archive", "--yes"}} {
				refused(t, "your crews' sessions run on another tmux server than this command uses, so it can't stop them: cadrei-work-dev", args...)
			}
			for _, p := range []string{home + "/.cadrei/work", home + "/.cadrei/life", home + "/.cadrei/config", home + "/.cadrei-archive"} {
				if _, err := os.Stat(p); err != nil {
					t.Errorf("%s went while its team ran", p)
				}
			}
			if out, _ := exec.Command("tmux", "-S", server, "list-sessions", "-F", "#S").Output(); !strings.Contains(string(out), "cadrei-work-dev") {
				t.Error("the team was stopped")
			}
		})
	}
}

// Forward-compatibility audit S1: every session cadrei starts records the
// cadrei that started it.
func TestSessionsCarryTheirVersion(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "work")
	must(t, "up", "dev/engineer")
	must(t, "--tmux", "--detach")
	for _, s := range []string{"cadrei-work-dev", "cadrei-work"} {
		if got := tmuxIn(socket, "show-options", "-qv", "-t", "="+s+":", "@cadrei_version"); got != version {
			t.Errorf("%s: @cadrei_version %q", s, got)
		}
	}
}

// Forward-compatibility audit S8: no member may be called orchestrator,
// whose address would be another crew's orchestrator's.
func TestNoMemberIsCalledOrchestrator(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	os.WriteFile(home+"/.cadrei/work/members/dev/Orchestrator.md", []byte("# Member\n"), 0o644)
	refused(t, "orchestrator is a name kept for your orchestrator", "up", "dev/Orchestrator")
	refused(t, "orchestrator is a name kept for your orchestrator", "up", "dev")
}

// Forward-compatibility audit S13: a crew's notes of this machine are in
// .claude/local, whatever runtime starts its members.
func TestTheLocalFolderIsPinned(t *testing.T) {
	t.Setenv("CADREI_TEST_RUNTIME", "fake")
	if got := cadreis.LocalDir("/c"); got != "/c/.claude/local" {
		t.Errorf("LocalDir: %s", got)
	}
}

// Review of fix round 8 (D1): a development build never lists the default
// tmux server, where the user's own teams run; a release does. Only the
// list is checked: no tmux runs.
func TestADevBuildNeverListsTheDefaultServer(t *testing.T) {
	t.Cleanup(func() { kindForTests = "" })
	for _, c := range []struct {
		kind, realHome string
		want           bool
	}{{"dev", "", false}, {"dev", "1", true}, {"release", "", true}, {"homebrew", "", true}} {
		kindForTests = c.kind
		t.Setenv("CADREI_DEV_REAL_HOME", c.realHome)
		got := false
		for _, s := range defaultSockets() {
			if strings.HasSuffix(s, "/default") {
				got = true
			}
		}
		if got != c.want {
			t.Errorf("%s (CADREI_DEV_REAL_HOME=%q): the default server listed %v, want %v", c.kind, c.realHome, got, c.want)
		}
	}
}

// Review of fix round 8 (A4): a recorded server that can't be listed stops
// every uninstall: a team may run on it.
func TestARecordedServerThatCantBeListedRefuses(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	other := session.Tmux{Socket: os.Getenv("CADREI_TMUX_SOCKET") + "-other"}
	os.MkdirAll(filepath.Dir(other.Path()), 0o700)
	if out, err := exec.Command("tmux", "-S", other.Path(), "new-session", "-d", "-s", "x", "sleep", "60").CombinedOutput(); err != nil {
		t.Fatalf("another server: %v %s", err, out)
	}
	t.Cleanup(func() {
		os.Chmod(other.Path(), 0o700)
		exec.Command("tmux", "-S", other.Path(), "kill-server").Run()
	})
	if err := cadreis.RecordServer(cadreis.Cadrei{Name: "work", Path: home + "/.cadrei/work"}, other.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(other.Path(), 0); err != nil {
		t.Fatalf("can't change the socket's mode: %v", err)
	}
	if out, _ := exec.Command("tmux", "-S", other.Path(), "has-session").CombinedOutput(); !strings.Contains(string(out), "Permission denied") {
		t.Skipf("the socket is still open to this user: %q", out)
	}
	for _, args := range [][]string{{"uninstall", "--yes"}, {"uninstall", "--archive", "--yes"}, {"uninstall", "--all", "--confirm", deletePhrase}} {
		refused(t, "couldn't see which teams run: tmux says", args...)
	}
	if _, err := os.Stat(home + "/.cadrei/work"); err != nil {
		t.Error("the crew went")
	}
}
