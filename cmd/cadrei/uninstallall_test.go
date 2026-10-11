//go:build !windows

package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// allSetup is a machine with two crews, a project linked from work, a
// running team, an archive, and a crew with changes that aren't
// committed. It returns the project's folder.
func allSetup(t *testing.T, home string) string {
	t.Helper()
	must(t, "init", "work")
	must(t, "init", "life")
	app := home + "/src/app"
	os.MkdirAll(app, 0o755)
	exec.Command("git", "-C", app, "init", "-q").Run()
	os.WriteFile(app+"/main.go", []byte("package main\n"), 0o644)
	must(t, "project", "add", "app", "--path", app, "--no-trust")
	os.WriteFile(home+"/.cadrei/life/notes.md", []byte("x\n"), 0o644)
	if _, err := cadreis.NewArchive(cadreis.Manifest{Version: 1, ArchivedAt: time.Now(), Home: home}); err != nil {
		t.Fatal(err)
	}
	return app
}

// cadrei uninstall --all deletes every crew, cadrei's settings and every
// archive, after its dry run and the typed phrase, and keeps every
// project's folder.
func TestUninstallAllDeletesEverythingButProjects(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	// Claude Code's config, with a folder of the user's own trusted.
	os.WriteFile(home+"/.claude.json", []byte(`{"projects":{"/elsewhere/x":{"hasTrustDialogAccepted":true}}}`+"\n"), 0o600)
	app := allSetup(t, home)
	if trusted(t, home)[home+"/.cadrei/work"] != true {
		t.Fatal("the crew isn't trusted to begin with")
	}
	must(t, "up", "dev")

	out := must(t, "uninstall", "--all", "--dry-run")
	for _, want := range []string{"cadrei uninstall --all will:", "stop 2 team members", "delete the crew life (~/.cadrei/life): 1 team, 2 members, 0 projects (their folders stay)",
		"delete the crew work (~/.cadrei/work): 1 team, 2 members, 1 project (their folders stay)", "delete ~/.cadrei-archive, with your archive in it",
		"It keeps every project's folder: app (~/src/app).", "! life has no backup, so nothing of it is kept anywhere.", "Nothing was deleted."} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	for _, p := range []string{home + "/.cadrei/work", home + "/.cadrei-archive"} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("the dry run deleted %s", p)
		}
	}

	// In a terminal: a wrong phrase stops it, the right one goes on.
	ttyForTests = true
	t.Cleanup(func() { ttyForTests = false })
	for _, typed := range []string{"yes", "Delete everything", "delete everything.", ""} {
		if code, _, _ := callIn(typed+"\n", "uninstall", "--all"); code == 0 {
			t.Errorf("%q went on", typed)
		}
	}
	if _, err := os.Stat(home + "/.cadrei/work"); err != nil {
		t.Fatal("a plain yes deleted the crew")
	}
	code, out, errOut := callIn(deletePhrase+"\n", "uninstall", "--all")
	if code != 0 || !strings.Contains(out, "Deleted life and work") {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	for _, p := range []string{home + "/.cadrei", home + "/.cadrei-archive", home + "/.claude/skills/cadrei"} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s is still there", p)
		}
	}
	if raw, err := os.ReadFile(app + "/main.go"); err != nil || string(raw) != "package main\n" {
		t.Errorf("the project's folder lost its work: %v", err)
	}
	if left, _ := exec.Command("tmux", "-L", socket, "list-sessions", "-F", "#{session_name}").Output(); strings.Contains(string(left), "cadrei") {
		t.Errorf("sessions left: %s", left)
	}
	// Untrust takes out the mark and keeps the key.
	after := trusted(t, home)
	for _, crew := range []string{"work", "life"} {
		if after[home+"/.cadrei/"+crew] != false {
			t.Errorf("%s is still trusted in Claude Code's config", crew)
		}
	}
	if after["/elsewhere/x"] != true {
		t.Error("the user's own trusted folder lost its trust")
	}
}

// trusted reads which folders Claude Code's config trusts: true, false
// for a folder with no mark, nil for one it doesn't name.
func trusted(t *testing.T, home string) map[string]any {
	t.Helper()
	var cfg struct {
		Projects map[string]map[string]any `json:"projects"`
	}
	if err := json.Unmarshal([]byte(readFile(t, home+"/.claude.json")), &cfg); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	for dir, v := range cfg.Projects {
		out[dir] = v["hasTrustDialogAccepted"] == true
	}
	return out
}

// Without a terminal, --all needs --confirm with the exact phrase; plain
// --yes is never enough, and --archive is refused with it in either order.
func TestUninstallAllRefusesWithoutTheTypedYes(t *testing.T) {
	home := sandbox(t)
	allSetup(t, home)
	for _, tc := range []struct {
		want string
		args []string
	}{
		{"--yes isn't enough to delete every crew", []string{"uninstall", "--all", "--yes"}},
		{"--yes isn't enough to delete every crew", []string{"uninstall", "--yes", "--all"}},
		{"--all and --archive don't go together", []string{"uninstall", "--all", "--archive"}},
		{"--all and --archive don't go together", []string{"uninstall", "--archive", "--all"}},
		{"--all and --archive don't go together", []string{"uninstall", "--archive", "--dry-run", "--all"}},
		{"needs your typed yes, and this isn't a terminal", []string{"uninstall", "--all"}},
		{"--confirm takes exactly \"delete everything\"", []string{"uninstall", "--all", "--confirm", "yes"}},
		{"--confirm takes exactly \"delete everything\"", []string{"uninstall", "--all", "--confirm"}},
		{"pick one: --dry-run or --confirm", []string{"uninstall", "--all", "--dry-run", "--confirm", deletePhrase}},
	} {
		refused(t, tc.want, tc.args...)
	}
	for _, p := range []string{home + "/.cadrei/work", home + "/.cadrei/life", home + "/.cadrei/config", home + "/.cadrei-archive"} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("a refusal deleted %s", p)
		}
	}
	// The explicit phrase works without a terminal.
	if code, out, errOut := call("uninstall", "--all", "--confirm", deletePhrase); code != 0 || !strings.Contains(out, "Uninstalled.") {
		t.Errorf("--confirm: exit %d\n%s%s", code, out, errOut)
	}
	if _, err := os.Stat(home + "/src/app/main.go"); err != nil {
		t.Error("the project's folder went")
	}
}

// The dry run flags a crew whose backup is behind, and a project folder
// inside what --all deletes stops it before anything is deleted. An
// archives folder that is a link loses the link only.
func TestUninstallAllGuardsWhatIsntCadreis(t *testing.T) {
	home := sandbox(t)
	allSetup(t, home)
	work := home + "/.cadrei/work"
	backedUp(t, work)
	os.WriteFile(work+"/playbook.md", []byte("# Changed\n"), 0o644)
	exec.Command("git", "-C", work, "commit", "-qam", "changed").Run()
	os.WriteFile(work+"/playbook.md", []byte("# Changed again\n"), 0o644)
	out := must(t, "uninstall", "--all", "--dry-run")
	for _, want := range []string{"! work has changes that aren't committed.", "! work has 1 commit that isn't in its backup ("} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}

	inside := work + "/teams/dev/notes"
	os.MkdirAll(inside, 0o755)
	if err := cadreis.SetPlace(cadreis.Cadrei{Name: "work", Path: work}, "app", inside); err != nil {
		t.Fatal(err)
	}
	refused(t, "the project app's folder, ~/.cadrei/work/teams/dev/notes, is inside ~/.cadrei, which this deletes", "uninstall", "--all", "--confirm", deletePhrase)
	if _, err := os.Stat(inside); err != nil {
		t.Fatal("the refusal deleted the project")
	}
	cadreis.SetPlace(cadreis.Cadrei{Name: "work", Path: work}, "app", home+"/src/app")

	// A project inside ~/.cadrei-archive, reached by another spelling of
	// its folder, or through a link.
	inArchive := home + "/.cadrei-archive/proj"
	os.MkdirAll(inArchive, 0o755)
	os.Symlink(inside, home+"/plink")
	spellings := map[string]string{inArchive: "~/.cadrei-archive/proj, is inside ~/.cadrei-archive", home + "/plink": "~/.cadrei/work/teams/dev/notes, is inside ~/.cadrei,"}
	if _, err := os.Stat(home + "/.CADREI/WORK/teams"); err == nil {
		// A disk that ignores case.
		spellings[home+"/.CADREI/WORK/teams/dev/notes"] = "is inside ~/.cadrei,"
	}
	for place, want := range spellings {
		// Written as is: SetPlace would resolve the link.
		os.WriteFile(cadreis.PlacesFile(cadreis.Cadrei{Name: "work", Path: work}), []byte(`{"places":{"app":"`+place+`"},"version":1}`), 0o600)
		refused(t, want, "uninstall", "--all", "--confirm", deletePhrase)
	}
	os.Remove(home + "/plink")
	os.RemoveAll(inArchive)
	cadreis.SetPlace(cadreis.Cadrei{Name: "work", Path: work}, "app", home+"/src/app")

	kept := home + "/elsewhere/archives"
	os.MkdirAll(home+"/elsewhere", 0o755)
	os.Rename(home+"/.cadrei-archive", kept)
	os.Symlink(kept, home+"/.cadrei-archive")
	if !strings.Contains(flat(must(t, "uninstall", "--all", "--dry-run")), "remove the link ~/.cadrei-archive (its folder ~/elsewhere/archives stays)") {
		t.Error("the plan doesn't keep the linked archives")
	}
	must(t, "uninstall", "--all", "--confirm", deletePhrase)
	if _, err := os.Lstat(home + "/.cadrei-archive"); err == nil {
		t.Error("the link is still there")
	}
	if entries, err := os.ReadDir(kept); err != nil || len(entries) == 0 {
		t.Errorf("the linked folder lost its archives: %v", err)
	}
}

// Review of fix round 6 (must-fix 2): when tmux can't list the sessions
// (a socket that can't be opened, a server of another version), no
// teardown can see which teams run, so none moves or deletes anything.
func TestATmuxThatCantListStopsEveryTeardown(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	must(t, "up", "dev")
	sock := session.Default().Path()
	if err := os.Chmod(sock, 0); err != nil {
		t.Skip("can't change the socket's mode")
	}
	t.Cleanup(func() { os.Chmod(sock, 0o700) })
	if out, _ := exec.Command("tmux", "-S", sock, "has-session").CombinedOutput(); !strings.Contains(string(out), "Permission denied") {
		t.Skipf("the socket is still open to this user: %q", out)
	}
	if out := must(t, "uninstall", "--all", "--dry-run"); !strings.Contains(flat(out), "Couldn't see which teams run: tmux says") {
		t.Errorf("the dry run doesn't say why it would stop:\n%s", out)
	}
	for _, args := range [][]string{{"uninstall", "--yes"}, {"uninstall", "--archive", "--yes"}, {"uninstall", "--all", "--confirm", deletePhrase}} {
		refused(t, "Nothing was changed, since a team that still runs would go on writing into its crew", args...)
	}
	for _, p := range []string{home + "/.cadrei/work", home + "/.cadrei/config"} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("a refusal moved or deleted %s", p)
		}
	}
	if _, err := os.Stat(home + "/.cadrei-archive"); err == nil {
		t.Error("a refusal made an archive")
	}
}

// Review of fix round 6 (must-fix 3): no member can uninstall, archive or
// restore, by its variables (set counts, even empty) or, with those gone,
// by its tmux window's own marker, found from the process up.
func TestAMemberCantUninstallOrRestore(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	allSetup(t, home)
	must(t, "up", "dev")
	commands := [][]string{{"uninstall", "--yes"}, {"uninstall", "--archive", "--yes"}, {"uninstall", "--all", "--confirm", deletePhrase}, {"restore"}}
	for _, v := range []string{"CADREI_MEMBER", "CADRE_PERSONA"} {
		for _, value := range []string{"work-dev-engineer", ""} {
			t.Setenv(v, value)
			for _, args := range commands {
				refused(t, "team members can't", args...)
			}
			os.Unsetenv(v)
		}
	}

	// A window of the member's own: a shell, and a program it runs.
	tmuxIn(socket, "new-window", "-d", "-t", "=cadrei-work-dev:", "-n", "shell", "sh -c 'sleep 60; :'")
	tmuxIn(socket, "set-option", "-w", "-t", "=cadrei-work-dev:shell", "@cadrei_member", "work-dev-engineer")
	shell, _ := strconv.Atoi(tmuxIn(socket, "display", "-p", "-t", "=cadrei-work-dev:shell", "#{pane_pid}"))
	var child int
	for range 50 {
		if out, err := exec.Command("pgrep", "-P", strconv.Itoa(shell)).Output(); err == nil {
			child, _ = strconv.Atoi(strings.TrimSpace(string(out)))
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if shell == 0 || child == 0 {
		t.Fatalf("no shell window: %d %d", shell, child)
	}
	t.Cleanup(func() { selfPID = os.Getpid })
	for _, pid := range []int{shell, child} {
		selfPID = func() int { return pid }
		for _, args := range commands {
			refused(t, "This runs in the window of the member work-dev-engineer", args...)
		}
	}
	// The user's own window in a team's session has no member's marker.
	tmuxIn(socket, "set-option", "-wu", "-t", "=cadrei-work-dev:shell", "@cadrei_member")
	if name := memberWindow([]session.Tmux{session.Default()}, child); name != "" {
		t.Errorf("a window with no member's marker counts as %s's", name)
	}
	selfPID = os.Getpid
	for _, p := range []string{home + "/.cadrei/work", home + "/.cadrei/life", home + "/.cadrei/config", home + "/.cadrei-archive"} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("a refusal moved or deleted %s", p)
		}
	}
}

// Review of fix round 6 (must-fix 4): --all refuses when it can't read a
// crew's projects or places, which could hide a project inside, and checks
// them again after the typed phrase.
func TestUninstallAllChecksProjectsItCantRead(t *testing.T) {
	home := sandbox(t)
	allSetup(t, home)
	work := cadreis.Cadrei{Name: "work", Path: home + "/.cadrei/work"}
	proj := work.Path + "/proj"
	os.MkdirAll(proj, 0o755)
	os.WriteFile(proj+"/keep", []byte("w\n"), 0o644)
	places := cadreis.PlacesFile(work)
	good := readFile(t, places)
	for name, raw := range map[string]string{
		"truncated":       `{"places":{"app":"~/.cadrei/work/proj"},"version":1`,
		"not a folder":    `{"places":{"app":"~/src/app","zz":1},"version":1}`,
		"another project": `{"places":{"app":"~/src/app","notes":"~/.cadrei/work/proj"},"version":1}`,
	} {
		os.WriteFile(places, []byte(raw), 0o600)
		want := "couldn't read ~/.cadrei/work/.claude/local/places.json, so this can't check that no project's folder is in what it deletes"
		if name == "another project" {
			want = "the project notes's folder, ~/.cadrei/work/proj, is inside ~/.cadrei, which this deletes"
		}
		refused(t, want, "uninstall", "--all", "--confirm", deletePhrase)
		if _, err := os.Stat(proj + "/keep"); err != nil {
			t.Fatalf("%s: the project's folder went", name)
		}
	}
	os.WriteFile(places, []byte(good), 0o600)
	for _, file := range []string{places, work.Registry()} {
		os.Chmod(file, 0)
		if code, _, errOut := call("uninstall", "--all", "--dry-run"); code == 0 || !strings.Contains(flat(errOut), "couldn't read "+cadreis.Tilde(file)+", so this can't check that no project's folder is in what it deletes: permission denied") {
			t.Errorf("unreadable %s: exit %d %q", file, code, errOut)
		}
		os.Chmod(file, 0o600)
	}
	old := home + "/.cadrei/config/places/gone.json"
	os.MkdirAll(home+"/.cadrei/config/places", 0o700)
	os.WriteFile(old, []byte(`{"places":{"x":"~/.cadrei/work/proj"},"version":1}`), 0o600)
	refused(t, "the project x's folder, ~/.cadrei/work/proj, is inside ~/.cadrei", "uninstall", "--all", "--dry-run")
	// One it can't read (review of fix round 7).
	os.Chmod(old, 0)
	refused(t, "couldn't read cadrei 0.2.0's notes of where projects are", "uninstall", "--all", "--dry-run")
	os.Remove(old)

	// A project linked in while the user types the phrase.
	ttyForTests = true
	t.Cleanup(func() { ttyForTests = false })
	in := &typing{text: deletePhrase + "\n", meanwhile: func() {
		os.WriteFile(places, []byte(`{"places":{"app":"~/.cadrei/work/proj"},"version":1}`), 0o600)
	}}
	var out, errOut strings.Builder
	if code := run([]string{"uninstall", "--all"}, in, &out, &errOut); code == 0 || !strings.Contains(flat(errOut.String()), "is inside ~/.cadrei, which this deletes. Nothing was deleted.") {
		t.Errorf("a project linked in meanwhile: exit %d\n%s%s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(proj + "/keep"); err != nil {
		t.Fatal("the project's folder went")
	}
	// A crew made meanwhile.
	os.WriteFile(places, []byte(good), 0o600)
	in = &typing{text: deletePhrase + "\n", meanwhile: func() { must(t, "new", "late") }}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"uninstall", "--all"}, in, &out, &errOut); code == 0 || !strings.Contains(flat(errOut.String()), "your crews changed while you read the plan") {
		t.Errorf("a crew made meanwhile: exit %d\n%s%s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(home + "/.cadrei/late"); err != nil {
		t.Error("the crew made meanwhile went")
	}
}

// typing is a user who does something else (meanwhile) before typing text.
type typing struct {
	text      string
	meanwhile func()
	done      bool
}

func (r *typing) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		r.meanwhile()
	}
	if r.text == "" {
		return 0, io.EOF
	}
	n := copy(p, r.text)
	r.text = r.text[n:]
	return n, nil
}

// Review of fix round 6 (must-fix 6): a team that won't stop, or an
// orchestrator open in another terminal, stops --all before it deletes
// anything; and a linked ~/.cadrei loses only the link and the crews.
func TestUninstallAllStopsForWhatStillRuns(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	allSetup(t, home)
	must(t, "up", "dev")

	rt, _ := runtime.Get(runtimeName())
	lock := orchestrator.LockPath(rt.BuildDir(home + "/.cadrei/life"))
	if _, err := orchestrator.WriteLock(lock, os.Getpid(), orchestrator.Terminal, "", ""); err != nil {
		t.Fatal(err)
	}
	// The dry run names it; a real run stops at it.
	if out := must(t, "uninstall", "--all", "--dry-run"); !strings.Contains(flat(out), "The orchestrator of life is open in another terminal (open since") {
		t.Errorf("the dry run doesn't name the open orchestrator:\n%s", out)
	}
	refused(t, "an orchestrator is open in another terminal. Nothing was deleted.", "uninstall", "--all", "--confirm", deletePhrase)
	// The orchestrator this runs under is the user's, closed after.
	if _, err := orchestrator.WriteLock(lock, os.Getppid(), orchestrator.Terminal, "", ""); err != nil {
		t.Fatal(err)
	}
	if out := must(t, "uninstall", "--all", "--dry-run"); strings.Contains(out, "is open in another terminal") {
		t.Errorf("the orchestrator this runs under counts as another:\n%s", out)
	}
	os.Remove(lock)

	// A tmux whose kill-session fails.
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	bin := home + "/stuck"
	os.MkdirAll(bin, 0o755)
	writeStub(t, bin+"/tmux", "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = kill-session ] && exit 1; done\nexec '"+real+"' \"$@\"\n")
	path := os.Getenv("PATH")
	t.Setenv("PATH", bin+":"+path)
	code, out, errOut := call("uninstall", "--all", "--confirm", deletePhrase)
	if code != 2 || !strings.Contains(flat(out+errOut), "Nothing was deleted, since a team is still running.") {
		t.Errorf("a team that won't stop: exit %d\n%s%s", code, out, errOut)
	}
	for _, p := range []string{home + "/.cadrei/work", home + "/.cadrei/life", home + "/.cadrei/config", home + "/.cadrei-archive"} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s went while a team ran", p)
		}
	}
	t.Setenv("PATH", path)
	must(t, "stop", "--all", "--yes")

	// ~/.cadrei as a link: the crews and the link go, and what else its
	// folder holds stays.
	store := home + "/store/cadrei"
	os.MkdirAll(home+"/store", 0o755)
	if err := os.Rename(home+"/.cadrei", store); err != nil {
		t.Fatal(err)
	}
	os.Symlink(store, home+"/.cadrei")
	os.WriteFile(store+"/mine.txt", []byte("mine\n"), 0o644)
	// A folder of the user's own with a members folder isn't a crew, and
	// a project there isn't in what --all deletes.
	os.MkdirAll(store+"/thesis/members", 0o755)
	os.WriteFile(store+"/thesis/chapter1.tex", []byte("x\n"), 0o644)
	exec.Command("git", "-C", store+"/thesis", "init", "-q").Run()
	cadreis.SetPlace(cadreis.Cadrei{Name: "work", Path: store + "/work"}, "app", store+"/thesis")
	out = must(t, "uninstall", "--all", "--confirm", deletePhrase)
	if !strings.Contains(flat(out), "Uninstalled. Nothing of cadrei is left here but the program.") || strings.Contains(out, "delete the crew thesis") {
		t.Errorf("linked: %s", out)
	}
	if _, err := os.Lstat(home + "/.cadrei"); err == nil {
		t.Error("the link is still there")
	}
	for _, kept := range []string{store + "/mine.txt", store + "/thesis/chapter1.tex"} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("the linked folder lost %s, which isn't cadrei's", kept)
		}
	}
	for _, own := range []string{store + "/config", store + "/framework"} {
		if _, err := os.Lstat(own); err == nil {
			t.Errorf("cadrei's %s stayed in the linked folder", own)
		}
	}
	for _, crew := range []string{"work", "life"} {
		if _, err := os.Stat(store + "/" + crew); err == nil {
			t.Errorf("the crew %s is still there", crew)
		}
	}
}

// Review of fix round 6 (uninstall --all, Low): what couldn't be deleted
// isn't reported as deleted, and "your archives" is said only of some.
func TestUninstallAllSaysWhatItCouldntDelete(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	stuck := home + "/.cadrei/work/teams/dev/stuck"
	os.MkdirAll(stuck, 0o755)
	os.WriteFile(stuck+"/f", []byte("x\n"), 0o644)
	os.Chmod(stuck, 0o500)
	t.Cleanup(func() { os.Chmod(stuck, 0o755) })
	code, out, errOut := call("uninstall", "--all", "--confirm", deletePhrase)
	all := flat(out + errOut)
	if code != 2 || !strings.Contains(all, "Couldn't delete the crew work") || !strings.Contains(all, "Uninstalled, but not all of cadrei could be deleted") ||
		strings.Contains(all, "Nothing of cadrei is left") || strings.Contains(all, "Deleted work") || strings.Contains(all, "your archives") {
		t.Errorf("exit %d\n%s", code, all)
	}
}

// Review of fix round 7 (should-fix B): an orchestrator opened while the
// user types stops --all, and each crew's places stay locked from the
// last check to the deletes, so no project is linked in meanwhile.
func TestUninstallAllHoldsWhatItChecked(t *testing.T) {
	home := sandbox(t)
	allSetup(t, home)
	ttyForTests = true
	t.Cleanup(func() { ttyForTests = false })
	// A live process this command doesn't run under.
	other := exec.Command("sleep", "60")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Process.Kill(); other.Wait() })
	rt, _ := runtime.Get(runtimeName())
	lock := orchestrator.LockPath(rt.BuildDir(home + "/.cadrei/life"))
	in := &typing{text: deletePhrase + "\n", meanwhile: func() {
		if _, err := orchestrator.WriteLock(lock, other.Process.Pid, orchestrator.Terminal, "", ""); err != nil {
			t.Fatal(err)
		}
	}}
	var out, errOut strings.Builder
	if code := run([]string{"uninstall", "--all"}, in, &out, &errOut); code != 2 || !strings.Contains(flat(errOut.String()), "an orchestrator opened in another terminal while you read the plan") {
		t.Errorf("an orchestrator opened meanwhile: exit %d\n%s%s", code, out.String(), errOut.String())
	}
	os.Remove(lock)
	if _, err := os.Stat(home + "/.cadrei/life"); err != nil {
		t.Fatal("it deleted with an orchestrator open")
	}

	// From the last check to the deletes, nothing else can change places.
	locked := 0
	beforeDeletesForTests = func(list []cadreis.Cadrei) {
		for _, c := range list {
			l, err := fsx.Acquire(filepath.Join(cadreis.LocalDir(c.Path), ".lock"), 50*time.Millisecond)
			if errors.Is(err, fsx.ErrBusy) {
				locked++
			} else if err == nil {
				l.Release()
			}
		}
	}
	t.Cleanup(func() { beforeDeletesForTests = nil })
	must(t, "uninstall", "--all", "--confirm", deletePhrase)
	if locked != 2 {
		t.Errorf("%d of 2 crews' places were locked before the deletes", locked)
	}
}
