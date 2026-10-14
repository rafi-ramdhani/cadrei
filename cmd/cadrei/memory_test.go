//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/memory"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// memoryCrew is a crew "work" whose orchestrator saved notes in Claude
// Code's own folder for it, as 0.2.1 left them. It returns the crew and
// that folder.
func memoryCrew(t *testing.T) (home, c, d string) {
	t.Helper()
	home = sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	must(t, "init", "work")
	c = home + "/.cadrei/work"
	rt, _ := runtime.Get(runtimeName())
	d = rt.Memory().Folders(c)[0]
	os.MkdirAll(d, 0o700)
	os.WriteFile(d+"/MEMORY.md", []byte("- [Role](user_role.md): the user is a developer\n"), 0o644)
	os.WriteFile(d+"/user_role.md", []byte("The user is a developer.\n"), 0o644)
	os.WriteFile(home+"/release", nil, 0o644)
	return home, c, d
}

// The memory moves in at the first open, with one notice, and nothing of
// it is committed in that session; the next open commits it.
func TestTheMemoryMovesIntoTheCrew(t *testing.T) {
	home, c, d := memoryCrew(t)
	out := must(t)
	if !strings.Contains(flat(out), "+ Your orchestrator's memory now lives in your crew (.memory/), so your backup keeps it") {
		t.Errorf("no notice:\n%s", out)
	}
	if got := readFile(t, c+"/.memory/user_role.md"); got != "The user is a developer.\n" {
		t.Errorf("the note: %q", got)
	}
	if got := readFile(t, d+"/MEMORY.md"); got != memory.Pointer(c) {
		t.Errorf("D: %q", got)
	}
	if _, err := os.Stat(d + "/user_role.md"); err == nil {
		t.Error("the note stayed in D")
	}
	// The orchestrator's settings name the folder, and it learns the
	// notice is due.
	var settings map[string]any
	json.Unmarshal([]byte(readFile(t, c+"/.claude/build/orchestrator-settings.json")), &settings)
	if settings["autoMemoryDirectory"] != c+"/.memory" || settings["hooks"] == nil {
		t.Errorf("settings: %v", settings)
	}
	if ran := readFile(t, home+"/orch-ran"); !strings.Contains(ran, "\nCADREI_MEMORY_NOTICE=1\n") {
		t.Errorf("no notice for the orchestrator:\n%s", ran)
	}
	if strings.Contains(gitLog(t, c), "memory") {
		t.Errorf("committed before the notice:\n%s", gitLog(t, c))
	}
	// The session start hook of that session commits nothing either.
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	t.Setenv("CADREI_HOME", c)
	t.Setenv(envMemoryNotice, "1")
	t.Chdir(c)
	if out := must(t, "hook", "snapshot"); !strings.Contains(out, "- Memory: kept in the crew (.memory/, 1 note), notice not given yet\\n") {
		t.Errorf("the snapshot: %s", out)
	}
	if strings.Contains(gitLog(t, c), "memory") {
		t.Errorf("the notice session's hook committed:\n%s", gitLog(t, c))
	}
	os.Unsetenv(envMemoryNotice)
	os.Unsetenv("CADREI_ORCHESTRATOR")
	os.Unsetenv("CADREI_HOME")
	t.Chdir(home)
	// The next open: no notice, one commit with only the memory.
	os.Remove(home + "/orch-ran")
	out = must(t)
	if strings.Contains(out, "memory") {
		t.Errorf("a second notice:\n%s", out)
	}
	if ran := readFile(t, home+"/orch-ran"); strings.Contains(ran, "CADREI_MEMORY_NOTICE") {
		t.Error("the notice again")
	}
	if log := gitLog(t, c); !strings.HasPrefix(log, "Update the orchestrator's memory\n") {
		t.Errorf("log:\n%s", log)
	}
	files, _ := exec.Command("git", "-C", c, "show", "--name-only", "--format=", "HEAD").Output()
	if string(files) != ".memory/MEMORY.md\n.memory/user_role.md\n" {
		t.Errorf("the commit holds %q", files)
	}
	// A note saved since, then a session start: committed by the hook.
	os.WriteFile(c+"/.memory/tone.md", []byte("Short answers.\n"), 0o644)
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	t.Setenv("CADREI_HOME", c)
	t.Chdir(c)
	out = must(t, "hook", "snapshot")
	if !strings.Contains(out, "- Memory: kept in the crew (.memory/, 2 notes)\\n") || !strings.Contains(out, "- Backup: none set up\\n") {
		t.Errorf("the snapshot: %s", out)
	}
	if log := gitLog(t, c); strings.Count(log, "Update the orchestrator's memory") != 2 {
		t.Errorf("the hook didn't commit:\n%s", log)
	}
}

// A crew with notes of its own and a machine with other notes: both are
// kept, and the clash is named.
func TestTheMemoryMergesWithTheCrews(t *testing.T) {
	_, c, d := memoryCrew(t)
	os.MkdirAll(c+"/.memory", 0o755)
	os.WriteFile(c+"/.memory/MEMORY.md", []byte("- [Role](user_role.md): the user is a designer\n"), 0o644)
	os.WriteFile(c+"/.memory/user_role.md", []byte("The user is a designer.\n"), 0o644)
	out := must(t)
	if !strings.Contains(flat(out), "1 of your orchestrator's notes differed from the crew's, so both are kept: user_role.from-") {
		t.Errorf("no word on the clash:\n%s", out)
	}
	if readFile(t, c+"/.memory/user_role.md") != "The user is a designer.\n" || memory.Count(c) != 2 {
		t.Error("a side was lost")
	}
	if holds, _ := memory.Holds(d); holds {
		t.Error("D still holds notes")
	}
}

// A note it can't move stops the move: D stays, and the orchestrator
// opens with its own folder.
func TestTheMemoryStaysWhenItCantMove(t *testing.T) {
	_, c, d := memoryCrew(t)
	os.MkdirAll(d+"/sub", 0o755)
	code, out, errOut := call()
	if code != 0 || !strings.Contains(out+errOut, "Your orchestrator's memory couldn't move into your crew:") {
		t.Errorf("%d\n%s%s", code, out, errOut)
	}
	if _, err := os.Stat(d + "/user_role.md"); err != nil {
		t.Error("D lost a note")
	}
	var settings map[string]any
	json.Unmarshal([]byte(readFile(t, c+"/.claude/build/orchestrator-settings.json")), &settings)
	if _, ok := settings["autoMemoryDirectory"]; ok {
		t.Errorf("settings name the crew's folder: %v", settings)
	}
}

// MEMORY=machine moves the notes back once, commits their removal, and
// leaves the setting out; MEMORY=crew brings them back in.
func TestKeepingTheMemoryOnThisMachine(t *testing.T) {
	_, c, d := memoryCrew(t)
	must(t)
	must(t) // committed now
	f, _ := os.OpenFile(c+"/cadrei.conf", os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("MEMORY=machine\n")
	f.Close()
	out := must(t)
	if !strings.Contains(flat(out), "+ Your orchestrator's memory is back on this machine only. Earlier backups still have it in their history.") {
		t.Errorf("no word:\n%s", out)
	}
	if _, err := os.Stat(c + "/.memory"); err == nil {
		t.Error(".memory stayed")
	}
	if readFile(t, d+"/user_role.md") != "The user is a developer.\n" || readFile(t, d+"/MEMORY.md") != "- [Role](user_role.md): the user is a developer\n" {
		t.Error("the notes didn't come back")
	}
	if log := gitLog(t, c); !strings.HasPrefix(log, "Keep the orchestrator's memory on this machine\n") {
		t.Errorf("log:\n%s", log)
	}
	var settings map[string]any
	json.Unmarshal([]byte(readFile(t, c+"/.claude/build/orchestrator-settings.json")), &settings)
	if _, ok := settings["autoMemoryDirectory"]; ok {
		t.Errorf("settings: %v", settings)
	}
	if cadreis.MemoryNotice(cadreis.Cadrei{Name: "work", Path: c}) != "" {
		t.Error("the notice is still recorded")
	}
	// Again: nothing to do, nothing said.
	if out := must(t); strings.Contains(out, "memory") {
		t.Errorf("again:\n%s", out)
	}
	// Back in the crew, with the notice again.
	raw := strings.Replace(readFile(t, c+"/cadrei.conf"), "MEMORY=machine", "MEMORY=crew", 1)
	os.WriteFile(c+"/cadrei.conf", []byte(raw), 0o644)
	if out := must(t); !strings.Contains(flat(out), "now lives in your crew") || memory.Count(c) != 1 {
		t.Errorf("back in:\n%s", out)
	}
}

// The machine's own cadrei.conf wins, for this machine only.
func TestMemorySettingForThisMachine(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	c := cadreis.Cadrei{Name: "work", Path: home + "/.cadrei/work"}
	if cadreis.MemoryIn(c) != cadreis.MemoryInCrew {
		t.Error("the default")
	}
	f, _ := os.OpenFile(c.Path+"/cadrei.conf", os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("MEMORY=crew\n")
	f.Close()
	if cadreis.MemoryIn(c) != cadreis.MemoryInCrew {
		t.Error("MEMORY=crew")
	}
	os.MkdirAll(cadreis.LocalDir(c.Path), 0o700)
	os.WriteFile(filepath.Join(cadreis.LocalDir(c.Path), "cadrei.conf"), []byte("MEMORY=machine\n"), 0o644)
	if cadreis.MemoryIn(c) != cadreis.MemoryOnMachine {
		t.Error("the local setting")
	}
	if strings.Contains(strings.Join(unknownConf(c.Path), ","), "MEMORY") {
		t.Error("MEMORY counts as unknown")
	}
}

// Members' settings deny writing into each crew's memory.
func TestMembersCantEditTheMemory(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	must(t, "up", "dev/engineer")
	var copy string
	entries, _ := os.ReadDir(c + "/.claude/build")
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), "member-settings") && strings.HasSuffix(en.Name(), ".json") {
			copy = readFile(t, c+"/.claude/build/"+en.Name())
		}
	}
	for _, rule := range []string{`"Edit(//**/.cadrei/*/.memory/**)"`, `"Edit(/` + c + `/.memory/**)"`} {
		if !strings.Contains(copy, rule) {
			t.Errorf("the settings copy lacks %s", rule)
		}
	}
	if strings.Contains(readFile(t, c+"/.claude/member-settings.json"), ".memory") {
		t.Error("the tracked grants file changed")
	}
}

// The plans and the restore line name the memory, --all removes only
// cadrei's own pointer, and the archive's commit takes new notes.
func TestTheMemoryInPlansAndCommits(t *testing.T) {
	_, c, d := memoryCrew(t)
	must(t)
	if out := must(t, "uninstall", "--dry-run"); !strings.Contains(out, "Your orchestrator's memory stays in each crew (.memory/).") {
		t.Errorf("uninstall plan:\n%s", out)
	}
	if !strings.Contains(holds(cadreis.Cadrei{Name: "work", Path: c}), ", and your orchestrator's memory (.memory/, 1 note)") {
		t.Errorf("--all plan: %s", holds(cadreis.Cadrei{Name: "work", Path: c}))
	}
	os.WriteFile(c+"/.memory/new.md", []byte("new\n"), 0o644)
	if out, err := commitChanges(c); err != nil {
		t.Fatal(string(out))
	}
	if files, _ := exec.Command("git", "-C", c, "show", "--name-only", "--format=", "HEAD").Output(); !strings.Contains(string(files), ".memory/new.md") {
		t.Errorf("the archive's commit: %q", files)
	}
	if !memory.RemovePointer(d) {
		t.Error("cadrei's pointer stayed")
	}
}

// In tmux, the notice reaches the orchestrator through its own command:
// the tmux server never hands it on.
func TestTheMemoryNoticeReachesATmuxOrchestrator(t *testing.T) {
	home, c, _ := memoryCrew(t)
	stubAttach(t, home)
	os.WriteFile(c+"/cadrei.conf", []byte("ORCHESTRATOR=tmux\n"), 0o644)
	// The orchestrator stays up in its window until the test ends.
	os.Remove(home + "/release")
	t.Cleanup(func() { os.WriteFile(home+"/release", nil, 0o644) })
	if code, out, errOut := call(); code != 0 {
		t.Fatalf("%d\n%s%s", code, out, errOut)
	}
	if ran := readFile(t, home+"/orch-ran"); !strings.Contains(ran, "CADREI_OPENS_IN=tmux") || !strings.Contains(ran, "\nCADREI_MEMORY_NOTICE=1\n") {
		t.Errorf("the orchestrator in tmux:\n%s", ran)
	}
	if os.Getenv(envMemoryNotice) != "" {
		t.Error("the notice stayed in cadrei's own environment")
	}
}
