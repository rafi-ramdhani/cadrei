//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	// M2: the archive's commit takes the memory only once the user was
	// told, and never after MEMORY=machine; the warning about untracked
	// files names the memory only when it stays out.
	crew := cadreis.Cadrei{Name: "work", Path: c}
	archived := func(note string) (committed, warned bool) {
		t.Helper()
		os.WriteFile(c+"/.memory/"+note, []byte(note+"\n"), 0o644)
		warned = slices.Contains(checkBackup(crew).untracked, ".memory/"+note)
		if out, err := commitChanges(c); err != nil {
			t.Fatal(string(out))
		}
		files, _ := exec.Command("git", "-C", c, "show", "--name-only", "--format=", "HEAD").Output()
		return strings.Contains(string(files), ".memory/"+note), warned
	}
	cadreis.SetMemoryNotice(crew, "")
	if committed, warned := archived("before.md"); committed || !warned {
		t.Errorf("before the notice: committed %v, warned %v", committed, warned)
	}
	cadreis.SetMemoryNotice(crew, "2026-10-14T12:00:00Z")
	if committed, warned := archived("new.md"); !committed || warned {
		t.Errorf("after the notice: committed %v, warned %v", committed, warned)
	}
	f, _ := os.OpenFile(c+"/cadrei.conf", os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("MEMORY=machine\n")
	f.Close()
	if committed, warned := archived("after.md"); committed || !warned {
		t.Errorf("after MEMORY=machine: committed %v, warned %v", committed, warned)
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

// A crew from before 0.2.2, with no .gitattributes: an open with no
// memory anywhere leaves nothing new in it, and the first memory commit
// carries the union line.
func TestAnOlderCrewGetsTheUnionLineWithItsMemory(t *testing.T) {
	home, c, d := memoryCrew(t)
	exec.Command("git", "-C", c, "rm", "-q", ".gitattributes").Run()
	exec.Command("git", "-C", c, "commit", "-qm", "as 0.2.1 made it").Run()
	os.RemoveAll(d)
	must(t)
	if st, _ := exec.Command("git", "-C", c, "status", "--porcelain").Output(); len(st) != 0 {
		t.Errorf("an open with no memory left %q", st)
	}
	os.MkdirAll(c+"/.memory", 0o755)
	os.WriteFile(c+"/.memory/role.md", []byte("role\n"), 0o644)
	must(t)
	must(t)
	files, _ := exec.Command("git", "-C", c, "show", "--name-only", "--format=", "HEAD").Output()
	if string(files) != ".gitattributes\n.memory/role.md\n" {
		t.Errorf("the first memory commit holds %q", files)
	}
	_ = home
}

// Review of crew memory, L2: with ~/.cadrei a link, notes Claude Code
// saved under the crew's path as written move in too.
func TestTheMemoryMovesInThroughALink(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	must(t, "init", "work")
	os.Rename(home+"/.cadrei", home+"/elsewhere")
	os.Symlink(home+"/elsewhere", home+"/.cadrei")
	rt, _ := runtime.Get(runtimeName())
	folders := rt.Memory().Folders(home + "/.cadrei/work")
	if len(folders) != 2 {
		t.Fatalf("folders: %v", folders)
	}
	d := folders[1] // named after the path as written
	os.MkdirAll(d, 0o700)
	os.WriteFile(d+"/role.md", []byte("role\n"), 0o644)
	os.WriteFile(home+"/release", nil, 0o644)
	must(t)
	if readFile(t, home+"/elsewhere/work/.memory/role.md") != "role\n" {
		t.Error("the note didn't move in")
	}
}

// H1: a member of a team with no project, which works inside the crew,
// starts with a memory folder of its own, not the crew's folder in Claude
// Code's that moves into the orchestrator's memory.
func TestAMemberInTheCrewKeepsItsOwnMemory(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	must(t, "up", "dev/engineer")
	ran := readFile(t, home+"/orch-ran")
	_, after, _ := strings.Cut(ran, "--settings\n")
	file, _, _ := strings.Cut(after, "\n")
	var copy map[string]any
	json.Unmarshal([]byte(readFile(t, file)), &copy)
	rt, _ := runtime.Get(runtimeName())
	dir, _ := copy["autoMemoryDirectory"].(string)
	if dir == "" || slices.Contains(rt.Memory().Folders(c), dir) || strings.HasPrefix(dir, c+"/") {
		t.Errorf("the member's memory folder: %q (settings %s)", dir, file)
	}
}

// M1: one machine keeps its memory on itself and pushes the removal; the
// other machine's clone pulls it, and its next open brings the notes back
// from the history into its own folder for the crew.
func TestTheOtherMachineKeepsItsMemory(t *testing.T) {
	home, c, _ := memoryCrew(t)
	must(t)
	must(t) // committed
	// The crew's pre-push hook names the test binary, so the pushes here
	// skip it: they only set the test up.
	bare := filepath.Join(t.TempDir(), "backup.git")
	for _, args := range [][]string{{"init", "-q", "--bare", bare}, {"-C", c, "remote", "add", "origin", bare}, {"-C", c, "push", "-q", "--no-verify", "-u", "origin", "HEAD"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	other := home + "/.cadrei/life"
	if out, err := exec.Command("git", "clone", "-q", bare, other).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	// This machine opts out, and pushes.
	f, _ := os.OpenFile(c+"/cadrei.conf", os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("MEMORY=machine\n")
	f.Close()
	exec.Command("git", "-C", c, "commit", "-qam", "Set MEMORY=machine").Run()
	must(t)
	if out, err := exec.Command("git", "-C", c, "push", "-q", "--no-verify").CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	// The other clone pulls, and opens.
	if out, err := exec.Command("git", "-C", other, "pull", "-q").CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	if _, err := os.Stat(other + "/.memory"); err == nil {
		t.Fatal("the pull kept .memory")
	}
	t.Chdir(other)
	out := must(t)
	if !strings.Contains(flat(out), "+ Brought your orchestrator's memory back to this machine from your crew's history (1 note).") {
		t.Errorf("no word:\n%s", out)
	}
	rt, _ := runtime.Get(runtimeName())
	d := rt.Memory().Folders(other)[0]
	if readFile(t, d+"/user_role.md") != "The user is a developer.\n" {
		t.Error("the note didn't come back")
	}
	// Once back, never again: not even after the user cleared the notes
	// (L11), here or on the machine that opted out.
	if out := must(t); strings.Contains(out, "Brought") {
		t.Errorf("again:\n%s", out)
	}
	os.RemoveAll(d)
	if out := must(t); strings.Contains(out, "Brought") {
		t.Errorf("cleared notes came back:\n%s", out)
	}
	if _, err := os.Stat(d + "/user_role.md"); err == nil {
		t.Error("a cleared note came back")
	}
	t.Chdir(c)
	mine := rt.Memory().Folders(c)[0]
	os.RemoveAll(mine)
	if out := must(t); strings.Contains(out, "Brought") {
		t.Errorf("the machine that opted out got its notes back:\n%s", out)
	}
}

// cadrei pull: no backup, a member, and a note both machines changed,
// kept whole on each side and named.
func TestCadreiPull(t *testing.T) {
	home, c, _ := memoryCrew(t)
	must(t)
	must(t) // the memory is committed
	if code, _, errOut := call("pull"); code != 1 || !strings.Contains(flat(errOut), "work has no backup to pull from.") {
		t.Errorf("no backup: %d %q", code, errOut)
	}
	// The crew's pre-push hook names the test binary, so the pushes here
	// skip it: they only set the test up.
	bare := filepath.Join(t.TempDir(), "backup.git")
	other := filepath.Join(t.TempDir(), "other")
	for _, args := range [][]string{{"init", "-q", "--bare", bare}, {"-C", c, "remote", "add", "origin", bare}, {"-C", c, "push", "-q", "--no-verify", "-u", "origin", "HEAD"},
		{"clone", "-q", bare, other}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	os.WriteFile(other+"/.memory/user_role.md", []byte("The user is a designer.\n"), 0o644)
	for _, args := range [][]string{{"-C", other, "commit", "-qam", "other"}, {"-C", other, "push", "-q", "--no-verify"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	os.WriteFile(c+"/.memory/user_role.md", []byte("The user is a manager.\n"), 0o644)
	exec.Command("git", "-C", c, "commit", "-qam", "here").Run()
	out := must(t, "pull")
	if !strings.Contains(flat(out), "Both machines changed one of your orchestrator's notes, so both are kept: the other machine's under its name, and this one's as user_role.from-") ||
		!strings.Contains(flat(out), "+ Pulled work's backup") {
		t.Errorf("the pull:\n%s", out)
	}
	if readFile(t, c+"/.memory/user_role.md") != "The user is a designer.\n" || memory.Count(c) != 2 {
		t.Error("a side was lost")
	}
	t.Setenv("CADREI_MEMBER", "work-dev-engineer")
	if code, _, errOut := call("pull"); code != 1 || !strings.Contains(errOut, "team members can't pull the crew's backup.") {
		t.Errorf("a member: %d %q", code, errOut)
	}
	_ = home
}
