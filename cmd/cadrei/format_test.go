//go:build !windows

package main

import (
	"os"
	"strings"
	"testing"
)

// Forward-compatibility audit M2: a crew a newer cadrei changed (FORMAT
// above 1 in its tracked cadrei.conf) is never changed by this one, nor
// are its members started; ls, attach, stop, --check and uninstall still
// work. FORMAT is never written for format 1, and the machine's own
// .claude/local/cadrei.conf can't set it.
func TestANewerCrewFormatIsOnlyRead(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	if strings.Contains(readFile(t, c+"/cadrei.conf"), "FORMAT") {
		t.Fatal("a new crew has FORMAT")
	}
	t.Setenv("CADREI_HOME", c)
	os.MkdirAll(home+"/src/app", 0o755)

	// The machine's own conf doesn't count.
	os.MkdirAll(c+"/.claude/local", 0o700)
	os.WriteFile(c+"/.claude/local/cadrei.conf", []byte("FORMAT=2\n"), 0o600)
	must(t, "up", "dev/engineer")
	must(t, "stop", "--yes")

	conf := readFile(t, c+"/cadrei.conf")
	for _, format := range []string{"2", "two"} {
		os.WriteFile(c+"/cadrei.conf", []byte(conf+"FORMAT="+format+"\n"), 0o644)
		want := "the crew work was changed by a newer cadrei (format " + format + ")"
		// Opening the orchestrator too (review of fix round 8): its
		// instructions change the crew in this version's layout.
		for _, args := range [][]string{{}, {"--tmux", "--detach"}, {"up", "dev"}, {"allow", "add", "Bash(ls)"}, {"allow", "remove", "1"}, {"project", "add", "app", "--path", home + "/src/app"},
			{"project", "link", "app", home + "/src/app"}, {"project", "unlink", "app"}, {"project", "sync"}, {"project", "trust", "--all"}, {"tmux", "on"}} {
			refused(t, want, args...)
		}
	}
	// What only reads, stops or uninstalls still works.
	if out := must(t, "ls", "--json"); !strings.Contains(out, "was changed by a newer cadrei (format two)") {
		t.Errorf("ls --json's problems don't say so:\n%s", out)
	}
	must(t, "ls")
	must(t, "stop", "--yes")
	if _, out, errOut := call("attach", "dev"); strings.Contains(out+errOut, "newer cadrei") {
		t.Errorf("attach refused: %s", errOut)
	}
	if _, out, errOut := call("--check"); strings.Contains(out+errOut, "isn't a key cadrei uses") || strings.Contains(out+errOut, "FORMAT") && !strings.Contains(out+errOut, "newer cadrei") {
		t.Errorf("--check:\n%s%s", out, errOut)
	}
	must(t, "uninstall", "--dry-run")

	// 0.2.0's notes aren't moved into it.
	os.MkdirAll(home+"/.cadrei/config/places", 0o700)
	os.WriteFile(home+"/.cadrei/config/places/work.json", []byte(`{"app":"~/src/app"}`), 0o600)
	call("ls")
	if exists(c + "/.claude/local/places.json") {
		t.Error("0.2.0's notes were moved into a newer crew")
	}
	// FORMAT=1 is this cadrei's own.
	os.WriteFile(c+"/cadrei.conf", []byte(conf+"FORMAT=1\n"), 0o644)
	must(t, "up", "dev/engineer")
}
