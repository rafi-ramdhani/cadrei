//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/rafi-ramdhani/cadrei/internal/backup"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

func TestUninstall(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "work")
	must(t, "--check")                  // the framework is written
	must(t, "up", "dev/engineer")       // a member runs
	must(t, "project", "dir", "~/Code") // config holds a projects folder
	c := home + "/.cadrei/work"
	os.MkdirAll(home+"/Code/app/.git", 0o755)
	// The orchestrator hook runs this binary (through a link named cadrei,
	// as an installed binary is); another hook and another cadrei's hook
	// stay.
	os.MkdirAll(home+"/inst", 0o755)
	os.Symlink(framework.Binary(), home+"/inst/cadrei")
	settings := home + "/.claude/settings.json"
	os.WriteFile(settings, []byte(`{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "`+home+`/inst/cadrei hook orchestrator"}]}, {"hooks": [{"type": "command", "command": "/other/cadrei hook orchestrator"}]}], "PreToolUse": [{"hooks": [{"type": "command", "command": "mine"}]}]}}`), 0o644)
	if !backup.Installed(c) {
		t.Fatal("init did not install the pre-push check")
	}

	t.Setenv("CADREI_MEMBER", "work-dev-engineer")
	refused(t, "team members can't", "uninstall", "--yes")
	os.Unsetenv("CADREI_MEMBER")

	out := must(t, "uninstall", "--dry-run")
	for _, want := range []string{"- stop your running teams: dev", "- remove cadrei's instructions link (~/.claude/skills/cadrei)",
		"- remove the every-chat hook from ~/.claude/settings.json (your other hooks stay)", "- remove cadrei's pre-push check from work",
		"- remove ~/.cadrei/config", "- remove ~/.cadrei/framework", "It keeps your crews (work, in ~/.cadrei) and every project.",
		"It keeps every-chat hooks that run another cadrei program.", "Then: delete the cadrei you built"} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}
	if code, _, errOut := call("uninstall"); code != 1 || !strings.Contains(errOut, "! Uninstalling needs a yes. Run it again with --yes.") {
		t.Errorf("without a terminal: %d %q", code, errOut)
	}
	if _, err := os.Stat(framework.Dir()); err != nil || tmuxIn(socket, "has-session", "-t", "=cadrei-work-dev") != "" {
		t.Fatal("the dry run or the refusal changed something")
	}

	out = must(t, "uninstall", "--yes")
	if !strings.Contains(out, "+ Uninstalled. Your crews and projects are still here.") {
		t.Errorf("uninstall: %q", out)
	}
	if tmuxIn(socket, "has-session", "-t", "=cadrei-work-dev") == "" {
		t.Error("the member still runs")
	}
	if _, err := os.Lstat(home + "/.claude/skills/cadrei"); err == nil {
		t.Error("the skill link is still there")
	}
	s := readFile(t, settings)
	if strings.Contains(s, home+"/inst/cadrei") || !strings.Contains(s, "/other/cadrei hook orchestrator") || !strings.Contains(s, `"mine"`) {
		t.Errorf("settings after uninstall:\n%s", s)
	}
	if backup.Installed(c) {
		t.Error("the pre-push check is still there")
	}
	for _, d := range []string{home + "/.cadrei/config", home + "/.cadrei/framework"} {
		if _, err := os.Stat(d); err == nil {
			t.Errorf("%s is still there", d)
		}
	}
	if _, err := os.Stat(c + "/members/dev/engineer.md"); err != nil {
		t.Error("the cadrei was touched")
	}
	if _, err := os.Stat(home + "/Code/app/.git"); err != nil {
		t.Error("a project was touched")
	}
	if log, _ := exec.Command("git", "-C", c, "status", "--porcelain").Output(); len(log) != 0 {
		t.Errorf("the cadrei's repository changed: %s", log)
	}
}

// A skill link that points at another cadrei is kept.
func TestUninstallKeepsWhatIsNotItsOwn(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	os.Remove(home + "/.claude/skills/cadrei")
	os.Symlink("/elsewhere/skills/cadrei", home+"/.claude/skills/cadrei")
	out := must(t, "uninstall", "--yes")
	if !strings.Contains(out, "It keeps ~/.claude/skills/cadrei, which points at another cadrei program.") {
		t.Errorf("plan: %q", out)
	}
	if to, _ := os.Readlink(home + "/.claude/skills/cadrei"); to != "/elsewhere/skills/cadrei" {
		t.Error("another cadrei's skill link was removed")
	}
}

// Sessions started by cadre 0.1.x are the user's, not this cadrei's: they
// are named and left running. A config folder that is a link is removed
// as a link, and the plan says its folder stays.
func TestUninstallLeavesLegacySessionsAndLinkedFolders(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "work")
	tmuxIn(socket, "new-session", "-d", "-s", "cadre-dev", "-n", "engineer", "sleep", "300")
	os.Rename(home+"/.cadrei/config", home+"/realconfig")
	os.Symlink(home+"/realconfig", home+"/.cadrei/config")
	out := must(t, "uninstall", "--dry-run")
	for _, want := range []string{"It leaves cadre 0.1.x's teams running: cadre-dev.",
		"remove the link ~/.cadrei/config (its folder ~/realconfig stays)"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "stop your running teams") {
		t.Errorf("the plan stops the 0.1.x session:\n%s", out)
	}
	out = must(t, "uninstall", "--yes")
	if !strings.Contains(out, "+ Removed the link ~/.cadrei/config") {
		t.Errorf("uninstall: %q", out)
	}
	if tmuxIn(socket, "has-session", "-t", "=cadre-dev:") != "" {
		t.Error("the 0.1.x session was stopped")
	}
	if _, err := os.Lstat(home + "/.cadrei/config"); err == nil {
		t.Error("the link is still there")
	}
	if _, err := os.Stat(home + "/realconfig/default"); err != nil {
		t.Error("the linked folder was removed")
	}
}

// 8.3.5: stop, archive and uninstall act only on sessions this machine's
// crews recorded as theirs, never on another setup's on the same server.
func TestOtherSetupsSessionsStay(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "work")
	must(t, "up", "dev")
	foreign := map[string]string{"cadrei-work-research": "", "cadrei-other": "orchestrator"}
	for name, role := range foreign {
		tmuxIn(socket, "new-session", "-d", "-s", name, "-n", "engineer", "sleep", "300")
		tmuxIn(socket, "set-option", "-t", "="+name+":", "@cadrei_home", "/elsewhere/.cadrei/work")
		tmuxIn(socket, "set-option", "-t", "="+name+":", "@cadrei_team", "research")
		if role != "" {
			tmuxIn(socket, "set-option", "-t", "="+name+":", "@cadrei_role", role)
		}
	}
	alive := func(when string) {
		t.Helper()
		for name := range foreign {
			if tmuxIn(socket, "has-session", "-t", "="+name+":") != "" {
				t.Errorf("%s: %s was stopped", when, name)
			}
		}
	}
	out := must(t, "stop", "--all", "--yes")
	if strings.Contains(out, "research") || strings.Contains(out, "elsewhere") || tmuxIn(socket, "has-session", "-t", "=cadrei-work-dev:") == "" {
		t.Errorf("stop --all:\n%s", out)
	}
	alive("stop --all")
	must(t, "up", "dev")
	// The plan doesn't stop them, and names the team it leaves running.
	if out := flat(must(t, "uninstall", "--dry-run")); strings.Contains(out, "stop your running teams: dev, research") ||
		!strings.Contains(out, "It leaves another setup's teams running: research in the crew work.") || strings.Contains(out, "cadrei-other") {
		t.Errorf("uninstall plan:\n%s", out)
	}
	if out := must(t, "uninstall", "--archive", "--yes"); strings.Contains(out, "research") || strings.Contains(out, "cadrei-other") {
		t.Errorf("archive:\n%s", out)
	}
	alive("uninstall --archive")
	if tmuxIn(socket, "has-session", "-t", "=cadrei-work-dev:") == "" {
		t.Error("the crew's own team still runs")
	}
}

// Review round 3, item 5: with ~/.cadrei/config a link and old notes in
// it, uninstall removes only the link; nothing in its folder is touched.
func TestUninstallLeavesALinkedConfigsFolderWhole(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "main")
	kept := home + "/dotfiles/cadrei-config"
	os.MkdirAll(home+"/dotfiles", 0o755)
	os.Rename(home+"/.cadrei/config", kept)
	os.Symlink(kept, home+"/.cadrei/config")
	os.MkdirAll(kept+"/.git", 0o755)
	os.WriteFile(kept+"/NOTES.md", []byte("mine\n"), 0o644)
	os.MkdirAll(kept+"/places", 0o700)
	os.WriteFile(kept+"/places/oldcrew.json", []byte("{}\n"), 0o600)
	before, _ := os.ReadDir(kept)
	out := must(t, "uninstall", "--yes")
	if !strings.Contains(flat(out), "Removed the link ~/.cadrei/config") {
		t.Errorf("uninstall:\n%s", out)
	}
	if _, err := os.Lstat(home + "/.cadrei/config"); err == nil {
		t.Error("the link is still there")
	}
	after, _ := os.ReadDir(kept)
	if len(after) != len(before) || readFile(t, kept+"/NOTES.md") != "mine\n" {
		t.Errorf("the folder changed: %d entries, then %d", len(before), len(after))
	}
}

// Review round 3, N7: a crew outside ~/.cadrei that CADREI_HOME names is
// this setup's, so stop --all and uninstall see its teams.
func TestACadreiHomeCrewsTeamsAreSeen(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "gamma")
	os.MkdirAll(home+"/work", 0o755)
	os.Rename(home+"/.cadrei/gamma", home+"/work/gamma")
	t.Setenv("CADREI_HOME", home+"/work/gamma")
	must(t, "up", "dev")
	if out := must(t, "uninstall", "--dry-run"); !strings.Contains(out, "stop your running teams: dev") {
		t.Errorf("uninstall plan:\n%s", out)
	}
	if out := must(t, "stop", "--all", "--yes"); !strings.Contains(out, "Stopped everything in all your crews") {
		t.Errorf("stop --all:\n%s", out)
	}
	if tmuxIn(socket, "has-session", "-t", "=cadrei-gamma-dev:") == "" {
		t.Error("its team still runs")
	}
}

// Review of fix round 4, item 3: an uninstall run from a shell inside one
// of the crew's team sessions stops that session too, last, with and
// without --archive.
func TestUninstallStopsTheSessionItRunsIn(t *testing.T) {
	for _, args := range [][]string{{"uninstall", "--yes"}, {"uninstall", "--archive", "--yes"}} {
		home := sandbox(t)
		socket := withTmux(t, home)
		must(t, "init", "work")
		must(t, "up", "dev")
		pane := tmuxIn(socket, "display", "-p", "-t", "=cadrei-work-dev:", "#{pane_id}")
		t.Setenv("TMUX", session.Default().Path()+",1,0")
		t.Setenv("TMUX_PANE", pane)
		code, out, errOut := call(args...)
		t.Setenv("TMUX", "")
		t.Setenv("TMUX_PANE", "")
		if code != 0 || !strings.Contains(out+errOut, "Stopping cadrei-work-dev last, since this command runs in it.") {
			t.Errorf("%v: exit %d\n%s%s", args, code, out, errOut)
		}
		if exec.Command("tmux", "-L", socket, "has-session", "-t", "=cadrei-work-dev").Run() == nil {
			t.Errorf("%v: cadrei-work-dev still runs", args)
		}
	}
}

// A team that ended on its own after the plan doesn't stop an archive.
func TestAnArchiveGoesOnWhenATeamEndedMeanwhile(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "work")
	must(t, "up", "dev")
	real, _ := exec.LookPath("tmux")
	dir := t.TempDir()
	// The team ends just before cadrei stops it.
	writeStub(t, dir+"/tmux", "#!/bin/sh\ncase \"$*\" in *kill-session*cadrei-work-dev*) "+real+" -L "+socket+" kill-session -t =cadrei-work-dev; exit 1 ;; esac\nexec "+real+" \"$@\"\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	if code, out, errOut := call("uninstall", "--archive", "--yes"); code != 0 || strings.Contains(errOut, "Couldn't stop") {
		t.Errorf("exit %d\n%s%s", code, out, errOut)
	}
}

// Review of fix round 4 (maint): a team session that won't stop stops the
// archive before anything moves, named once.
func TestAnArchiveStopsWhenATeamWontStop(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	must(t, "up", "dev")
	real, _ := exec.LookPath("tmux")
	dir := t.TempDir()
	writeStub(t, dir+"/tmux", "#!/bin/sh\ncase \"$*\" in *kill-session*cadrei-work-dev*) exit 1 ;; esac\nexec "+real+" \"$@\"\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	code, out, errOut := call("uninstall", "--archive", "--yes")
	if code == 0 || strings.Count(strings.ToLower(errOut), "couldn't stop cadrei-work-dev") != 1 || !strings.Contains(flat(errOut), "Nothing was moved.") {
		t.Errorf("exit %d\n%s%s", code, out, errOut)
	}
	if _, err := os.Stat(home + "/.cadrei/work/members"); err != nil {
		t.Error("the crew was moved")
	}
}
