//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/backup"
	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// backedUp gives a crew a backup it's up to date with, and returns it.
func backedUp(t *testing.T, c string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), filepath.Base(c)+".git")
	exec.Command("git", "init", "-q", "--bare", bare).Run()
	for _, args := range [][]string{{"remote", "add", "origin", bare}, {"push", "-q", "--no-verify", "-u", "origin", "main"}} {
		if out, err := exec.Command("git", append([]string{"-C", c}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	return bare
}

// Acceptance 54 to 61: archive a setup, start over, and bring it back.
func TestArchiveAndRestore(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/.claude.json", []byte("{}\n"), 0o600)
	os.MkdirAll(home+"/.claude/projects/x", 0o755)
	os.WriteFile(home+"/.claude/projects/x/chat.jsonl", []byte("{}\n"), 0o600)
	for _, n := range []string{"main", "work", "life", "solo"} {
		must(t, "init", n)
	}
	must(t, "use", "main")
	root := home + "/.cadrei"
	os.MkdirAll(home+"/src/app", 0o755)
	exec.Command("git", "-C", home+"/src/app", "init", "-q").Run()
	must(t, "project", "add", "app", "--path", home+"/src/app", "--no-trust")
	must(t, "tmux", "on")
	m0, _ := cadreis.Find("main")
	const chat = "11111111-2222-3333-4444-555555555555"
	cadreis.SetOrchestratorID(m0, chat)
	rt, _ := runtime.Get(runtimeName())
	session.WriteRecord(session.RecordPath(rt.BuildDir(m0.Path), "main-orchestrator"), session.Record{ID: chat, Dir: m0.Path, Since: time.Now()})
	chats := home + "/.claude/projects/" + regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(m0.Path, "-")
	os.MkdirAll(chats, 0o755)
	raw, _ := os.ReadFile(filepath.Join(fixtures, "answered-seed.jsonl"))
	os.WriteFile(chats+"/"+chat+".jsonl", raw, 0o600)
	os.WriteFile(root+"/config/team-line-hint", nil, 0o644)
	backedUp(t, root+"/main")
	backedUp(t, root+"/work")
	os.WriteFile(root+"/work/notes.md", []byte("x\n"), 0o644)
	exec.Command("git", "-C", root+"/work", "add", "notes.md").Run()
	exec.Command("git", "-C", root+"/work", "commit", "-qm", "notes").Run()
	backedUp(t, root+"/life")
	os.WriteFile(root+"/life/notes.md", []byte("x\n"), 0o644)
	claudeBefore := readFile(t, home+"/.claude.json")

	// The plan, with every kind of backup line, and nothing changed.
	out := must(t, "uninstall", "--archive", "--dry-run")
	for _, want := range []string{"cadrei uninstall --archive will:", "+ main is backed up to ", ", and up to date",
		"! work has 1 commit that isn't on ", "! life has changes that aren't committed yet.", "! solo has no backup. After this, it's only in the archive.",
		"Bring it all back later with cadrei restore, or press o on cadrei's first run."} {
		if !strings.Contains(flat(out), flat(want)) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(home + "/.cadrei-archive"); err == nil {
		t.Error("the dry run made an archive")
	}
	// Refused from inside an orchestrator.
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	refused(t, "run this in a terminal, not from your orchestrator.", "uninstall", "--archive", "--yes")
	t.Setenv("CADREI_ORCHESTRATOR", "")

	out = must(t, "uninstall", "--archive", "--yes")
	if !strings.Contains(out, "! work's 1 commit wasn't pushed (--yes doesn't push).") || !strings.Contains(out, "+ Removed ~/.cadrei") || !strings.Contains(out, "To bring it back: cadrei restore") {
		t.Errorf("archive:\n%s", out)
	}
	if _, err := os.Stat(root); err == nil {
		t.Error("~/.cadrei is still there")
	}
	archives := cadreis.Archives()
	if len(archives) != 1 || len(archives[0].Manifest.Cadreis) != 4 || !archives[0].Finished() {
		t.Fatalf("archives %+v", archives)
	}
	a := archives[0]
	if _, err := os.Stat(a.Dir + "/cadreis/main/members"); err != nil || readFile(t, a.Dir+"/README.txt") == "" {
		t.Error("the archive's crews or README")
	}
	if readFile(t, home+"/.claude.json") != claudeBefore || readFile(t, home+"/.claude/projects/x/chat.jsonl") != "{}\n" {
		t.Error("Claude Code's files changed")
	}
	if _, err := os.Stat(home + "/src/app"); err != nil {
		t.Error("the project moved")
	}

	// A new user's first run, with the archive offered; Enter starts over.
	ttyForTests = true
	t.Cleanup(func() { ttyForTests = false })
	os.WriteFile(home+"/release", nil, 0o644)
	_, out, _ = callIn("p\n")
	if !strings.Contains(out, "You have an archived setup from ") || !strings.Contains(out, "(life, main, solo, work).") || !strings.Contains(out, "Press o to bring it back instead.") {
		t.Errorf("first run:\n%s", out)
	}
	if len(unrestored()) != 1 {
		t.Error("the archive was restored")
	}
	// With a setup here, restore refuses.
	refused(t, "you already have a crew set up here (main).", "restore")
	// Archive the trial, then bring the old one back.
	must(t, "uninstall", "--archive", "--yes")
	if n := len(cadreis.Archives()); n != 2 {
		t.Fatalf("%d archives", n)
	}
	os.Remove(home + "/release")
	stubAttach(t, home)
	// The test binary's hook isn't read back as cadrei's own, so the
	// archive saw it off: the smoke test checks that half with a real build.
	old := cadreis.Archives()[1]
	old.Manifest.Machine.EveryChatHook = "on"
	old.Save()
	hookAnywhereForTests = true
	t.Cleanup(func() { hookAnywhereForTests = false })
	code, out, errOut := callIn("2\n\n", "restore")
	if code != 0 || !strings.Contains(out, "+ Put life, main, solo and work back in ~/.cadrei") || !strings.Contains(out, "Your setup is back.") {
		t.Fatalf("restore: %d\n%s%s", code, out, errOut)
	}
	m, _ := cadreis.Find("main")
	if cadreis.Default() != "main" || cadreis.Place(m, "app") != home+"/src/app" {
		t.Error("the default or the places")
	}
	if where, _ := cadreis.OpensIn(m); where != cadreis.OpensInTmux {
		t.Error("the tmux choice")
	}
	if to, _ := os.Readlink(home + "/.claude/skills/cadrei"); to == "" {
		t.Error("the skill isn't linked")
	}
	if !strings.Contains(readFile(t, home+"/.claude/settings.json"), "hook orchestrator") || !strings.Contains(out, "+ Turned the every-chat hook back on") {
		t.Error("the every-chat hook isn't back")
	}
	if !strings.Contains(readFile(t, home+"/orch-ran"), "--resume\n"+chat+"\n") {
		t.Errorf("the orchestrator didn't resume its chat:\n%s", readFile(t, home+"/orch-ran"))
	}
	if _, err := os.Stat(root + "/config/team-line-hint"); err != nil {
		t.Error("the markers aren't back")
	}
	restored, _ := cadreis.ReadArchive(a.Dir)
	if restored.Manifest.RestoredAt == nil || !strings.Contains(readFile(t, a.Dir+"/README.txt"), "Brought back on ") {
		t.Error("the archive isn't marked as restored")
	}
	refused(t, "that setup was already brought back on", "restore", a.Dir)
	// From another home folder: the note.
	other := cadreis.Archives()[0]
	other.Manifest.Home = "/Users/old"
	other.Save()
	must(t, "uninstall", "--archive", "--yes")
	if _, out, _ := callIn("q\n", "restore", other.Dir); !strings.Contains(flat(out), "! This setup comes from another home folder (/Users/old).") {
		t.Errorf("another home:\n%s", out)
	}
}

// Acceptance 55 to 57, and T-A1: running sessions stop, behind backups are
// pushed on a yes (a failed push doesn't stop it), an open orchestrator
// blocks it, and a run that stopped halfway is finished by running it again.
func TestArchivePushesStopsAndFinishes(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	stubClaude(t, home)
	must(t, "init", "main")
	must(t, "init", "work")
	// Pushes skip git hooks here: the pre-push check would run this test
	// binary. tests/smoke.sh pushes through it.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", home+"/no-hooks")
	root := home + "/.cadrei"
	bare := backedUp(t, root+"/main")
	backedUp(t, root+"/work")
	for _, c := range []string{"main", "work"} {
		os.WriteFile(root+"/"+c+"/notes.md", []byte("x\n"), 0o644)
		exec.Command("git", "-C", root+"/"+c, "add", "notes.md").Run()
		exec.Command("git", "-C", root+"/"+c, "commit", "-qm", "notes").Run()
	}
	exec.Command("git", "-C", root+"/work", "remote", "set-url", "origin", home+"/gone.git").Run()

	// An orchestrator open in a terminal blocks it.
	rt, _ := runtime.Get(runtimeName())
	lock := orchestrator.LockPath(rt.BuildDir(root + "/main"))
	if _, err := orchestrator.WriteLock(lock, os.Getpid(), orchestrator.Terminal, "", ""); err != nil {
		t.Fatal(err)
	}
	refused(t, "your orchestrator is open in another terminal", "uninstall", "--archive", "--yes")
	os.Remove(lock)

	must(t, "up", "dev")
	if out := must(t, "uninstall", "--archive", "--dry-run"); !strings.Contains(out, "stop 2 team members") {
		t.Errorf("plan:\n%s", out)
	}
	ttyForTests = true
	t.Cleanup(func() { ttyForTests = false })
	code, out, errOut := callIn("\n\ny\n", "uninstall", "--archive")
	if code != 0 {
		t.Fatalf("archive: %d\n%s%s", code, out, errOut)
	}
	for _, want := range []string{"+ Pushed main to ", "! Couldn't push work: ", "It's still safe in the archive.", "+ Stopped 2 team members"} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("archive lacks %q:\n%s", want, out)
		}
	}
	if log, _ := exec.Command("git", "-C", bare, "log", "--format=%s").Output(); !strings.Contains(string(log), "notes\n") {
		t.Errorf("main wasn't pushed: %q", log)
	}
	if left, _ := exec.Command("tmux", "-S", socket, "list-sessions", "-F", "#{session_name}").Output(); strings.Contains(string(left), "cadrei") {
		t.Errorf("sessions left: %s", left)
	}
	first := cadreis.Archives()[0]
	if !first.Finished() || len(first.Manifest.Cadreis) != 2 {
		t.Errorf("archive %+v", first.Manifest)
	}

	// A run that stopped after moving one crew.
	ttyForTests = false
	must(t, "init", "solo")
	must(t, "init", "life")
	stopped, err := cadreis.NewArchive(cadreis.Manifest{Version: 1, ArchivedAt: first.Manifest.ArchivedAt.Add(time.Minute), Home: home,
		Cadreis: []cadreis.ArchivedCrew{{Name: "life", Path: root + "/life", Default: true}}, Steps: []string{"stopped", "unlinked"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := cadreis.Move(root+"/life", stopped.Dir+"/cadreis/life"); err != nil {
		t.Fatal(err)
	}
	stopped.Step("moved life")
	out = must(t, "uninstall", "--archive", "--yes")
	if !strings.Contains(flat(out), "! An archive stopped halfway on ") || !strings.Contains(out, "+ Moved life and solo into "+cadreis.Tilde(stopped.Dir)) {
		t.Errorf("finishing:\n%s", out)
	}
	done, _ := cadreis.ReadArchive(stopped.Dir)
	if !done.Finished() || len(cadreis.Archives()) != 2 || !done.HasCrew("solo") {
		t.Errorf("finished archive %+v", done.Manifest)
	}
	if _, err := os.Stat(stopped.Dir + "/cadreis/solo/cadrei.conf"); err != nil {
		t.Error("solo isn't in the archive")
	}
	if _, err := os.Lstat(root); err == nil {
		t.Error("~/.cadrei is still there")
	}

	// A restore that stopped after putting one crew back: run again, it
	// finishes.
	done.Step("restoring")
	if err := cadreis.Move(done.Dir+"/cadreis/life", root+"/life"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(home+"/release", nil, 0o644)
	code, out, errOut = callIn("\n", "restore", done.Dir)
	if code != 0 || !strings.Contains(out, "+ Put life and solo back in ~/.cadrei") {
		t.Fatalf("finishing the restore: %d\n%s%s", code, out, errOut)
	}
	for _, c := range []string{"life", "solo"} {
		if _, err := os.Stat(root + "/" + c + "/cadrei.conf"); err != nil {
			t.Errorf("%s isn't back", c)
		}
	}
}

// T-A3: a push that hangs (a credential prompt, a slow check) is stopped at
// the limit, with everything it started.
func TestAHungPushIsStopped(t *testing.T) {
	home := sandbox(t)
	repo := home + "/repo"
	exec.Command("git", "init", "-q", repo).Run()
	exec.Command("git", "-C", repo, "commit", "-q", "--allow-empty", "-m", "x").Run()
	bare := home + "/b.git"
	exec.Command("git", "init", "-q", "--bare", bare).Run()
	exec.Command("git", "-C", repo, "remote", "add", "origin", bare).Run()
	exec.Command("git", "-C", repo, "branch", "-q", "--set-upstream-to", "origin/main").Run()
	exec.Command("git", "-C", repo, "config", "push.default", "current").Run()
	writeStub(t, repo+"/.git/hooks/pre-push", "#!/bin/sh\nsleep 30 &\nsleep 30\n")
	start := time.Now()
	_, err := pushWithin(repo, time.Second)
	if err == nil || err.Error() != "it took longer than 1 seconds" {
		t.Errorf("err %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %s", took)
	}
}

// cadrei's own git commands in a crew run none of the crew's hooks or its
// fsmonitor: the backup check, the commit before archiving, and the push,
// which keeps its hooks so the pre-push check still runs (review of fix
// round 5, N1).
func TestCrewGitRunsNothingOfTheCrews(t *testing.T) {
	home := sandbox(t)
	repo := home + "/repo"
	exec.Command("git", "init", "-q", repo).Run()
	os.WriteFile(repo+"/notes.md", []byte("x\n"), 0o644)
	exec.Command("git", "-C", repo, "add", "notes.md").Run()
	exec.Command("git", "-C", repo, "commit", "-qm", "x").Run()
	bare := home + "/b.git"
	exec.Command("git", "init", "-q", "--bare", bare).Run()
	exec.Command("git", "-C", repo, "remote", "add", "origin", bare).Run()
	exec.Command("git", "-C", repo, "config", "push.default", "current").Run()
	exec.Command("git", "-C", repo, "config", "core.fsmonitor", "touch "+home+"/fsmonitor; false").Run()
	for _, h := range []string{"pre-commit", "commit-msg", "post-commit", "post-index-change"} {
		writeStub(t, repo+"/.git/hooks/"+h, "#!/bin/sh\ntouch "+home+"/"+h+"\n")
	}
	writeStub(t, repo+"/.git/hooks/pre-push", "#!/bin/sh\ntouch "+home+"/pre-push\n")
	os.WriteFile(repo+"/notes.md", []byte("y\n"), 0o644)

	if b := checkBackup(cadreis.Cadrei{Name: "repo", Path: repo}); !b.uncommitted {
		t.Errorf("the change isn't seen: %+v", b)
	}
	if out, err := commitChanges(repo); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	if out, _ := exec.Command("git", "-C", repo, "-c", "core.fsmonitor=false", "log", "-1", "--format=%s").Output(); string(out) != "Changes before archiving\n" {
		t.Errorf("last commit %q", out)
	}
	if out, err := pushWithin(repo, 20*time.Second); err != nil {
		t.Fatalf("push: %v %s", err, out)
	}
	for _, f := range []string{"fsmonitor", "pre-commit", "commit-msg", "post-commit", "post-index-change"} {
		if _, err := os.Stat(home + "/" + f); err == nil {
			t.Errorf("the crew's %s ran", f)
		}
	}
	if _, err := os.Stat(home + "/pre-push"); err != nil {
		t.Error("the pre-push hook didn't run")
	}
}

// Review 1, end to end: a copied archive whose manifest names a crew
// outside ~/.cadrei is refused, and nothing lands outside.
func TestRestoreRefusesANameThatClimbsOut(t *testing.T) {
	home := sandbox(t)
	arch := home + "/dl/arch"
	os.MkdirAll(arch+"/Library/LaunchAgents", 0o755)
	os.WriteFile(arch+"/Library/LaunchAgents/x.plist", []byte("x\n"), 0o644)
	a := cadreis.Archive{Dir: arch, Manifest: cadreis.Manifest{Version: 1, Home: home, Steps: []string{cadreis.StepDone},
		Cadreis: []cadreis.ArchivedCrew{{Name: "../Library/LaunchAgents", Default: true}}}}
	a.Save()
	refused(t, "names a crew \"../Library/LaunchAgents\" that cadrei can't restore", "restore", arch)
	if _, err := os.Stat(home + "/Library/LaunchAgents/x.plist"); err == nil {
		t.Error("the file was put outside ~/.cadrei")
	}
}

// Review 3: a restore never deletes a live config. One from before the
// restore is kept aside; a re-run of a stopped restore keeps the whole copy.
func TestRestoreKeepsTheLiveConfig(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/release", nil, 0o644)
	must(t, "init", "main")
	must(t, "project", "dir", "~/Code")
	must(t, "uninstall", "--archive", "--yes")
	a := cadreis.Archives()[0]
	// A config that isn't the archive's: kept aside.
	os.MkdirAll(home+"/.cadrei/config", 0o700)
	os.WriteFile(home+"/.cadrei/config/default", []byte("trial\n"), 0o600)
	if code, out, errOut := callIn("\n", "restore"); code != 0 || !strings.Contains(flat(out), "settings from before are kept in ~/.cadrei/config.before-restore-") {
		t.Fatalf("restore: %d\n%s%s", code, out, errOut)
	}
	asides, _ := filepath.Glob(home + "/.cadrei/config.before-restore-*")
	if len(asides) != 1 || readFile(t, asides[0]+"/default") != "trial\n" || strings.TrimSpace(readFile(t, home+"/.cadrei/config/projects-dir")) != "~/Code" {
		t.Errorf("asides %v", asides)
	}
	// A re-run after a stop partway through the config's move: the live
	// copy is whole, the archive's partial. Nothing is lost.
	must(t, "uninstall", "--archive", "--yes")
	b := cadreis.Archives()[0]
	if b.Dir == a.Dir {
		t.Fatal("no second archive")
	}
	cadreis.Move(b.Dir+"/cadreis/main", home+"/.cadrei/main")
	os.MkdirAll(home+"/.cadrei/config", 0o700)
	for _, f := range []string{"default", "projects-dir"} {
		raw, _ := os.ReadFile(b.Dir + "/config/" + f)
		os.WriteFile(home+"/.cadrei/config/"+f, raw, 0o600)
	}
	os.Remove(b.Dir + "/config/projects-dir")
	b.Step(stepRestoring)
	b.Step(stepConfig)
	callIn("\n", "restore", b.Dir)
	if strings.TrimSpace(readFile(t, home+"/.cadrei/config/projects-dir")) != "~/Code" || strings.TrimSpace(readFile(t, home+"/.cadrei/config/default")) != "main" {
		t.Error("the live config lost files")
	}
}

// Review 8 (data safety #2): a linked ~/.cadrei comes back as the same
// link, so every crew keeps its physical path; with its disk gone, the
// restore stops and says so.
func TestALinkedCadreiFolderComesBackLinked(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/release", nil, 0o644)
	disk := home + "/disk/cadrei"
	os.MkdirAll(disk, 0o755)
	os.RemoveAll(home + "/.cadrei")
	if err := os.Symlink(disk, home+"/.cadrei"); err != nil {
		t.Fatal(err)
	}
	must(t, "init", "main")
	before := paths.Real(home + "/.cadrei/main")
	must(t, "uninstall", "--archive", "--yes")
	a := cadreis.Archives()[0]
	if a.Manifest.RootLink != disk {
		t.Fatalf("root link %q", a.Manifest.RootLink)
	}
	// The disk isn't there: nothing is restored.
	os.Rename(home+"/disk", home+"/disk-away")
	if code, _, errOut := callIn("\n", "restore"); code == 0 || !strings.Contains(flat(errOut), "and that place isn't there now") {
		t.Errorf("restore with the disk gone: %d %q", code, errOut)
	}
	if _, err := os.Lstat(home + "/.cadrei"); err == nil {
		t.Error("a ~/.cadrei was made with the disk gone")
	}
	os.Rename(home+"/disk-away", home+"/disk")
	if code, out, errOut := callIn("\n", "restore"); code != 0 {
		t.Fatalf("restore: %d\n%s%s", code, out, errOut)
	}
	if to, err := os.Readlink(home + "/.cadrei"); err != nil || to != disk {
		t.Errorf("~/.cadrei is %q, %v", to, err)
	}
	if after := paths.Real(home + "/.cadrei/main"); after != before {
		t.Errorf("the crew moved from %s to %s", before, after)
	}
}

// Audit qual-maint-2: when cadrei's instructions link can't be taken out
// (its folder can't be written), the archive stops before it moves
// anything, and says so, as plain uninstall does.
func TestTheArchiveStopsWhenALinkStays(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "main")
	dir := home + "/.claude/skills"
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	code, out, errOut := call("uninstall", "--archive", "--yes")
	if code == 0 || !strings.Contains(flat(errOut), "! Couldn't remove cadrei's instructions link") ||
		!strings.Contains(flat(errOut), "Nothing was moved. Run the same command again to finish.") {
		t.Errorf("exit %d\n%s%s", code, out, errOut)
	}
	if _, err := os.Stat(home + "/.cadrei/main/members"); err != nil {
		t.Error("the crew was moved")
	}
	// Once it can, the same command finishes.
	os.Chmod(dir, 0o755)
	if code, out, errOut := call("uninstall", "--archive", "--yes"); code != 0 {
		t.Errorf("again: %d\n%s%s", code, out, errOut)
	}
	if _, err := os.Lstat(home + "/.cadrei"); err == nil {
		t.Error("~/.cadrei is still there")
	}
}

// Audit sec-fs-03: an archive made from a linked ~/.cadrei/config can be
// brought back, with the config as the same link. A config link the
// manifest doesn't name, or in an archive from elsewhere, still isn't.
func TestALinkedConfigComesBackLinked(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/release", nil, 0o644)
	must(t, "init", "main")
	dots := home + "/dotfiles/cadrei-config"
	os.MkdirAll(home+"/dotfiles", 0o755)
	if err := os.Rename(home+"/.cadrei/config", dots); err != nil {
		t.Fatal(err)
	}
	os.Symlink(dots, home+"/.cadrei/config")
	must(t, "uninstall", "--archive", "--yes")
	if bad := cadreis.Unreadable(); len(bad) > 0 {
		t.Fatalf("unreadable: %v", bad)
	}
	a := cadreis.Archives()[0]
	if a.Manifest.ConfigLink != dots {
		t.Fatalf("config link %q", a.Manifest.ConfigLink)
	}
	// A copy elsewhere, or a link the manifest doesn't name, is refused.
	copied := home + "/dl/arch"
	os.MkdirAll(copied, 0o755)
	os.Symlink(dots, copied+"/config")
	raw, _ := os.ReadFile(a.Dir + "/manifest.json")
	os.WriteFile(copied+"/manifest.json", raw, 0o600)
	if _, err := cadreis.ReadArchive(copied); err == nil {
		t.Error("a linked config from elsewhere was read")
	}
	other := home + "/.cadrei-archive/2000-01-01-0000"
	os.MkdirAll(other, 0o755)
	os.Symlink(home+"/elsewhere", other+"/config")
	os.WriteFile(other+"/manifest.json", raw, 0o600)
	if _, err := cadreis.ReadArchive(other); err == nil {
		t.Error("a config link the manifest doesn't name was read")
	}
	os.RemoveAll(other)
	if code, out, errOut := callIn("\n", "restore"); code != 0 {
		t.Fatalf("restore: %d\n%s%s", code, out, errOut)
	}
	if to, err := os.Readlink(home + "/.cadrei/config"); err != nil || to != dots {
		t.Errorf("~/.cadrei/config is %q, %v", to, err)
	}
}

// Review 8 (data safety #9, #10): the push goes where the question says
// (origin's push URL), only tracked changes are committed, and a crew with
// no remote still records its changes.
func TestArchivePushesWhereItSays(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	must(t, "init", "main")
	must(t, "init", "solo")
	c := home + "/.cadrei/main"
	fetch := backedUp(t, c)
	push := filepath.Join(t.TempDir(), "push.git")
	exec.Command("git", "clone", "-q", "--bare", fetch, push).Run()
	exec.Command("git", "-C", c, "remote", "set-url", "--push", "origin", push).Run()
	os.WriteFile(c+"/playbook.md", []byte("changed\n"), 0o644)
	os.WriteFile(c+"/teams/scratch.txt", []byte("hello\n"), 0o644)
	os.WriteFile(home+"/.cadrei/solo/teams/wip.txt", []byte("hello\n"), 0o644)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", home+"/no-hooks")
	ttyForTests = true
	t.Cleanup(func() { ttyForTests = false })
	code, out, errOut := callIn("\ny\n", "uninstall", "--archive")
	if code != 0 || !strings.Contains(flat(out+errOut), "Commit main's changes and push to "+readable(push)+" first?") ||
		!strings.Contains(flat(out), "main has files git doesn't track yet (teams/scratch.txt)") {
		t.Fatalf("archive: %d\n%s%s", code, out, errOut)
	}
	pushed, _ := exec.Command("git", "-C", push, "log", "-1", "--name-only", "--format=%s").Output()
	if !strings.Contains(string(pushed), "Changes before archiving") || strings.Contains(string(pushed), "scratch.txt") {
		t.Errorf("pushed: %s", pushed)
	}
	if got, _ := exec.Command("git", "-C", fetch, "log", "-1", "--format=%s").Output(); strings.Contains(string(got), "Changes before archiving") {
		t.Error("pushed to the fetch URL")
	}
	for _, cr := range cadreis.Archives()[0].Manifest.Cadreis {
		if cr.Name == "solo" && !cr.Backup.HasUncommitted {
			t.Error("solo's changes weren't recorded")
		}
	}
}

// Review 8 (data safety #11): an archive whose manifest can't be read is
// named, never hidden behind "There's no archived setup".
func TestAnUnreadableArchiveIsNamed(t *testing.T) {
	home := sandbox(t)
	os.MkdirAll(home+"/.cadrei-archive/2026-10-01-1200/cadreis/main", 0o755)
	os.WriteFile(home+"/.cadrei-archive/2026-10-01-1200/manifest.json", []byte("{broken"), 0o600)
	_, out, errOut := call("restore")
	if !strings.Contains(flat(out+errOut), "holds 2026-10-01-1200, which cadrei can't read") || strings.Contains(out+errOut, "There's no archived setup") {
		t.Errorf("restore:\n%s%s", out, errOut)
	}
}

// Review of fix round 5 (sec N9): a pre-push hook in cadrei's form is
// cadrei's own only when it runs this program; one for any other program
// is named, since a copied archive can name what it likes there.
func TestAForgedPrePushHookIsNamed(t *testing.T) {
	sandbox(t)
	crew := t.TempDir()
	exec.Command("git", "init", "-q", crew).Run()
	if _, err := backup.Install(crew, crew+"/members/dev/x.sh"); err != nil {
		t.Fatal(err)
	}
	if got := gitRunners(crew); !slices.Contains(got, "hooks/pre-push") {
		t.Errorf("a hook for another program: %v", got)
	}
	os.Remove(crew + "/.git/hooks/pre-push")
	if _, err := backup.Install(crew, framework.Binary()); err != nil {
		t.Fatal(err)
	}
	if got := gitRunners(crew); len(got) != 0 {
		t.Errorf("cadrei's own hook: %v", got)
	}
}
