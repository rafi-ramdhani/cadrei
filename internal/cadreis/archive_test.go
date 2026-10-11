package cadreis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
)

func tree(t *testing.T, dir string, files ...string) {
	t.Helper()
	for _, f := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// T-A1: the copy, check and remove fallback, and a move that stopped
// halfway, finished by moving again.
func TestMoveFinishesAStoppedMove(t *testing.T) {
	dir := t.TempDir()
	src, dst := dir+"/src", dir+"/archive/dst"

	// The fallback, as on another disk.
	tree(t, src, "a", "b/c")
	os.Symlink("a", src+"/link")
	if err := fsx.CopyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if err := fsx.SameTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if to, _ := os.Readlink(dst + "/link"); to != "a" {
		t.Errorf("the link was copied as %q", to)
	}
	os.RemoveAll(dst)

	// Stopped partway through the copy, even in the middle of a file:
	// cadrei's marked partial is redone (review 8, #6), on the copy path.
	rename = func(string, string) error { return os.ErrInvalid }
	t.Cleanup(func() { rename = os.Rename })
	partial := PartialOf(dst)
	tree(t, partial, partialMark, filepath.Base(dst)+"/a")
	os.WriteFile(partial+"/"+filepath.Base(dst)+"/b", nil, 0o644)
	if err := Move(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst + "/b/c"); err != nil {
		t.Error("the partial copy wasn't finished")
	}
	if _, err := os.Lstat(src); err == nil {
		t.Error("the original is still there")
	}

	if _, err := os.Lstat(partial); err == nil {
		t.Error("the partial is left")
	}

	// Stopped partway through removing the original: what's left of it
	// is the copy's own files, with their times. The whole copy stays.
	tree(t, src, "a")
	if fi, err := os.Stat(dst + "/a"); err == nil {
		os.Chtimes(src+"/a", fi.ModTime(), fi.ModTime())
	}
	if err := Move(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst + "/b/c"); err != nil {
		t.Error("the whole copy was removed")
	}
	if _, err := os.Lstat(src); err == nil {
		t.Error("the partial original is still there")
	}

	// Two different folders: nothing is removed.
	tree(t, src, "x")
	if err := Move(src, dst); err == nil {
		t.Error("moved onto a different folder")
	}
	if _, err := os.Stat(src + "/x"); err != nil {
		t.Error("the original was removed")
	}
	if _, err := os.Stat(dst + "/a"); err != nil {
		t.Error("the folder there was removed")
	}

	// A crew folder that's a link is moved as a link.
	real := dir + "/real"
	tree(t, real, "cadrei.conf")
	os.Symlink(real, dir+"/crew")
	if err := Move(dir+"/crew", dir+"/archive/crew"); err != nil {
		t.Fatal(err)
	}
	if to, _ := os.Readlink(dir + "/archive/crew"); to != real {
		t.Errorf("the crew link moved as %q", to)
	}
}

// Review 1: a manifest is checked before anything is restored from it: a
// crew name that climbs out, a reserved name or a duplicate is refused.
func TestAManifestWithBadNamesIsRefused(t *testing.T) {
	for _, names := range [][]string{{"../Library/LaunchAgents"}, {"config"}, {"a/b"}, {"main", "main"}, {""}} {
		dir := t.TempDir()
		var crews []ArchivedCrew
		for _, n := range names {
			crews = append(crews, ArchivedCrew{Name: n})
		}
		a := Archive{Dir: dir, Manifest: Manifest{Version: 1, Cadreis: crews, Steps: []string{StepDone}}}
		if err := a.Save(); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadArchive(dir); err == nil {
			t.Errorf("%v was read", names)
		}
	}
}

// Review 8, #12: an original changed after the copy, even to the same
// size, isn't taken for a copy's leftover: nothing is removed.
func TestMoveKeepsAnOriginalChangedAfterTheCopy(t *testing.T) {
	dir := t.TempDir()
	src, dst := dir+"/src", dir+"/dst"
	tree(t, src, "state.json")
	if err := fsx.CopyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	os.WriteFile(src+"/state.json", []byte("STATE.JSON"), 0o644) // same size
	os.Chtimes(src+"/state.json", later, later)
	if err := Move(src, dst); err == nil {
		t.Error("moved onto a copy of an older original")
	}
	if readFile(t, src+"/state.json") != "STATE.JSON" {
		t.Error("the newer original was removed")
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	raw, _ := os.ReadFile(p)
	return string(raw)
}

// Review round 3, item 4: Move removes only partial folders it made. A
// user's own folder at the same name, or an old-style name.partial, stays.
func TestMoveLeavesTheUsersPartials(t *testing.T) {
	dir := t.TempDir()
	src, dst := dir+"/archive/main", dir+"/cadrei/main"
	tree(t, src, "a")
	tree(t, dst+".partial", "thesis.txt")
	tree(t, PartialOf(dst), "mine.txt")
	if err := Move(src, dst); err == nil || !strings.Contains(err.Error(), "is in the way, and it isn't cadrei's") {
		t.Errorf("Move: %v", err)
	}
	for _, f := range []string{dst + ".partial/thesis.txt", PartialOf(dst) + "/mine.txt", src + "/a"} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s is gone", f)
		}
	}
	os.RemoveAll(PartialOf(dst))
	if err := Move(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst + ".partial/thesis.txt"); err != nil {
		t.Error("the user's name.partial was removed")
	}
}

// Review round 3, item 7: a disk that keeps coarser times (HFS+, exFAT,
// FAT) still counts as a whole copy, within 2 seconds.
func TestACopyOnACoarseDiskMatches(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir+"/src", "a")
	tree(t, dir+"/dst", "a")
	at := time.Date(2026, 10, 10, 9, 0, 0, 700e6, time.UTC)
	os.Chtimes(dir+"/src/a", at, at)
	os.Chtimes(dir+"/dst/a", at.Truncate(2*time.Second), at.Truncate(2*time.Second))
	if err := fsx.SameTree(dir+"/src", dir+"/dst"); err != nil {
		t.Errorf("rounded: %v", err)
	}
	os.Chtimes(dir+"/dst/a", at.Add(-3*time.Second), at.Add(-3*time.Second))
	if err := fsx.SameTree(dir+"/src", dir+"/dst"); err == nil {
		t.Error("3 seconds apart passed")
	}
}

// With CADREI_TEST_COARSE_DISK set to a folder on an HFS+, exFAT or FAT
// disk, Move copies a tree with sub-second times onto it and finishes.
func TestMoveOntoACoarseDisk(t *testing.T) {
	disk := os.Getenv("CADREI_TEST_COARSE_DISK")
	if disk == "" {
		t.Skip("CADREI_TEST_COARSE_DISK names no disk")
	}
	src := t.TempDir() + "/main"
	tree(t, src, "a", "b/c")
	at := time.Now().Truncate(time.Second).Add(700 * time.Millisecond)
	os.Chtimes(src+"/a", at, at)
	dst := filepath.Join(disk, "main")
	os.RemoveAll(dst)
	t.Cleanup(func() { os.RemoveAll(dst) })
	if err := Move(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst + "/b/c"); err != nil {
		t.Error("the copy isn't whole")
	}
}

// Review round 3, the case clash: two crews whose names differ only in
// case (made on a case-sensitive disk) still read, so their archive can be
// restored where the disk can hold both.
func TestCrewNamesThatDifferInCaseRead(t *testing.T) {
	dir := t.TempDir()
	a := Archive{Dir: dir, Manifest: Manifest{Version: 1, Cadreis: []ArchivedCrew{{Name: "dev"}, {Name: "Dev"}}, Steps: []string{StepDone}}}
	a.Save()
	if _, err := ReadArchive(dir); err != nil {
		t.Error(err)
	}
}

// Review of fix round 4 (maint): a move between disks flushes the copy,
// its folders and its new name before the original goes, and so does a
// move stopped after its rename, run again.
func TestMoveFlushesBeforeItRemoves(t *testing.T) {
	dir := t.TempDir()
	src, dst := dir+"/src", dir+"/archive/dst"
	tree(t, src, "a", "b/c")
	rename = func(string, string) error { return os.ErrInvalid }
	// The flushes counted when the original goes: all of them come first
	// (review of fix round 5, tests N4b).
	var atRemove int64
	removeOriginal = func(p string) error { atRemove = fsx.Syncs(); return os.RemoveAll(p) }
	t.Cleanup(func() { rename, removeOriginal = os.Rename, os.RemoveAll })
	before := fsx.Syncs()
	if err := Move(src, dst); err != nil {
		t.Fatal(err)
	}
	// 2 files, 2 folders, then dst's folder after the rename.
	if n := fsx.Syncs() - before; n != 5 || atRemove-before != 5 {
		t.Errorf("%d flushes, %d before the remove, want 5", n, atRemove-before)
	}
	tree(t, src, "a", "b/c")
	os.Chtimes(src+"/a", time.Now(), time.Now())
	for _, f := range []string{"a", "b/c"} {
		st, _ := os.Stat(dst + "/" + f)
		os.Chtimes(src+"/"+f, st.ModTime(), st.ModTime())
	}
	before = fsx.Syncs()
	if err := Move(src, dst); err != nil {
		t.Fatal(err)
	}
	if fsx.Syncs() == before || atRemove == before {
		t.Error("the stopped move removed its original with nothing flushed")
	}
}

// Review of fix round 5 (sec N8 and N10): an archive made here carries a
// seal, not this machine's id, and the seal fits that archive only: one
// copied onto another archive, or an archive with no id on this machine,
// isn't made here.
func TestAnArchivesSealFitsOnlyIt(t *testing.T) {
	fakeHome(t, true)
	a, err := NewArchive(Manifest{Version: 1, ArchivedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := MachineID()
	if !a.MadeHere() || a.Manifest.MadeBy == "" || strings.Contains(a.Manifest.MadeBy, id) {
		t.Errorf("made here %v, made_by %q", a.MadeHere(), a.Manifest.MadeBy)
	}
	raw, _ := os.ReadFile(filepath.Join(a.Dir, "manifest.json"))
	if strings.Contains(string(raw), id) {
		t.Error("the manifest holds the machine's id")
	}
	b, _ := NewArchive(Manifest{Version: 1, ArchivedAt: a.Manifest.ArchivedAt.Add(time.Minute)})
	b.Manifest.MadeBy = a.Manifest.MadeBy
	if b.MadeHere() {
		t.Error("another archive's seal was taken")
	}
	// The same time in another folder (review of fix round 6): the seal
	// is the folder's too.
	moved := Archive{Dir: filepath.Join(filepath.Dir(a.Dir), "2020-01-01-0000"), Manifest: a.Manifest}
	if moved.MadeHere() {
		t.Error("a seal was taken in another folder")
	}
	// No id on this machine: nothing is made here, even a seal made with
	// an empty key.
	os.Remove(filepath.Join(ArchiveRoot(), ".machine"))
	a.Manifest.MadeBy = a.seal("")
	if a.MadeHere() {
		t.Error("made here with no machine id")
	}
}
