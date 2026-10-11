package cadreis

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/gitx"
	"github.com/rafi-ramdhani/cadrei/internal/registry"
)

var tmpl = fstest.MapFS{
	"template/playbook.md":        {Data: []byte("# Playbook of {{name}}\n")},
	"template/projects.yaml":      {Data: []byte("# One entry per project.\n")},
	"template/cadrei.conf":        {Data: []byte("PERMISSION_MODE=default\n")},
	"template/.gitignore":         {Data: []byte(".DS_Store\nnode_modules/\n")},
	"template/teams/.gitkeep":     {Data: nil},
	"template/members/dev/pm.md":  {Data: []byte("# PM of {{name}}\n")},
	"template/members/dev/eng.md": {Data: []byte("# Engineer\n")},
}

// fakeHome gives the test a HOME of its own, with git set up there.
func fakeHome(t *testing.T, identity bool) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CADREI_HOME", "")
	gitconfig := filepath.Join(home, ".gitconfig-test")
	body := "[init]\n\tdefaultBranch = main\n"
	if identity {
		body += "[user]\n\tname = T\n\temail = t@example.com\n"
	}
	os.WriteFile(gitconfig, []byte(body), 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return home
}

func TestCheckName(t *testing.T) {
	for _, ok := range []string{"work", "Work-2", "a_b"} {
		if err := CheckName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".hidden", "my cadrei", "a.b", "x/y", "config", "Framework", "-x"} {
		if err := CheckName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCreateInCadreiFolder(t *testing.T) {
	home := fakeHome(t, true)
	c, note, err := Create("work", tmpl)
	if err != nil || note != "" {
		t.Fatalf("Create: %v %q", err, note)
	}
	if c.Path != home+"/.cadrei/work" || !c.Present() {
		t.Errorf("cadrei %+v", c)
	}
	if b, _ := os.ReadFile(c.Path + "/playbook.md"); string(b) != "# Playbook of work\n" {
		t.Errorf("name not put in: %q", b)
	}
	if b, _ := os.ReadFile(c.Path + "/.gitignore"); string(b) != ".DS_Store\nnode_modules/\n" {
		t.Errorf(".gitignore %q", b)
	}
	if _, err := os.Stat(c.Path + "/projects"); err == nil {
		t.Error("a cadrei in ~/.cadrei has projects/")
	}
	if _, err := os.Stat(c.Path + "/teams/.gitkeep"); err != nil {
		t.Error("dot files of the template were not copied")
	}
	if out, _ := exec.Command("git", "-C", c.Path, "log", "--format=%s").Output(); strings.TrimSpace(string(out)) != "Start work from the cadrei template" {
		t.Errorf("commit %q", out)
	}
	if _, _, err := Create("work", tmpl); err == nil {
		t.Error("a second cadrei with the same name was created")
	}
	if _, _, err := Create("WORK", tmpl); err == nil {
		t.Errorf("a name differing only in case: %v", err)
	}
	if _, _, err := Create("config", tmpl); err == nil {
		t.Error("a reserved name was accepted")
	}
}

func TestCreateWithoutAGitIdentity(t *testing.T) {
	fakeHome(t, false)
	c, note, err := Create("old", tmpl)
	if err != nil || !c.Present() {
		t.Fatal(err)
	}
	if !strings.Contains(note, "Git has no name or email set") || strings.Contains(note, c.Path) {
		t.Errorf("no note without a git identity: %q", note)
	}
}

func TestList(t *testing.T) {
	home := fakeHome(t, true)
	Create("b", tmpl)
	Create("a", tmpl)
	os.MkdirAll(home+"/.cadrei/notacadrei", 0o755)
	os.MkdirAll(home+"/.cadrei/.hidden/members", 0o755)
	os.MkdirAll(home+"/.cadrei/framework/members", 0o755)
	// A config/external list from the bash 0.2.0 work is not read.
	out, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(out+"/ext/members", 0o755)
	os.WriteFile(Config("external"), []byte(out+"/ext\n"), 0o600)
	list, _ := List()
	var names []string
	for _, c := range list {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "a,b" {
		t.Errorf("list %v", names)
	}
	if Default() != "" {
		t.Error("a default appeared")
	}
	SetDefault("a")
	if Default() != "a" {
		t.Errorf("Default %q", Default())
	}
}

func TestOldCadres(t *testing.T) {
	home := fakeHome(t, true)
	Create("work", tmpl)
	old := filepath.Join(home, "Documents", "demo")
	os.MkdirAll(old+"/personas", 0o755)
	os.WriteFile(old+"/projects.yaml", nil, 0o644)
	os.MkdirAll(home+"/.config/cadre", 0o755)
	os.WriteFile(home+"/.config/cadre/home", []byte("~/Documents/demo\n"), 0o644)
	if OldHome() != old {
		t.Errorf("OldHome %q", OldHome())
	}
	if !OldCadre(old) || OldCadre(old+"/personas") || OldCadre(home+"/.cadrei/work") || OldCadre(home) {
		t.Error("OldCadre")
	}
	os.WriteFile(home+"/.config/cadre/home", []byte("/nowhere/demo\n"), 0o644)
	if OldHome() != "" {
		t.Error("a missing old home was named")
	}
}

// addLink writes a project entry into a cadrei's registry and records its
// place on this machine.
func addLink(t *testing.T, c Cadrei, name, path string) {
	t.Helper()
	reg, _ := registry.Load(c.Registry())
	reg.Add(name, registry.Field{Key: "repo", Value: "me/" + name})
	if err := reg.Save(c.Registry()); err != nil {
		t.Fatal(err)
	}
	if err := SetPlace(c, name, expandHome(path)); err != nil {
		t.Fatal(err)
	}
}

func TestResolve(t *testing.T) {
	home := fakeHome(t, true)
	work, _, _ := Create("work", tmpl)
	life, _, _ := Create("life", tmpl)
	SetDefault("life")
	dev := filepath.Join(home, "Developer")
	for _, d := range []string{"app/src", "app/sub/inner", "shared", "unlinked"} {
		os.MkdirAll(filepath.Join(dev, d), 0o755)
	}
	addLink(t, work, "app", "~/Developer/app")
	addLink(t, work, "inner", dev+"/app/sub/inner")
	addLink(t, work, "shared", "~/Developer/shared")
	addLink(t, life, "shared", "~/Developer/shared")
	ext, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(ext+"/old/members", 0o755)

	for _, tc := range []struct{ cwd, cadrei, from string }{
		{work.Path + "/teams", "work", "from this folder"},
		{ext + "/old/projects/x", "life", "default"},
		{dev + "/app/src", "work", "from the project app"},
		{dev + "/app/sub/inner", "work", "from the project inner"},
		{dev + "/unlinked", "life", "default"},
		{home, "life", "default"},
	} {
		os.MkdirAll(tc.cwd, 0o755)
		r, err := Resolve(tc.cwd, nil)
		if err != nil || r.Name != tc.cadrei || r.From != tc.from || r.Default != "life" {
			t.Errorf("Resolve(%s) = %+v, %v; want %s %s", tc.cwd, r, err, tc.cadrei, tc.from)
		}
	}
	// One project, two cadreis: refused without a terminal, asked with one.
	_, err := Resolve(dev+"/shared", nil)
	if err == nil || err.Error() != "shared is in more than one crew (life and work). Run this from the crew's folder, ~/.cadrei/<name>" {
		t.Errorf("two cadreis without a terminal: %v", err)
	}
	r, err := Resolve(dev+"/shared", func(project string, names []string) (string, error) { return "work", nil })
	if err != nil || r.Name != "work" || r.Project != "shared" {
		t.Errorf("asked: %+v %v", r, err)
	}
	if got := Linking(dev + "/shared"); len(got) != 2 {
		t.Errorf("Linking: %+v", got)
	}
	// CADREI_HOME wins wherever the command runs, registered or not.
	t.Setenv("CADREI_HOME", ext+"/old")
	if r, _ := Resolve(work.Path, nil); r.Name != "old" || r.From != "from CADREI_HOME" {
		t.Errorf("CADREI_HOME: %+v", r)
	}
	t.Setenv("CADREI_HOME", "")
	// Two present cadreis with one name in another letter case (a file
	// system that tells them apart) are not used.
	os.MkdirAll(home+"/.cadrei/WORK/members", 0o755)
	a, _ := os.Stat(home + "/.cadrei/WORK")
	if b, _ := os.Stat(work.Path); !os.SameFile(a, b) {
		if _, err := Resolve(work.Path, nil); err == nil || !strings.Contains(err.Error(), "share the name") {
			t.Errorf("two cadreis with one name: %v", err)
		}
	}
}

func TestResolveWithNothing(t *testing.T) {
	home := fakeHome(t, true)
	if _, err := Resolve(home, nil); !errors.Is(err, ErrNoCadrei) {
		t.Errorf("no cadrei: %v", err)
	}
	SetDefault("gone")
	if _, err := Resolve(home, nil); !errors.Is(err, ErrNoCadrei) {
		t.Errorf("a missing default: %v", err)
	}
}

func TestPlaces(t *testing.T) {
	home := fakeHome(t, true)
	w := Cadrei{Name: "w", Path: home + "/.cadrei/w"}
	e := func(text string) *registry.Entry { return registry.Parse(text).Entries()[0] }
	// A path in the registry, from another machine or an older build, is
	// not read: only this machine's places are.
	if d := ProjectDir(w, e("a:\n  repo: r\n  path: ~/Dev/a\n")); d != "" {
		t.Errorf("a registry path was read: %s", d)
	}
	SetPlace(w, "a", home+"/Dev/a")
	SetPlace(w, "b", "/elsewhere/b")
	if d := ProjectDir(w, e("a:\n  repo: r\n")); d != home+"/Dev/a" {
		t.Errorf("place: %s", d)
	}
	if raw, _ := os.ReadFile(PlacesFile(w)); !strings.Contains(string(raw), `"a": "~/Dev/a"`) || !strings.Contains(string(raw), `"b": "/elsewhere/b"`) {
		t.Errorf("places file:\n%s", raw)
	}
	if st, _ := os.Stat(PlacesFile(w)); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	// Another cadrei has its own places.
	if Place(Cadrei{Name: "v"}, "a") != "" {
		t.Error("places leaked to another cadrei")
	}
	SetPlace(w, "a", "")
	if Place(w, "a") != "" || Place(w, "b") != "/elsewhere/b" {
		t.Error("forgetting a place")
	}
	// A relative entry, which cadrei never writes, is ignored.
	os.WriteFile(PlacesFile(w), []byte(`{"c": "rel/c"}`), 0o600)
	if Place(w, "c") != "" {
		t.Error("a relative place was used")
	}
}

func TestWhere(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ dir, want string }{
		{"", "not here"},
		{dir, "present"},
		{dir + "/gone", "missing"},
		{"/Volumes/cadrei-no-such-drive/app", "drive"},
		{"/media/someone/cadrei-no-such-drive/app", "drive"},
	} {
		if got := Where(tc.dir); got != tc.want {
			t.Errorf("Where(%q) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

func TestListSkipsLinksAndBadNames(t *testing.T) {
	home := fakeHome(t, true)
	Create("work", tmpl)
	look := filepath.Join(home, "lookalike")
	os.MkdirAll(look+"/members", 0o755)
	os.Symlink(look, home+"/.cadrei/linked")
	os.MkdirAll(home+"/.cadrei/my.cadrei/members", 0o755)
	list, _ := List()
	if len(list) != 1 || list[0].Name != "work" {
		t.Errorf("list %+v", list)
	}
}

// On a case-insensitive disk (macOS), another spelling of a cadrei's folder
// or a project's folder is that folder.
func TestResolveThroughAnotherSpelling(t *testing.T) {
	home := fakeHome(t, true)
	Create("work", tmpl)
	play, _, _ := Create("play", tmpl)
	SetDefault("work")
	upper := home + "/.CADREI/PLAY/teams"
	if _, err := os.Stat(upper); err != nil {
		t.Skip("the disk is case-sensitive")
	}
	if r, err := Resolve(upper, nil); err != nil || r.Name != "play" || r.From != "from this folder" {
		t.Errorf("a case variant of a cadrei folder: %+v %v", r, err)
	}
	os.MkdirAll(home+"/Dev/app", 0o755)
	addLink(t, play, "app", "~/Dev/app")
	if r, err := Resolve(home+"/DEV/APP", nil); err != nil || r.Name != "play" || r.Project != "app" {
		t.Errorf("a case variant of a project folder: %+v %v", r, err)
	}
	t.Setenv("CADREI_HOME", home+"/.cadrei/PLAY")
	if r, _ := Resolve(home, nil); r.Name != "play" {
		t.Errorf("CADREI_HOME in another case: %+v", r)
	}
}

// A places file that does not parse is refused, not replaced: replacing it
// would lose every other project's place. Entries cadrei does not read are
// kept as they are.
func TestABrokenPlacesFileIsKept(t *testing.T) {
	home := fakeHome(t, true)
	w := Cadrei{Name: "w", Path: home + "/.cadrei/w"}
	SetPlace(w, "a", "/elsewhere/a")
	for _, bad := range []string{"not json", "null", `["a"]`} {
		os.WriteFile(PlacesFile(w), []byte(bad), 0o600)
		if err := SetPlace(w, "b", "/elsewhere/b"); err == nil || !strings.Contains(err.Error(), "fix or remove it (nothing was changed)") {
			t.Errorf("%s: %v", bad, err)
		}
		if raw, _ := os.ReadFile(PlacesFile(w)); string(raw) != bad {
			t.Errorf("%s was replaced with %s", bad, raw)
		}
	}
	os.WriteFile(PlacesFile(w), []byte(`{"x": 1, "places": {"a": "/elsewhere/a"}}`), 0o600)
	if err := SetPlace(w, "b", "/elsewhere/b"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(PlacesFile(w)); !strings.Contains(string(raw), `"x": 1`) || Place(w, "a") != "/elsewhere/a" || Place(w, "b") != "/elsewhere/b" {
		t.Errorf("places file:\n%s", raw)
	}
}

// Commit runs none of the crew's hooks or its fsmonitor (review of fix
// round 5, N1).
func TestCommitRunsNothingOfTheCrews(t *testing.T) {
	dir, ran := t.TempDir(), t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "T")
	git("config", "user.email", "t@example.com")
	git("config", "core.fsmonitor", "touch "+ran+"/fsmonitor; false")
	for _, h := range []string{"pre-commit", "commit-msg", "post-commit", "post-index-change"} {
		os.WriteFile(dir+"/.git/hooks/"+h, []byte("#!/bin/sh\ntouch "+ran+"/"+h+"\n"), 0o755)
	}
	os.WriteFile(dir+"/notes.md", []byte("x\n"), 0o644)
	if note, err := Commit(dir, "notes", "notes.md"); note != "" || err != nil {
		t.Fatalf("commit: %q %v", note, err)
	}
	if out, _ := exec.Command("git", "-C", dir, "-c", "core.fsmonitor=false", "log", "--format=%s").Output(); string(out) != "notes\n" {
		t.Errorf("log %q", out)
	}
	if left, _ := os.ReadDir(ran); len(left) > 0 {
		t.Errorf("ran: %v", left)
	}
}

// A committed link where cadrei reads files or runs sessions is listed; one
// elsewhere, or a plain file, is not.
func TestCommittedLinks(t *testing.T) {
	dir := t.TempDir()
	scratch := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	os.MkdirAll(dir+"/teams", 0o755)
	os.MkdirAll(dir+"/docs", 0o755)
	os.WriteFile(dir+"/playbook.md", []byte("# Playbook\n"), 0o644)
	os.Symlink(scratch, dir+"/teams/dev")
	os.Symlink("playbook.md", dir+"/protocol.md")
	os.Symlink(scratch, dir+"/docs/notes")
	os.MkdirAll(dir+"/rt", 0o755)
	os.Symlink(scratch, dir+"/rt/build")
	run("add", "-A")
	got, err := CommittedLinks(dir, "rt")
	if err != nil || strings.Join(got, " ") != "protocol.md rt/build teams/dev" {
		t.Errorf("links %q %v", got, err)
	}
}

// Review of fix round 6 (must-fix 5): a crew with no repo of its own, or a
// .git git won't take, isn't committed through a repo above it, such as a
// ~/.cadrei/.git a restore brought back.
func TestCommitStaysInTheCrewsOwnRepo(t *testing.T) {
	for _, layout := range []string{"no .git", "an empty .git"} {
		above := t.TempDir()
		git := func(args ...string) string {
			t.Helper()
			out, _ := exec.Command("git", append([]string{"-C", above, "-c", "core.fsmonitor=false"}, args...)...).CombinedOutput()
			return string(out)
		}
		git("init", "-q")
		git("config", "user.name", "T")
		git("config", "user.email", "t@example.com")
		crew := filepath.Join(above, "work")
		os.MkdirAll(crew, 0o755)
		if layout == "an empty .git" {
			os.Mkdir(filepath.Join(crew, ".git"), 0o755)
		}
		os.WriteFile(filepath.Join(crew, "notes.md"), []byte("x\n"), 0o644)
		if note, err := Commit(crew, "notes", "notes.md"); note != "" || err != nil {
			t.Errorf("%s: commit: %q %v", layout, note, err)
		}
		if log := git("log", "--format=%s"); strings.Contains(log, "notes") {
			t.Errorf("%s: committed in the repo above: %q", layout, log)
		}
		if staged := git("diff", "--cached", "--name-only"); staged != "" {
			t.Errorf("%s: added to the repo above: %q", layout, staged)
		}
		if out, err := gitx.Reading(crew, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
			t.Errorf("%s: git found a repo for the crew: %s", layout, out)
		}
	}
}

// Forward-compatibility audit M3: a crew's .claude/local file a newer
// cadrei wrote is still read, but never written over, so its version
// is never lowered either; a version 1 file stays version 1.
func TestANewerLocalFileIsntWrittenOver(t *testing.T) {
	fakeHome(t, true)
	c, _, err := Create("work", tmpl)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(LocalDir(c.Path), 0o700)
	newer := `{"places":{"app":"/src/app"},"version":2,"hint":{"remote":"x"}}` + "\n"
	os.WriteFile(PlacesFile(c), []byte(newer), 0o600)
	if err := SetPlace(c, "lib", "/src/lib"); err == nil || !strings.Contains(err.Error(), "was written by a newer cadrei. Upgrade cadrei; nothing was changed") {
		t.Errorf("SetPlace over version 2: %v", err)
	}
	if raw, _ := os.ReadFile(PlacesFile(c)); string(raw) != newer {
		t.Errorf("the newer file changed: %s", raw)
	}
	if Places(c)["app"] != "/src/app" {
		t.Error("a newer file's places can't be read")
	}
	os.WriteFile(PlacesFile(c), []byte(`{"places":{},"version":1}`), 0o600)
	if err := SetPlace(c, "lib", "/src/lib"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(PlacesFile(c)); !strings.Contains(string(raw), `"version": 1`) {
		t.Errorf("version 1: %s", raw)
	}
}

// Forward-compatibility audit M3: an archive a newer cadrei made isn't
// read for a restore, and counts among the unreadable ones.
func TestANewerArchiveIsRefused(t *testing.T) {
	fakeHome(t, true)
	a, err := NewArchive(Manifest{Version: ManifestVersion + 1, ArchivedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadArchive(a.Dir); err == nil || !strings.Contains(err.Error(), "was made by a newer cadrei (archive version 2). Upgrade cadrei to restore it") {
		t.Errorf("ReadArchive: %v", err)
	}
	if len(Archives()) != 0 || len(Unreadable()) != 1 {
		t.Errorf("archives %v, unreadable %v", Archives(), Unreadable())
	}
}

// Review of fix round 8 (W1): a file a newer cadrei wrote after this one
// read it isn't written over either: writeLocal checks the file on disk,
// not only what was read.
func TestWriteLocalChecksTheFileOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	read := map[string]json.RawMessage{"version": json.RawMessage("1")}
	os.WriteFile(path, []byte(`{"version":2,"x":"newer"}`), 0o600)
	if err := writeLocal(path, read); err == nil || !strings.Contains(err.Error(), "was written by a newer cadrei") {
		t.Errorf("writeLocal over version 2 on disk: %v", err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != `{"version":2,"x":"newer"}` {
		t.Errorf("the file changed: %s", raw)
	}
}
