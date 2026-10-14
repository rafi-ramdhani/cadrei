// Package testguard keeps tests away from the user's own setup. Every test
// package's TestMain runs through Main, which gives the whole test binary a
// throwaway HOME, a private tmux server and none of the variables that
// point cadrei or Claude Code elsewhere, so a test that forgets its own
// sandbox still cannot reach ~/.cadrei, ~/.claude, ~/.claude.json or the
// user's tmux sessions. Only tests import it.
package testguard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// realHome is HOME as the test binary started with it.
var realHome string

// cleared are the variables that would point a test at the user's setup.
var cleared = []string{
	"CLAUDE_CONFIG_DIR", "TMUX", "TMUX_PANE", "XDG_CACHE_HOME", "XDG_CONFIG_HOME",
	"CADREI_HOME", "CADREI_MEMBER", "CADRE_PERSONA", "CADREI_OFF", "CADREI_ORCHESTRATOR", "CADREI_TEST_TTY",
	"CADREI_MEMORY_NOTICE",
}

// Environment a guarded test binary passes to the test binaries it runs as
// helpers, so they share its HOME and sockets and leave nothing behind.
const (
	envHome     = "CADREI_TESTGUARD_HOME"
	envRealHome = "CADREI_TESTGUARD_REAL_HOME"
)

// Main sets up the guarded environment, runs the tests and cleans up. A
// test binary run as a helper by a guarded one reuses its environment.
func Main(m *testing.M) {
	// A test may have set its own HOME before starting the helper; Unsafe
	// still checks that HOME is not the real one.
	if os.Getenv(envHome) != "" && os.Getenv("TMUX_TMPDIR") != "" {
		realHome = os.Getenv(envRealHome)
		if err := Unsafe(); err != nil {
			fmt.Fprintln(os.Stderr, "testguard:", err)
			os.Exit(1)
		}
		os.Exit(m.Run())
	}
	realHome = os.Getenv("HOME")
	home, err := os.MkdirTemp("", "cadrei-test-home-")
	if err == nil {
		home, err = filepath.EvalSymlinks(home)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "testguard:", err)
		os.Exit(1)
	}
	os.Setenv("HOME", home)
	os.Setenv(envHome, home)
	os.Setenv(envRealHome, realHome)
	// tmux keeps its sockets here, not in the user's own tmux folder, so
	// none is left there. A short path: a socket's path has a length limit.
	sockets, err := os.MkdirTemp("/tmp", "cadrei-tmux-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testguard:", err)
		os.Exit(1)
	}
	os.Setenv("TMUX_TMPDIR", sockets)
	socket := fmt.Sprintf("cadrei-gotest-%d", os.Getpid())
	os.Setenv("CADREI_TMUX_SOCKET", socket)
	for _, v := range cleared {
		os.Unsetenv(v)
	}
	if err := Unsafe(); err != nil {
		fmt.Fprintln(os.Stderr, "testguard:", err)
		os.Exit(1)
	}
	code := m.Run()
	exec.Command("tmux", "-L", socket, "kill-server").Run()
	os.RemoveAll(home)
	os.RemoveAll(sockets)
	os.Exit(code)
}

// Unsafe says why the environment could reach the user's setup, or nil.
func Unsafe() error {
	home := os.Getenv("HOME")
	switch {
	case home == "" || realHome == "":
		return fmt.Errorf("HOME is not set")
	case same(home, realHome):
		return fmt.Errorf("HOME is the user's real home (%s)", home)
	case os.Getenv("CADREI_TMUX_SOCKET") == "":
		return fmt.Errorf("CADREI_TMUX_SOCKET is not set, so tmux commands would reach the user's server")
	case os.Getenv("TMUX_TMPDIR") == "":
		return fmt.Errorf("TMUX_TMPDIR is not set, so tmux sockets would land in the user's tmux folder")
	case os.Getenv("CLAUDE_CONFIG_DIR") != "" && within(os.Getenv("CLAUDE_CONFIG_DIR"), realHome):
		return fmt.Errorf("CLAUDE_CONFIG_DIR points into the user's real home")
	}
	return nil
}

// Check fails the test when the environment could reach the user's setup.
func Check(t testing.TB) {
	t.Helper()
	if err := Unsafe(); err != nil {
		t.Fatalf("testguard: %s", err)
	}
}

// MustBeSafe panics when the environment could reach the user's setup, for
// helpers that have no *testing.T.
func MustBeSafe() {
	if err := Unsafe(); err != nil {
		panic("testguard: " + err.Error())
	}
}

// real resolves symlinks in the longest part of p that exists, so a path
// that does not exist yet under a symlinked folder (/var on macOS) is
// still compared physically.
func real(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

func same(a, b string) bool { return real(a) == real(b) }

func within(p, dir string) bool {
	rel, err := filepath.Rel(real(dir), real(p))
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && (len(rel) < 3 || rel[:3] != "../")
}
