package memory

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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

// Two machines: each saves a fact, and both are kept, the index by the
// union merge. When both rewrite the same fact, the rebase stops.
func TestTwoMachines(t *testing.T) {
	bare := t.TempDir()
	gitIn(t, bare, "init", "-q", "--bare", "-b", "main")
	a := repo(t)
	Attributes(a)
	write(t, filepath.Join(Dir(a), Index), "- [Role](role.md): the user\n")
	write(t, filepath.Join(Dir(a), "role.md"), "role\n")
	Commit(a, "Update the orchestrator's memory")
	gitIn(t, a, "remote", "add", "origin", bare)
	gitIn(t, a, "push", "-q", "-u", "origin", "main")
	b := filepath.Join(t.TempDir(), "b")
	gitIn(t, filepath.Dir(b), "clone", "-q", bare, b)
	gitIn(t, b, "config", "user.name", "T")
	gitIn(t, b, "config", "user.email", "t@example.invalid")

	write(t, filepath.Join(Dir(a), "tone.md"), "tone\n")
	write(t, filepath.Join(Dir(a), Index), "- [Role](role.md): the user\n- [Tone](tone.md): tone\n")
	Commit(a, "Update the orchestrator's memory")
	gitIn(t, a, "push", "-q")
	write(t, filepath.Join(Dir(b), "status.md"), "status\n")
	write(t, filepath.Join(Dir(b), Index), "- [Role](role.md): the user\n- [Status](status.md): status\n")
	Commit(b, "Update the orchestrator's memory")
	gitIn(t, b, "pull", "-q", "--rebase", "--autostash")
	gitIn(t, b, "push", "-q")
	gitIn(t, a, "pull", "-q", "--rebase", "--autostash")
	for _, dir := range []string{a, b} {
		if got := names(t, Dir(dir)); !slices.Equal(got, []string{Index, "role.md", "status.md", "tone.md"}) {
			t.Errorf("%s: %v", dir, got)
		}
		idx := read(t, filepath.Join(Dir(dir), Index))
		for _, l := range []string{"(role.md)", "(tone.md)", "(status.md)"} {
			if !strings.Contains(idx, l) || strings.Contains(idx, "<<<<<<<") {
				t.Errorf("%s index:\n%s", dir, idx)
			}
		}
	}
	// The same fact, rewritten on both: the rebase stops, and aborting it
	// keeps each machine's own.
	write(t, filepath.Join(Dir(a), "role.md"), "role from a\n")
	Commit(a, "Update the orchestrator's memory")
	gitIn(t, a, "push", "-q")
	write(t, filepath.Join(Dir(b), "role.md"), "role from b\n")
	Commit(b, "Update the orchestrator's memory")
	cmd := exec.Command("git", "-C", b, "pull", "-q", "--rebase", "--autostash")
	if err := cmd.Run(); err == nil {
		t.Fatal("the rebase went through a clash")
	}
	if Busy(b) == "" || Commit(b, "x") {
		t.Error("a commit during the stopped rebase")
	}
	gitIn(t, b, "rebase", "--abort")
	if read(t, filepath.Join(Dir(b), "role.md")) != "role from b\n" {
		t.Error("b lost its own fact")
	}
}
