package memory

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, en := range list {
		out = append(out, en.Name())
	}
	return out
}

func gone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("%s is still there", path)
	}
}

func TestThePointer(t *testing.T) {
	p := Pointer("/Users/me/.cadrei/main")
	if p != "- This crew's orchestrator memory is in /Users/me/.cadrei/main/.memory (cadrei keeps it there). Read /Users/me/.cadrei/main/.memory/MEMORY.md for the index.\n" {
		t.Errorf("pointer %q", p)
	}
	if !IsPointer([]byte(p)) {
		t.Error("cadrei's own pointer")
	}
	for _, not := range []string{
		strings.TrimSuffix(p, "\n"),
		p + "- [Role](role.md): the user\n",
		strings.Replace(p, "Read /Users/me/.cadrei/main", "Read /Users/me/.cadrei/other", 1),
		"- [Role](role.md): the user\n",
		"",
	} {
		if IsPointer([]byte(not)) {
			t.Errorf("counted as a pointer: %q", not)
		}
	}
}

// The table in section 5.2: each row.
func TestMoveIn(t *testing.T) {
	t.Run("nothing in D", func(t *testing.T) {
		crew, d := t.TempDir(), filepath.Join(t.TempDir(), "memory")
		res, err := MoveIn(crew, []string{d}, "m")
		if err != nil || res.Moved != 0 {
			t.Fatalf("%+v %v", res, err)
		}
		gone(t, Dir(crew))
		gone(t, filepath.Join(d, Index)) // no crew memory, no pointer
	})
	t.Run("only the pointer in D", func(t *testing.T) {
		crew, d := t.TempDir(), filepath.Join(t.TempDir(), "memory")
		write(t, filepath.Join(Dir(crew), "role.md"), "role\n")
		write(t, filepath.Join(d, Index), Pointer(crew))
		res, err := MoveIn(crew, []string{d}, "m")
		if err != nil || res.Moved != 0 {
			t.Fatalf("%+v %v", res, err)
		}
		if got := names(t, Dir(crew)); !slices.Equal(got, []string{"role.md"}) {
			t.Errorf("crew %v", got)
		}
		if read(t, filepath.Join(d, Index)) != Pointer(crew) {
			t.Error("the pointer changed")
		}
	})
	t.Run("notes in D, none in the crew", func(t *testing.T) {
		crew, d := t.TempDir(), filepath.Join(t.TempDir(), "memory")
		write(t, filepath.Join(d, Index), "- [Role](role.md): the user\n")
		write(t, filepath.Join(d, "role.md"), "role\n")
		write(t, filepath.Join(d, "status.md"), "status\n")
		res, err := MoveIn(crew, []string{d}, "m")
		if err != nil || res.Moved != 2 || len(res.Copies) != 0 {
			t.Fatalf("%+v %v", res, err)
		}
		if got := names(t, Dir(crew)); !slices.Equal(got, []string{Index, "role.md", "status.md"}) {
			t.Errorf("crew %v", got)
		}
		if read(t, filepath.Join(Dir(crew), "role.md")) != "role\n" || read(t, filepath.Join(Dir(crew), Index)) != "- [Role](role.md): the user\n" {
			t.Error("the notes changed on the way")
		}
		// D keeps only the pointer.
		if got := names(t, d); !slices.Equal(got, []string{Index}) || read(t, filepath.Join(d, Index)) != Pointer(crew) {
			t.Errorf("D %v", got)
		}
		if Count(crew) != 2 {
			t.Errorf("count %d", Count(crew))
		}
	})
	t.Run("notes in both", func(t *testing.T) {
		crew, d := t.TempDir(), filepath.Join(t.TempDir(), "memory")
		write(t, filepath.Join(Dir(crew), Index), "- [Role](role.md): the user\n- [Status](status.md): status\n")
		write(t, filepath.Join(Dir(crew), "role.md"), "role\n")
		write(t, filepath.Join(Dir(crew), "status.md"), "status on the crew\n")
		write(t, filepath.Join(d, Index), "- [Role](role.md): the user\n- [Status](status.md): status\n- [Tone](tone.md): tone\n")
		write(t, filepath.Join(d, "role.md"), "role\n")             // the same: skipped
		write(t, filepath.Join(d, "status.md"), "status on here\n") // other text: a copy
		write(t, filepath.Join(d, "tone.md"), "tone\n")             // missing: copied in
		res, err := MoveIn(crew, []string{d}, "laptop")
		if err != nil || res.Moved != 2 || !slices.Equal(res.Copies, []string{"status.from-laptop.md"}) {
			t.Fatalf("%+v %v", res, err)
		}
		if got := names(t, Dir(crew)); !slices.Equal(got, []string{Index, "role.md", "status.from-laptop.md", "status.md", "tone.md"}) {
			t.Errorf("crew %v", got)
		}
		if read(t, filepath.Join(Dir(crew), "status.md")) != "status on the crew\n" || read(t, filepath.Join(Dir(crew), "status.from-laptop.md")) != "status on here\n" {
			t.Error("a clash lost a side")
		}
		want := "- [Role](role.md): the user\n- [Status](status.md): status\n- [Status](status.from-laptop.md): status\n- [Tone](tone.md): tone\n"
		if got := read(t, filepath.Join(Dir(crew), Index)); got != want {
			t.Errorf("index:\n%s", got)
		}
		if got := names(t, d); !slices.Equal(got, []string{Index}) {
			t.Errorf("D %v", got)
		}
		// Again: nothing more to do, nothing changes.
		write(t, filepath.Join(d, "status.md"), "status on here\n")
		os.Remove(filepath.Join(d, Index))
		if res, err := MoveIn(crew, []string{d}, "laptop"); err != nil || res.Moved != 0 {
			t.Errorf("again: %+v %v", res, err)
		}
		if got := read(t, filepath.Join(Dir(crew), Index)); got != want {
			t.Errorf("index again:\n%s", got)
		}
	})
	t.Run("a clash with no index line", func(t *testing.T) {
		crew, d := t.TempDir(), filepath.Join(t.TempDir(), "memory")
		write(t, filepath.Join(Dir(crew), "a.md"), "one\n")
		write(t, filepath.Join(Dir(crew), "a.from-m.md"), "two\n")
		write(t, filepath.Join(d, "a.md"), "three\n")
		res, err := MoveIn(crew, []string{d}, "m")
		if err != nil || !slices.Equal(res.Copies, []string{"a.from-m-2.md"}) {
			t.Fatalf("%+v %v", res, err)
		}
		if got := read(t, filepath.Join(Dir(crew), Index)); got != "- [a, as m had it](a.from-m-2.md)\n" {
			t.Errorf("index %q", got)
		}
	})
	t.Run("both of D's spellings", func(t *testing.T) {
		crew, d1, d2 := t.TempDir(), filepath.Join(t.TempDir(), "memory"), filepath.Join(t.TempDir(), "memory")
		write(t, filepath.Join(d1, "a.md"), "a\n")
		write(t, filepath.Join(d2, "b.md"), "b\n")
		if res, err := MoveIn(crew, []string{d1, d2}, "m"); err != nil || res.Moved != 2 {
			t.Fatalf("%+v %v", res, err)
		}
		if got := names(t, d1); !slices.Equal(got, []string{Index}) {
			t.Errorf("D1 %v", got)
		}
		gone(t, d2)
	})
}

// A file it can't move stops the move, and D stays as it was.
func TestMoveInStopsOnWhatItCantMove(t *testing.T) {
	crew, d := t.TempDir(), filepath.Join(t.TempDir(), "memory")
	write(t, filepath.Join(d, "a.md"), "a\n")
	os.MkdirAll(filepath.Join(d, "sub"), 0o755)
	if _, err := MoveIn(crew, []string{d}, "m"); err == nil || !strings.Contains(err.Error(), "isn't a file") {
		t.Fatalf("a folder in D: %v", err)
	}
	if got := names(t, d); !slices.Equal(got, []string{"a.md", "sub"}) {
		t.Errorf("D %v", got)
	}
	os.Remove(filepath.Join(d, "sub"))
	write(t, filepath.Join(d, "b.md"), "b\n")
	os.Chmod(filepath.Join(d, "b.md"), 0)
	defer os.Chmod(filepath.Join(d, "b.md"), 0o644)
	if os.Getuid() != 0 {
		if _, err := MoveIn(crew, []string{d}, "m"); err == nil {
			t.Fatal("an unreadable note")
		}
		if got := names(t, d); !slices.Equal(got, []string{"a.md", "b.md"}) {
			t.Errorf("D after an unreadable note %v", got)
		}
	}
}

func TestMoveOut(t *testing.T) {
	crew, d := t.TempDir(), filepath.Join(t.TempDir(), "memory")
	write(t, filepath.Join(Dir(crew), Index), "- [Role](role.md): the user\n")
	write(t, filepath.Join(Dir(crew), "role.md"), "role\n")
	write(t, filepath.Join(d, Index), Pointer(crew))
	res, err := MoveOut(crew, d, "m")
	if err != nil || res.Moved != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	gone(t, Dir(crew))
	if got := names(t, d); !slices.Equal(got, []string{Index, "role.md"}) {
		t.Errorf("D %v", got)
	}
	if read(t, filepath.Join(d, Index)) != "- [Role](role.md): the user\n" {
		t.Error("the pointer stayed, or the index was lost")
	}
	// Back in: the same notes again.
	if res, err := MoveIn(crew, []string{d}, "m"); err != nil || res.Moved != 1 || Count(crew) != 1 {
		t.Errorf("back in: %+v %v", res, err)
	}
}

// The pointer never goes over notes, and only where the crew has a
// memory folder.
func TestPoint(t *testing.T) {
	crew, d := t.TempDir(), filepath.Join(t.TempDir(), "memory")
	if err := Point(crew, d); err != nil {
		t.Fatal(err)
	}
	gone(t, d)
	os.MkdirAll(Dir(crew), 0o755)
	write(t, filepath.Join(d, Index), "- [Role](role.md): the user's own\n")
	if err := Point(crew, d); err != nil || read(t, filepath.Join(d, Index)) != "- [Role](role.md): the user's own\n" {
		t.Errorf("wrote over notes: %v", err)
	}
	os.Remove(filepath.Join(d, Index))
	if err := Point(crew, d); err != nil || read(t, filepath.Join(d, Index)) != Pointer(crew) {
		t.Errorf("no pointer: %v", err)
	}
}

// A crew with notes and no index: the pointer in D doesn't stay as if it
// were D's index.
func TestMoveOutWithNoIndex(t *testing.T) {
	crew, d := t.TempDir(), filepath.Join(t.TempDir(), "memory")
	write(t, filepath.Join(Dir(crew), "role.md"), "role\n")
	write(t, filepath.Join(d, Index), Pointer(crew))
	if _, err := MoveOut(crew, d, "m"); err != nil {
		t.Fatal(err)
	}
	if got := names(t, d); !slices.Equal(got, []string{"role.md"}) {
		t.Errorf("D %v", got)
	}
}

func TestRemovePointer(t *testing.T) {
	crew, d := t.TempDir(), filepath.Join(t.TempDir(), "memory")
	write(t, filepath.Join(d, Index), Pointer(crew)+"- an edit\n")
	if RemovePointer(d) {
		t.Error("removed an index that isn't exactly cadrei's")
	}
	write(t, filepath.Join(d, Index), Pointer(crew))
	if !RemovePointer(d) {
		t.Error("kept cadrei's pointer")
	}
	gone(t, d)
}

func TestMachine(t *testing.T) {
	m := Machine()
	if m == "" || strings.ContainsAny(m, " ./") || strings.ToLower(m) != m {
		t.Errorf("machine %q", m)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "user.name", "T")
	gitIn(t, dir, "config", "user.email", "t@example.invalid")
	write(t, filepath.Join(dir, "playbook.md"), "playbook\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-qm", "init")
	return dir
}

func TestAttributes(t *testing.T) {
	crew := t.TempDir()
	write(t, filepath.Join(crew, ".gitattributes"), "*.png binary")
	if added, err := Attributes(crew); !added || err != nil {
		t.Fatal(added, err)
	}
	if added, _ := Attributes(crew); added {
		t.Error("added twice")
	}
	if got := read(t, filepath.Join(crew, ".gitattributes")); got != "*.png binary\n# The orchestrator's memory index: two machines' lines are both kept.\n/.memory/MEMORY.md merge=union\n" {
		t.Errorf("%q", got)
	}
}

func TestCommit(t *testing.T) {
	crew := repo(t)
	if Commit(crew, "Update the orchestrator's memory") {
		t.Error("committed nothing")
	}
	write(t, filepath.Join(Dir(crew), "a.md"), "a\n")
	Attributes(crew)
	// Another change, staged and not: it stays out of the commit.
	write(t, filepath.Join(crew, "playbook.md"), "changed\n")
	write(t, filepath.Join(crew, "notes.md"), "staged\n")
	gitIn(t, crew, "add", "notes.md")
	if !Commit(crew, "Update the orchestrator's memory") {
		t.Fatal("no commit")
	}
	if got := gitIn(t, crew, "show", "--name-only", "--format=%s", "HEAD"); got != "Update the orchestrator's memory\n\n.gitattributes\n.memory/a.md" {
		t.Errorf("the commit:\n%s", got)
	}
	if got := gitIn(t, crew, "status", "--porcelain"); got != "M playbook.md\nA  notes.md" && got != "A  notes.md\n M playbook.md" {
		t.Errorf("the other changes: %q", got)
	}
	// Busy: an index lock, a rebase.
	write(t, filepath.Join(Dir(crew), "b.md"), "b\n")
	for _, f := range []string{"index.lock", "MERGE_HEAD"} {
		write(t, filepath.Join(crew, ".git", f), "")
		if Busy(crew) != f || Commit(crew, "x") {
			t.Errorf("committed with %s", f)
		}
		os.Remove(filepath.Join(crew, ".git", f))
	}
	os.MkdirAll(filepath.Join(crew, ".git", "rebase-merge"), 0o755)
	if Commit(crew, "x") {
		t.Error("committed during a rebase")
	}
	os.Remove(filepath.Join(crew, ".git", "rebase-merge"))
	// A removed folder is committed as removed.
	os.RemoveAll(Dir(crew))
	write(t, filepath.Join(crew, ".gitattributes"), "changed\n")
	if !CommitRemoval(crew, "Keep the orchestrator's memory on this machine") || tracked(crew, Folder) {
		t.Error("the removal wasn't committed")
	}
	if got := gitIn(t, crew, "show", "--name-only", "--format=%s", "HEAD"); got != "Keep the orchestrator's memory on this machine\n\n.memory/a.md" {
		t.Errorf("the removal commit:\n%s", got)
	}
	if CommitRemoval(crew, "again") {
		t.Error("a second removal commit")
	}
	// While the folder is there, it's no removal: nothing is committed.
	write(t, filepath.Join(Dir(crew), "c.md"), "c\n")
	Commit(crew, "Update the orchestrator's memory")
	write(t, filepath.Join(Dir(crew), "d.md"), "d\n")
	if CommitRemoval(crew, "Keep the orchestrator's memory on this machine") {
		t.Error("committed a note as a removal")
	}
	// No identity: nothing.
	plain := t.TempDir()
	gitIn(t, plain, "init", "-q")
	write(t, filepath.Join(Dir(plain), "a.md"), "a\n")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	if Commit(plain, "x") {
		t.Error("committed without an identity")
	}
	if Busy(t.TempDir()) == "" {
		t.Error("no repository isn't busy")
	}
}

func TestUnpushed(t *testing.T) {
	crew := repo(t)
	if Unpushed(crew) != -1 || HasRemote(crew) {
		t.Error("no remote")
	}
	bare := t.TempDir()
	gitIn(t, bare, "init", "-q", "--bare")
	gitIn(t, crew, "remote", "add", "origin", bare)
	if Unpushed(crew) != -1 || !HasRemote(crew) {
		t.Error("no upstream")
	}
	gitIn(t, crew, "push", "-q", "-u", "origin", "main")
	write(t, filepath.Join(Dir(crew), "a.md"), "a\n")
	Commit(crew, "one")
	write(t, filepath.Join(Dir(crew), "b.md"), "b\n")
	Commit(crew, "two")
	if n := Unpushed(crew); n != 2 {
		t.Errorf("unpushed %d", n)
	}
}

// note is a note in Claude Code's own format.
func note(name, description, modified, body string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\nmetadata:\n  type: user\n  modified: " + modified + "\n---\n\n" + body + "\n"
}

// oneFrontMatter fails unless text is one note: one front matter, at
// the top, with no key twice.
func oneFrontMatter(t *testing.T, label, text string) {
	t.Helper()
	if strings.Count(text, "\n---\n")+boolInt(strings.HasPrefix(text, "---\n")) != 2 || strings.Contains(text, "<<<<<<<") {
		t.Errorf("%s isn't one note:\n%s", label, text)
		return
	}
	head, _, _ := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
	seen := map[string]bool{}
	for _, l := range strings.Split(head, "\n") {
		key, _, _ := strings.Cut(l, ":")
		if seen[key] {
			t.Errorf("%s has %q twice:\n%s", label, key, text)
		}
		seen[key] = true
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Two machines upgrade a crew from before 0.2.2, each from notes of its
// own, with one note both named the same, then change one note on both
// and delete another on one while the other changes it. Pull never makes
// a merged note: a note both changed is kept whole on each side, the
// other machine's under its name and this one's beside it, and a changed
// note beats a deletion. A clash outside the memory aborts the pull.
func TestTwoMachines(t *testing.T) {
	bare := t.TempDir()
	gitIn(t, bare, "init", "-q", "--bare", "-b", "main")
	a := repo(t)
	gitIn(t, a, "remote", "add", "origin", bare)
	gitIn(t, a, "push", "-q", "-u", "origin", "main")
	b := filepath.Join(t.TempDir(), "b")
	gitIn(t, filepath.Dir(b), "clone", "-q", bare, b)
	gitIn(t, b, "config", "user.name", "T")
	gitIn(t, b, "config", "user.email", "t@example.invalid")
	upgrade := func(crew, machine, text, own string) {
		d := filepath.Join(t.TempDir(), "memory")
		write(t, filepath.Join(d, Index), "- [Role](user_role.md): "+text+"\n- ["+own+"]("+own+".md): "+own+"\n")
		write(t, filepath.Join(d, "user_role.md"), note("user_role", text, "2026-10-14T10:00:00Z", text+"."))
		write(t, filepath.Join(d, own+".md"), note(own, own, "2026-10-14T10:00:00Z", own+"."))
		if _, err := MoveIn(crew, []string{d}, machine); err != nil {
			t.Fatal(err)
		}
		Attributes(crew)
	}
	upgrade(a, "a", "the user designs", "tone")
	upgrade(b, "b", "the user codes", "status")
	if !Commit(a, "Update the orchestrator's memory") {
		t.Fatal("a didn't commit")
	}
	gitIn(t, a, "push", "-q")
	// B's notice session: its notes are untracked, and a pull would stop
	// on them, which is why the orchestrator skips it.
	if _, err := Pull(b, "b", filepath.Join(b, ".aside")); err == nil {
		t.Error("a pull over untracked notes went through; the notice session's skip isn't needed")
	}
	if Busy(b) != "" {
		t.Error("a stopped pull left git busy")
	}
	if !Commit(b, "Update the orchestrator's memory") {
		t.Fatal("b didn't commit")
	}
	pulled, err := Pull(b, "b", filepath.Join(b, ".aside"))
	if err != nil || !slices.Equal(pulled.Kept, []string{"user_role.from-b.md"}) {
		t.Fatalf("b's pull: %+v %v", pulled, err)
	}
	gitIn(t, b, "push", "-q")
	if _, err := Pull(a, "a", filepath.Join(a, ".aside")); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{a, b} {
		if got := names(t, Dir(dir)); !slices.Equal(got, []string{Index, "status.md", "tone.md", "user_role.from-b.md", "user_role.md"}) {
			t.Errorf("%s: %v", dir, got)
		}
		if got := read(t, filepath.Join(Dir(dir), "user_role.md")); !strings.Contains(got, "the user designs") || strings.Contains(got, "the user codes") {
			t.Errorf("%s: the other machine's note: %q", dir, got)
		}
		if got := read(t, filepath.Join(Dir(dir), "user_role.from-b.md")); !strings.Contains(got, "the user codes") || strings.Contains(got, "designs") {
			t.Errorf("%s: b's own note: %q", dir, got)
		}
		for _, f := range []string{"user_role.md", "user_role.from-b.md", "tone.md", "status.md"} {
			oneFrontMatter(t, dir+"/"+f, read(t, filepath.Join(Dir(dir), f)))
		}
		idx := read(t, filepath.Join(Dir(dir), Index))
		for _, l := range []string{"(user_role.md)", "(user_role.from-b.md)", "(tone.md)", "(status.md)"} {
			if !strings.Contains(idx, l) || strings.Contains(idx, "<<<<<<<") {
				t.Errorf("%s index lacks %s:\n%s", dir, l, idx)
			}
		}
	}
	// One note changed on both, with Claude Code's own fields; another
	// deleted on b while a changes it.
	write(t, filepath.Join(Dir(a), "tone.md"), note("tone", "short answers", "2026-10-14T11:00:00Z", "Short answers."))
	write(t, filepath.Join(Dir(a), "status.md"), note("status", "status, updated", "2026-10-14T11:00:00Z", "Updated."))
	Commit(a, "Update the orchestrator's memory")
	gitIn(t, a, "push", "-q")
	write(t, filepath.Join(Dir(b), "tone.md"), note("tone", "long answers", "2026-10-14T11:05:00Z", "Long answers."))
	os.Remove(filepath.Join(Dir(b), "status.md"))
	Commit(b, "Update the orchestrator's memory")
	pulled, err = Pull(b, "b", filepath.Join(b, ".aside"))
	if err != nil || !slices.Equal(pulled.Kept, []string{"tone.from-b.md"}) || !slices.Equal(pulled.Restored, []string{"status.md"}) {
		t.Fatalf("b's second pull: %+v %v", pulled, err)
	}
	oneFrontMatter(t, "tone.md", read(t, filepath.Join(Dir(b), "tone.md")))
	oneFrontMatter(t, "tone.from-b.md", read(t, filepath.Join(Dir(b), "tone.from-b.md")))
	if !strings.Contains(read(t, filepath.Join(Dir(b), "tone.from-b.md")), "Long answers.") || !strings.Contains(read(t, filepath.Join(Dir(b), "status.md")), "Updated.") {
		t.Error("a side was lost")
	}
	// A clash outside the memory aborts the pull, and nothing changes.
	gitIn(t, b, "push", "-q")
	Pull(a, "a", filepath.Join(a, ".aside"))
	write(t, filepath.Join(a, "playbook.md"), "from a\n")
	gitIn(t, a, "commit", "-qam", "a")
	gitIn(t, a, "push", "-q")
	write(t, filepath.Join(b, "playbook.md"), "from b\n")
	gitIn(t, b, "commit", "-qam", "b")
	before := gitIn(t, b, "rev-parse", "HEAD")
	pulled, err = Pull(b, "b", filepath.Join(b, ".aside"))
	if err != nil || !slices.Equal(pulled.Conflicts, []string{"playbook.md"}) {
		t.Errorf("a clash outside the memory: %+v %v", pulled, err)
	}
	if Busy(b) != "" || gitIn(t, b, "rev-parse", "HEAD") != before || read(t, filepath.Join(b, "playbook.md")) != "from b\n" {
		t.Error("the aborted pull changed something")
	}
}

// The line a build before the review wrote, which merged notes too, is
// replaced, in the file and in the memory's first commit.
func TestTheOldUnionLineGoes(t *testing.T) {
	crew := repo(t)
	write(t, filepath.Join(crew, ".gitattributes"), "*.png binary\n# The orchestrator's memory: two machines' lines are both kept.\n/.memory/** merge=union\n")
	gitIn(t, crew, "add", ".gitattributes")
	gitIn(t, crew, "commit", "-qm", "the old line")
	if added, err := Attributes(crew); !added || err != nil {
		t.Fatal(added, err)
	}
	want := "*.png binary\n" + attributesText
	if got := read(t, filepath.Join(crew, ".gitattributes")); got != want {
		t.Errorf("the file: %q", got)
	}
	write(t, filepath.Join(Dir(crew), "a.md"), "a\n")
	Commit(crew, "Update the orchestrator's memory")
	if got := gitIn(t, crew, "show", "HEAD:.gitattributes"); got != strings.TrimSpace(want) {
		t.Errorf("committed: %q", got)
	}
}

// A machine that keeps its memory on itself pushes the folder's removal;
// the other machine's clone brings the notes back from the history into
// its own folder for the crew.
func TestFromHistory(t *testing.T) {
	crew := repo(t)
	write(t, filepath.Join(Dir(crew), Index), "- [Role](role.md): the user\n")
	write(t, filepath.Join(Dir(crew), "role.md"), "role\n")
	Commit(crew, "Update the orchestrator's memory")
	os.RemoveAll(Dir(crew))
	CommitRemoval(crew, "Keep the orchestrator's memory on this machine")
	d := filepath.Join(t.TempDir(), "memory")
	write(t, filepath.Join(d, Index), Pointer(crew))
	res, removal, err := FromHistory(crew, d, "", nil)
	if err != nil || res.Moved != 1 || removal == "" {
		t.Fatalf("%+v %q %v", res, removal, err)
	}
	if got := names(t, d); !slices.Equal(got, []string{Index, "role.md"}) || read(t, filepath.Join(d, Index)) != "- [Role](role.md): the user\n" {
		t.Errorf("D %v", got)
	}
	if res, _, err := FromHistory(repo(t), t.TempDir(), "", nil); err != nil || res.Moved != 0 {
		t.Errorf("no removal: %+v %v", res, err)
	}
	// L11: the same removal again, after the user cleared the notes:
	// done, so they stay cleared.
	os.RemoveAll(d)
	if res, again, err := FromHistory(crew, d, removal, nil); err != nil || res.Moved != 0 || again != "" {
		t.Errorf("brought back twice: %+v %q %v", res, again, err)
	}
	if _, err := os.Stat(d); err == nil {
		t.Error("the cleared folder came back")
	}
	// Re-review 3, M7: notes the user cleared while the crew kept them
	// never come back from that emptying.
	crew = repo(t)
	write(t, filepath.Join(Dir(crew), "role.md"), "role\n")
	Commit(crew, "Update the orchestrator's memory")
	os.RemoveAll(Dir(crew))
	Commit(crew, "Update the orchestrator's memory")
	if res, removal, err := FromHistory(crew, d, "", nil); err != nil || res.Moved != 0 || removal != "" {
		t.Errorf("an emptying came back: %+v %q %v", res, removal, err)
	}
}

// Re-review 5: a note copied from the crew, or from its history, that
// clashes with one here is named after the crew, not after this machine.
func TestACopyFromTheCrewIsNamedAfterIt(t *testing.T) {
	crew := repo(t)
	write(t, filepath.Join(Dir(crew), Index), "- [Role](role.md): the user\n")
	write(t, filepath.Join(Dir(crew), "role.md"), "the crew's role\n")
	Commit(crew, "Update the orchestrator's memory")
	check := func(how string, d string, res Result) {
		t.Helper()
		if !slices.Equal(res.Copies, []string{"role.from-crew.md"}) || read(t, filepath.Join(d, "role.from-crew.md")) != "the crew's role\n" || read(t, filepath.Join(d, "role.md")) != "this machine's role\n" {
			t.Errorf("%s: %+v %v", how, res, names(t, d))
		}
		if idx := read(t, filepath.Join(d, Index)); !strings.Contains(idx, "(role.from-crew.md)") {
			t.Errorf("%s: index:\n%s", how, idx)
		}
	}
	d := filepath.Join(t.TempDir(), "memory")
	write(t, filepath.Join(d, "role.md"), "this machine's role\n")
	res, err := Copy(crew, d)
	if err != nil {
		t.Fatal(err)
	}
	check("the copy", d, res)
	os.RemoveAll(Dir(crew))
	CommitRemoval(crew, OptOut)
	d = filepath.Join(t.TempDir(), "memory")
	write(t, filepath.Join(d, "role.md"), "this machine's role\n")
	res, _, err = FromHistory(crew, d, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	check("the history", d, res)
}

// The memory commit takes only the union line from .gitattributes: the
// user's own edit there stays theirs, uncommitted.
func TestCommitTakesOnlyTheUnionLine(t *testing.T) {
	crew := repo(t)
	write(t, filepath.Join(crew, ".gitattributes"), "*.png binary\n")
	gitIn(t, crew, "add", ".gitattributes")
	gitIn(t, crew, "commit", "-qm", "attributes")
	write(t, filepath.Join(crew, ".gitattributes"), "*.png binary\n*.pdf binary\n")
	Attributes(crew)
	write(t, filepath.Join(Dir(crew), "a.md"), "a\n")
	if !Commit(crew, "Update the orchestrator's memory") {
		t.Fatal("no commit")
	}
	if got := gitIn(t, crew, "show", "HEAD:.gitattributes"); got != strings.TrimSpace("*.png binary\n"+attributesText) {
		t.Errorf("committed attributes:\n%s", got)
	}
	if got := read(t, filepath.Join(crew, ".gitattributes")); !strings.Contains(got, "*.pdf binary") || !strings.Contains(got, UnionLine) {
		t.Errorf("the user's file: %q", got)
	}
	if got := gitIn(t, crew, "status", "--porcelain"); got != "M .gitattributes" {
		t.Errorf("status: %q", got)
	}
	// An edit the user staged in .gitattributes stays staged.
	write(t, filepath.Join(crew, ".gitattributes"), read(t, filepath.Join(crew, ".gitattributes"))+"*.zip binary\n")
	gitIn(t, crew, "add", ".gitattributes")
	write(t, filepath.Join(Dir(crew), "staged.md"), "s\n")
	Commit(crew, "Update the orchestrator's memory")
	if got := gitIn(t, crew, "diff", "--cached", "--name-only"); got != ".gitattributes" {
		t.Errorf("the user's staged edit: %q", got)
	}
	gitIn(t, crew, "reset", "-q", "--", ".gitattributes")
	// With the line committed, a later memory commit leaves the file alone.
	write(t, filepath.Join(Dir(crew), "b.md"), "b\n")
	Commit(crew, "Update the orchestrator's memory")
	if got := gitIn(t, crew, "show", "--name-only", "--format=", "HEAD"); got != ".memory/b.md" {
		t.Errorf("the next commit: %q", got)
	}
}

// L1: a note Claude Code saved under cadrei's pointer moves in, and the
// pointer's own line never reaches the crew's index.
func TestThePointerLineStaysOut(t *testing.T) {
	crew, d := t.TempDir(), filepath.Join(t.TempDir(), "memory")
	write(t, filepath.Join(Dir(crew), Index), "- [Role](role.md): the user\n")
	write(t, filepath.Join(Dir(crew), "role.md"), "role\n")
	write(t, filepath.Join(d, Index), Pointer(crew)+"- [Tone](tone.md): tone\n")
	write(t, filepath.Join(d, "tone.md"), "tone\n")
	if res, err := MoveIn(crew, []string{d}, "m"); err != nil || res.Moved != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := read(t, filepath.Join(Dir(crew), Index)); got != "- [Role](role.md): the user\n- [Tone](tone.md): tone\n" {
		t.Errorf("index:\n%s", got)
	}
	if read(t, filepath.Join(d, Index)) != Pointer(crew) {
		t.Error("D's pointer")
	}
	// Only the pointer, with a blank line: no notes.
	write(t, filepath.Join(d, Index), Pointer(crew)+"\n")
	if holds, _ := Holds(d); holds {
		t.Error("a pointer and a blank line count as notes")
	}
}

// L3: a note that changed after merge read it stays in D, for the next
// move, and isn't lost.
func TestANoteSavedDuringTheMoveStays(t *testing.T) {
	crew, d := t.TempDir(), filepath.Join(t.TempDir(), "memory")
	write(t, filepath.Join(d, "a.md"), "a\n")
	write(t, filepath.Join(d, "b.md"), "b\n")
	_, taken, err := merge(d, Dir(crew), "m")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(d, "b.md"), "b, rewritten\n")
	write(t, filepath.Join(d, "c.md"), "c\n")
	if err := clear(d, taken); err != nil {
		t.Fatal(err)
	}
	if got := names(t, d); !slices.Equal(got, []string{"b.md", "c.md"}) {
		t.Errorf("D %v", got)
	}
}

// L4: with two folders, a stop on the second leaves the first as it was.
func TestAStopLeavesEveryFolder(t *testing.T) {
	crew, d1, d2 := t.TempDir(), filepath.Join(t.TempDir(), "memory"), filepath.Join(t.TempDir(), "memory")
	write(t, filepath.Join(d1, "a.md"), "a\n")
	write(t, filepath.Join(d2, "b.md"), "b\n")
	os.MkdirAll(filepath.Join(d2, "sub"), 0o755)
	if _, err := MoveIn(crew, []string{d1, d2}, "m"); err == nil {
		t.Fatal("no stop")
	}
	if got := names(t, d1); !slices.Equal(got, []string{"a.md"}) {
		t.Errorf("the first folder: %v", got)
	}
}

// twoClones is a crew with a backup and a second machine's clone, both
// with a committed, pushed memory of two notes.
func twoClones(t *testing.T) (a, b string) {
	t.Helper()
	bare := t.TempDir()
	gitIn(t, bare, "init", "-q", "--bare", "-b", "main")
	a = repo(t)
	Attributes(a)
	write(t, filepath.Join(Dir(a), Index), "- [Role](role.md): the user\n- [Tone](tone.md): tone\n")
	write(t, filepath.Join(Dir(a), "role.md"), note("role", "the user", "2026-10-14T10:00:00Z", "The user codes."))
	write(t, filepath.Join(Dir(a), "tone.md"), note("tone", "tone", "2026-10-14T10:00:00Z", "Short answers."))
	Commit(a, "Update the orchestrator's memory")
	gitIn(t, a, "remote", "add", "origin", bare)
	gitIn(t, a, "push", "-q", "-u", "origin", "main")
	b = filepath.Join(t.TempDir(), "b")
	gitIn(t, filepath.Dir(b), "clone", "-q", bare, b)
	gitIn(t, b, "config", "user.name", "T")
	gitIn(t, b, "config", "user.email", "t@example.invalid")
	return a, b
}

// Re-review 2, M5: an uncommitted change to a note the other machine
// changed, or deleted, is settled like a committed one: no conflict marks,
// nothing marked unmerged, no stash left. One to another file is named,
// stays marked, and blocks the memory's commit.
func TestPullWithUncommittedChanges(t *testing.T) {
	a, b := twoClones(t)
	write(t, filepath.Join(Dir(a), "role.md"), note("role", "the user", "2026-10-14T11:00:00Z", "The user designs."))
	os.Remove(filepath.Join(Dir(a), "tone.md"))
	Commit(a, "Update the orchestrator's memory")
	gitIn(t, a, "push", "-q")
	write(t, filepath.Join(Dir(b), "role.md"), note("role", "the user", "2026-10-14T11:05:00Z", "The user manages."))
	write(t, filepath.Join(Dir(b), "tone.md"), note("tone", "tone", "2026-10-14T11:05:00Z", "Long answers."))
	pulled, err := Pull(b, "b", t.TempDir())
	if err != nil || !slices.Equal(pulled.Kept, []string{"role.from-b.md"}) || !slices.Equal(pulled.Restored, []string{"tone.md"}) || len(pulled.Stashed) != 0 {
		t.Fatalf("%+v %v", pulled, err)
	}
	for _, f := range []string{"role.md", "role.from-b.md", "tone.md"} {
		oneFrontMatter(t, f, read(t, filepath.Join(Dir(b), f)))
	}
	if !strings.Contains(read(t, filepath.Join(Dir(b), "role.md")), "designs") || !strings.Contains(read(t, filepath.Join(Dir(b), "role.from-b.md")), "manages") ||
		!strings.Contains(read(t, filepath.Join(Dir(b), "tone.md")), "Long answers.") {
		t.Error("a side was lost")
	}
	if Busy(b) != "" || gitIn(t, b, "stash", "list") != "" {
		t.Errorf("left busy (%q) or a stash: %q", Busy(b), gitIn(t, b, "stash", "list"))
	}
	if !Commit(b, "Update the orchestrator's memory") {
		t.Error("the settled notes weren't committed")
	}
	// Another file: named, marked, and the memory waits.
	gitIn(t, b, "push", "-q")
	Pull(a, "a", t.TempDir())
	write(t, filepath.Join(a, "playbook.md"), "from a\n")
	gitIn(t, a, "commit", "-qam", "a")
	gitIn(t, a, "push", "-q")
	write(t, filepath.Join(b, "playbook.md"), "from b\n")
	pulled, err = Pull(b, "b", t.TempDir())
	if err != nil || !slices.Equal(pulled.Stashed, []string{"playbook.md"}) {
		t.Fatalf("another file: %+v %v", pulled, err)
	}
	write(t, filepath.Join(Dir(b), "new.md"), "new\n")
	if Busy(b) != "unmerged files" || Commit(b, "x") {
		t.Errorf("a commit with marked files: busy %q", Busy(b))
	}
	if gitIn(t, b, "stash", "list") == "" {
		t.Error("the stash with b's change went")
	}
}

// Re-review 2, M6: the other machine keeps its memory on itself now and
// pushed the folder's removal, while this one has a committed, unpushed
// note change and an uncommitted new note. Every note of this machine's is
// set aside, no unpushed commit carries a note any more, the crew stops
// tracking the folder, and this machine's other work keeps its commit.
func TestPullAfterAnOptOut(t *testing.T) {
	a, b := twoClones(t)
	optOut(t, a)
	gitIn(t, a, "push", "-q")
	write(t, filepath.Join(Dir(b), "role.md"), note("role", "the user", "2026-10-14T11:05:00Z", "The user manages."))
	Commit(b, "Update the orchestrator's memory")
	write(t, filepath.Join(b, "playbook.md"), "b's playbook\n")
	gitIn(t, b, "commit", "-qam", "b's playbook")
	write(t, filepath.Join(Dir(b), "fresh.md"), "fresh\n")
	aside := filepath.Join(t.TempDir(), "aside")
	pulled, err := Pull(b, "b", aside)
	if err != nil || pulled.SetAside != 3 {
		t.Fatalf("%+v %v", pulled, err)
	}
	if tracked(b, Folder) {
		t.Error("the crew tracks the memory again")
	}
	if got := gitIn(t, b, "log", "--format=%s", "@{upstream}..HEAD", "--", Folder); got != "" {
		t.Errorf("unpushed commits carry notes: %q", got)
	}
	if got := gitIn(t, b, "log", "-1", "--format=%s %an %ae"); got != "b's playbook T t@example.invalid" || read(t, filepath.Join(b, "playbook.md")) != "b's playbook\n" {
		t.Errorf("b's other work: %q", got)
	}
	if _, err := os.Stat(Dir(b)); err == nil {
		t.Error("the folder stayed")
	}
	if got := names(t, aside); !slices.Equal(got, []string{Index, "fresh.md", "role.md", "tone.md"}) || !strings.Contains(read(t, filepath.Join(aside, "role.md")), "manages") {
		t.Errorf("aside: %v", got)
	}
	// The next open moves them into its own folder.
	d := filepath.Join(t.TempDir(), "memory")
	if res, err := Move(aside, d, "b"); err != nil || res.Moved != 3 {
		t.Errorf("%+v %v", res, err)
	}
	gone(t, aside)
}

// Re-review 5: after a pull set this machine's notes aside, the next
// open brings back the opt-out's history without an older copy of a note
// this machine changed since, and still with what the other machine
// changed before it opted out.
func TestTheHistoryAfterNotesSetAside(t *testing.T) {
	a, b := twoClones(t)
	write(t, filepath.Join(Dir(b), "tone.md"), note("tone", "tone", "2026-10-14T11:05:00Z", "Long answers."))
	write(t, filepath.Join(Dir(b), Index), "- [Role](role.md): the user\n- [Tone](tone.md): long answers\n")
	Commit(b, "Update the orchestrator's memory")
	write(t, filepath.Join(Dir(a), "role.md"), note("role", "the user", "2026-10-14T11:00:00Z", "The user designs."))
	// Its index changed too: its line for tone.md is older than b's.
	write(t, filepath.Join(Dir(a), "new.md"), note("new", "new", "2026-10-14T11:00:00Z", "New."))
	write(t, filepath.Join(Dir(a), Index), "- [Role](role.md): the user\n- [Tone](tone.md): tone\n- [New](new.md): new\n")
	Commit(a, "Update the orchestrator's memory")
	optOut(t, a)
	gitIn(t, a, "push", "-q")
	aside := filepath.Join(t.TempDir(), "aside")
	if pulled, err := Pull(b, "b", aside); err != nil || pulled.SetAside != 2 {
		t.Fatalf("%+v %v", pulled, err)
	}
	// What the next open does: the notes set aside, then the history.
	d := filepath.Join(t.TempDir(), "memory")
	brought, err := Move(aside, d, "b")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := FromHistory(b, d, "", brought.Notes); err != nil {
		t.Fatal(err)
	}
	if got := names(t, d); !slices.Equal(got, []string{Index, "new.md", "role.from-crew.md", "role.md", "tone.md"}) {
		t.Errorf("D %v", got)
	}
	if !strings.Contains(read(t, filepath.Join(d, "tone.md")), "Long answers.") || !strings.Contains(read(t, filepath.Join(d, "role.md")), "The user codes.") || !strings.Contains(read(t, filepath.Join(d, "role.from-crew.md")), "The user designs.") {
		t.Error("a note isn't the one it should be")
	}
	if idx := read(t, filepath.Join(d, Index)); strings.Count(idx, "(tone.md)") != 1 || !strings.Contains(idx, "long answers") || strings.Count(idx, "(role.md)") != 1 || !strings.Contains(idx, "(role.from-crew.md)") || !strings.Contains(idx, "(new.md)") {
		t.Errorf("index:\n%s", idx)
	}
}

// optOut does what keepOnMachine does after the orchestrator set
// MEMORY=machine: the setting committed, the memory folder removed.
func optOut(t *testing.T, crew string) {
	t.Helper()
	write(t, filepath.Join(crew, "cadrei.conf"), "MEMORY=machine\n")
	gitIn(t, crew, "add", "cadrei.conf")
	gitIn(t, crew, "commit", "-qm", "Set MEMORY=machine")
	os.RemoveAll(Dir(crew))
	CommitRemoval(crew, OptOut)
}

// Re-review 4, H3: after an opt-out and an opt-in (MEMORY=crew again),
// the old opt-out commit doesn't make each pull strip the memory.
func TestPullAfterAnOptIn(t *testing.T) {
	a, b := twoClones(t)
	optOut(t, a)
	gitIn(t, a, "push", "-q")
	write(t, filepath.Join(a, "cadrei.conf"), "MEMORY=crew\n")
	gitIn(t, a, "commit", "-qam", "Set MEMORY=crew")
	gitIn(t, a, "push", "-q")
	// a, back in, commits its memory, and pulls from a crew that has none
	// yet: the commit stays, and goes up.
	write(t, filepath.Join(Dir(a), "tone.md"), "a's tone\n")
	Commit(a, "Update the orchestrator's memory")
	aside := filepath.Join(t.TempDir(), "aside")
	if pulled, err := Pull(a, "a", aside); err != nil || pulled.SetAside != 0 || !tracked(a, Folder) {
		t.Fatalf("a: %+v %v", pulled, err)
	}
	if got := gitIn(t, a, "log", "--format=%s", "@{upstream}..HEAD"); got != "Update the orchestrator's memory" {
		t.Errorf("a's unpushed: %q", got)
	}
	gitIn(t, a, "push", "-q")
	// b never saw the opt-out, and changed a note.
	write(t, filepath.Join(Dir(b), "role.md"), "b's role\n")
	Commit(b, "Update the orchestrator's memory")
	if pulled, err := Pull(b, "b", aside); err != nil || pulled.SetAside != 0 || !tracked(b, Folder) {
		t.Fatalf("b: %+v %v", pulled, err)
	}
	gone(t, aside)
	if read(t, filepath.Join(Dir(b), "tone.md")) != "a's tone\n" || read(t, filepath.Join(Dir(b), "role.md")) != "b's role\n" {
		t.Error("b's notes")
	}
	gitIn(t, b, "push", "-q")
	if _, err := Pull(a, "a", aside); err != nil {
		t.Fatal(err)
	}
	// Out again: a machine that brought the first opt-out's notes back
	// gets the second one's, once; the first one's never again.
	first := LastOptOut(a)
	optOut(t, a)
	d := filepath.Join(t.TempDir(), "memory")
	res, removal, err := FromHistory(a, d, first, nil)
	if err != nil || removal == "" || removal == first || res.Moved != 2 || read(t, filepath.Join(d, "tone.md")) != "a's tone\n" {
		t.Errorf("the second opt-out: %+v %q %v", res, removal, err)
	}
	if res, again, _ := FromHistory(a, t.TempDir(), removal, nil); res.Moved != 0 || again != "" {
		t.Errorf("brought back twice: %+v", res)
	}
}

// Re-review 3, M7: a crew that only emptied its memory isn't an opt-out.
// The emptying reaches the other machine as one: nothing set aside, no
// commit rewritten. An opt-out made by hand while cadrei.conf says
// MEMORY=machine still counts.
func TestPullAfterAnEmptying(t *testing.T) {
	a, b := twoClones(t)
	os.RemoveAll(Dir(a))
	Commit(a, "Update the orchestrator's memory")
	gitIn(t, a, "push", "-q")
	write(t, filepath.Join(b, "playbook.md"), "b's playbook\n")
	gitIn(t, b, "add", "playbook.md")
	gitIn(t, b, "commit", "-qm", "b's playbook")
	aside := filepath.Join(t.TempDir(), "aside")
	pulled, err := Pull(b, "b", aside)
	if err != nil || pulled.SetAside != 0 {
		t.Fatalf("%+v %v", pulled, err)
	}
	gone(t, aside)
	gone(t, Dir(b))
	if got := gitIn(t, b, "reflog", "--format=%gs"); strings.Contains(got, "set the memory aside") {
		t.Errorf("b's commits were rewritten:\n%s", got)
	}
	if LastOptOut(b) != "" {
		t.Error("the emptying counts as an opt-out")
	}

	a, b = twoClones(t)
	os.RemoveAll(Dir(a))
	write(t, filepath.Join(a, "cadrei.conf"), "MEMORY=machine\n")
	gitIn(t, a, "add", "-A")
	gitIn(t, a, "commit", "-qm", "My own words")
	gitIn(t, a, "push", "-q")
	if pulled, err := Pull(b, "b", aside); err != nil || pulled.SetAside != 2 {
		t.Fatalf("by hand: %+v %v", pulled, err)
	}
	if LastOptOut(b) == "" {
		t.Error("by hand: no opt-out")
	}

	// Emptied, then MEMORY=machine: this machine's notes stay out too.
	a, b = twoClones(t)
	os.RemoveAll(Dir(a))
	Commit(a, "Update the orchestrator's memory")
	write(t, filepath.Join(a, "cadrei.conf"), "MEMORY=machine\n")
	gitIn(t, a, "add", "cadrei.conf")
	gitIn(t, a, "commit", "-qm", "Set MEMORY=machine")
	gitIn(t, a, "push", "-q")
	write(t, filepath.Join(Dir(b), "role.md"), "b's role\n")
	Commit(b, "Update the orchestrator's memory")
	if pulled, err := Pull(b, "b", t.TempDir()); err != nil || pulled.SetAside != 2 {
		t.Fatalf("emptied, then machine: %+v %v", pulled, err)
	}
	if got := gitIn(t, b, "log", "--format=%s", "@{upstream}..HEAD"); got != "" {
		t.Errorf("unpushed: %q", got)
	}
}

// Re-review 6, L16 and L17: the files that came in changed are git's
// names only, never its warnings, with a moved file's old name too, and a
// name outside ASCII as it is.
func TestPullNamesWhatChanged(t *testing.T) {
	a, b := twoClones(t)
	for _, f := range []string{"x1.md", "x2.md", "z.md"} {
		write(t, filepath.Join(a, f), f+" at first\n")
	}
	gitIn(t, a, "add", "-A")
	gitIn(t, a, "commit", "-qm", "files")
	gitIn(t, a, "push", "-q")
	if _, err := Pull(b, "b", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// Past diff.renameLimit, git warns on stderr and exits 0.
	gitIn(t, b, "config", "diff.renameLimit", "1")
	gitIn(t, a, "mv", "x1.md", "y1.md")
	gitIn(t, a, "mv", "x2.md", "y2.md")
	gitIn(t, a, "mv", "z.md", "w.md")
	write(t, filepath.Join(a, "y1.md"), "y1 now\n")
	write(t, filepath.Join(a, "y2.md"), "y2 now\n")
	write(t, filepath.Join(a, "café.md"), "coffee\n")
	write(t, filepath.Join(Dir(a), "role.md"), note("role", "the user", "2026-10-15T10:00:00Z", "The user designs."))
	gitIn(t, a, "add", "-A")
	gitIn(t, a, "commit", "-qm", "moves")
	gitIn(t, a, "push", "-q")
	pulled, err := Pull(b, "b", t.TempDir())
	if want := []string{"café.md", "w.md", "x1.md", "x2.md", "y1.md", "y2.md", "z.md"}; err != nil || !slices.Equal(pulled.Changed, want) {
		t.Errorf("changed: %q %v", pulled.Changed, err)
	}
	// Any other warning stays out too: with a tag named HEAD, git says
	// the name is ambiguous, and goes on.
	a, b = twoClones(t)
	gitIn(t, b, "update-ref", "refs/tags/HEAD", "HEAD")
	write(t, filepath.Join(a, "playbook.md"), "the new playbook\n")
	gitIn(t, a, "commit", "-qam", "playbook")
	gitIn(t, a, "push", "-q")
	if pulled, err := Pull(b, "b", t.TempDir()); !slices.Equal(pulled.Changed, []string{"playbook.md"}) {
		t.Errorf("a warning: %q %v", pulled.Changed, err)
	}
}

// Re-review 2, L14: a pull stopped at its limit says where it left the
// crew.
func TestAPullThatTakesTooLong(t *testing.T) {
	_, b := twoClones(t)
	was := pullLimit
	pullLimit = time.Nanosecond
	defer func() { pullLimit = was }()
	if _, err := Pull(b, "b", t.TempDir()); err == nil || !strings.Contains(err.Error(), "was stopped, before anything came in") {
		t.Errorf("%v", err)
	}
	if Busy(b) != "" {
		t.Error("left busy")
	}
}
