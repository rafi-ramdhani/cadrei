package paths

import (
	"os"
	"path/filepath"
	"testing"
)

// tempDir is t.TempDir in its physical form (macOS: /var is /private/var).
func tempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestReal(t *testing.T) {
	d := tempDir(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(d, "real", "sub"), 0o755))
	must(os.Symlink(filepath.Join(d, "real"), filepath.Join(d, "abs")))
	must(os.Symlink("real/sub", filepath.Join(d, "rel")))
	must(os.Symlink("abs", filepath.Join(d, "chain")))
	must(os.Symlink("loop", filepath.Join(d, "loop")))
	for _, tc := range []struct{ in, want string }{
		{d + "/abs/sub", d + "/real/sub"},
		{d + "/rel", d + "/real/sub"},
		{d + "/chain/sub/missing/deeper", d + "/real/sub/missing/deeper"},
		{d + "/rel/../x", d + "/real/x"},       // .. after a link is physical
		{d + "/missing/../real", d + "/real"},  // .. after a missing part
		{d + "//real/./sub/", d + "/real/sub"}, // doubled and dot parts
		{d + "/loop/x", d + "/loop/x"},         // a loop ends
		{"/", "/"},
	} {
		if got := Real(tc.in); got != tc.want {
			t.Errorf("Real(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRealRelative(t *testing.T) {
	d := tempDir(t)
	t.Chdir(d)
	if got := Real("x/y"); got != d+"/x/y" {
		t.Errorf("Real(x/y) = %q", got)
	}
}

func TestHomeUsesHOME(t *testing.T) {
	d := tempDir(t)
	t.Setenv("HOME", d)
	if Home() != d {
		t.Errorf("Home() = %q, want %q", Home(), d)
	}
}

func TestWithin(t *testing.T) {
	for _, tc := range []struct {
		p, dir string
		want   bool
	}{
		{"/a/b", "/a", true}, {"/a", "/a", true}, {"/ab", "/a", false}, {"/a/b", "/a/", true}, {"/x", "/", true},
	} {
		if Within(tc.p, tc.dir) != tc.want {
			t.Errorf("Within(%q, %q) != %v", tc.p, tc.dir, tc.want)
		}
	}
}

// Review of fix round 5 (sec N6): a folder that isn't there yet still
// holds another spelling of a path inside it: its first parent that is
// there is compared by identity, the rest in any case.
func TestInsideAFolderThatIsntThereYet(t *testing.T) {
	home := tempDir(t)
	dir := home + "/.claude/skills"
	for path, want := range map[string]bool{
		home + "/.CLAUDE/Skills/x": true, home + "/.claude/skills": true, home + "/.Claude/skills/a/b": true,
		home + "/.clauder/skills": false, home + "/.claude": false, home + "/other/.claude/skills": false,
	} {
		if Inside(path, dir) != want {
			t.Errorf("Inside(%q) != %v", path, want)
		}
	}
}

// Review of fix round 5 (sec N7): a folder that shows the disk again
// (macOS's /System/Volumes/Data) holds what it shows.
func TestHoldsThroughAFolderThatShowsTheDiskAgain(t *testing.T) {
	root := tempDir(t)
	os.MkdirAll(root+"/real/home/.cadrei", 0o755)
	path := root + "/real/home/.cadrei"
	view := root + "/view"
	again := filepath.Join(view, path)
	os.MkdirAll(filepath.Dir(again), 0o755)
	if err := os.Symlink(path, again); err != nil {
		t.Fatal(err)
	}
	if !Holds(view, path) || !Holds(root+"/real", path) || Holds(root+"/elsewhere", path) {
		t.Error("Holds")
	}
	if _, err := os.Stat("/System/Volumes/Data"); err == nil {
		if home := Real(Home()); !Holds("/System/Volumes/Data", home) {
			t.Errorf("/System/Volumes/Data doesn't hold %s", home)
		}
	}
}
