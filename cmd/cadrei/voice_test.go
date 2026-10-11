//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/rafi-ramdhani/cadrei/internal/paths"
)

// Acceptance 73: no human-readable output holds an em dash, "problem:",
// "note:" or a full path under the home folder. ls --json, the hooks and
// project path print data, and aren't read here.
func TestOneVoiceEverywhere(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/release", nil, 0o644)
	os.WriteFile(home+"/.claude.json", []byte("{}\n"), 0o600)
	os.MkdirAll(home+"/src/app", 0o755)
	exec.Command("git", "-C", home+"/src/app", "init", "-q").Run()
	runs := [][]string{
		{"new", "work"}, {"new", "life"}, {"new", "work"}, {"use", "work"}, {"use", "nope"},
		{"help"}, {"help", "advanced"}, {"--version"}, {"project"}, {"sl"}, {"foo"}, {"down"}, {"project", "ad", "x"},
		{"project", "add"}, {"project", "add", "app", "--path", home + "/src/app"}, {"project", "add", "app", "--path", home + "/src/app"},
		{"project", "dir"}, {"project", "dir", "~/Developer"}, {"project", "sync"}, {"project", "trust", "--all"},
		{"project", "trust", "nope"}, {"project", "link", "app", home + "/src/app"},
		{"allow"}, {"allow", "add", "Bash(npm test)"}, {"allow", "add", "Bash(ls docs/*)"}, {"allow", "add", "Bash(npm test)"},
		{"allow", "remove", "Bash(npm test)"}, {"allow", "remove", "--once"},
		{"up"}, {"up", "dev", "app"}, {"up", "dev/engineer", "app"}, {"ls"}, {"ls", "--all"},
		{"allow", "add", "Bash(make x)"}, {"attach"}, {"attach", "dev", "nope"},
		{"stop", "dev/reviewer", "app"}, {"stop", "dev", "app"}, {"stop", "dev", "app"}, {"stop"}, {"stop", "--yes"},
		{"tmux"}, {"tmux", "on"}, {"tmux", "off"}, {"hook"}, {"--check"}, {"restore"},
		{"uninstall", "--dry-run"}, {"uninstall", "--archive", "--dry-run"}, {"project", "unlink", "app"},
		{"stop", "zz"}, {"up", "zz"}, {"up", "dev", "nothere"}, {"help", "x"}, {"uninstall", "--x"}, {"project", "add", "x", "/no/such/repo"},
		{},
	}
	for _, args := range runs {
		_, out, errOut := call(args...)
		for _, bad := range []string{"\u2014", "problem:", "note:", home + "/", paths.Real(home) + "/", "warning:", "usage:", "registered project", "fatal:", "cannot "} {
			if strings.Contains(out+errOut, bad) {
				t.Errorf("cadrei %v says %q:\n%s%s", args, bad, out, errOut)
			}
		}
	}
	if _, out, _ := call("uninstall", "--yes"); strings.Contains(out, home+"/") || strings.Contains(out, "\u2014") {
		t.Errorf("uninstall:\n%s", out)
	}
}

// Review 11 (M4) and F-R3: a note that starts with a name keeps it as
// written, and an ordinary start still gets its capital.
func TestNotesKeepNamesAsWritten(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	os.MkdirAll(home+"/src/my-app", 0o755)
	exec.Command("git", "-C", home+"/src/my-app", "init", "-q").Run()
	out := must(t, "project", "add", "my-app", "--path", home+"/src/my-app", "--no-trust")
	if !strings.Contains(out, "! my-app has no GitHub repo") {
		t.Errorf("add:\n%s", out)
	}
	for in, want := range map[string]string{"app has no repo": "app has no repo.", "another cadrei command is busy": "Another cadrei command is busy.", "projects.yaml has an entry": "projects.yaml has an entry."} {
		if got := sentence(in); got != want {
			t.Errorf("sentence(%q) = %q", in, got)
		}
	}
}

// 8.3.4: cadrei stop zz in the new voice, with its fix on its own line.
func TestStopAnUnknownTeam(t *testing.T) {
	sandbox(t)
	must(t, "init", "main")
	if _, _, errOut := call("stop", "zz"); errOut != "cadrei: there's no team called zz in your crew main.\n  Ask your orchestrator to add it.\n" {
		t.Errorf("stop zz: %q", errOut)
	}
}
