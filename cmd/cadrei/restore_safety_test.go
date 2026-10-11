//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/backup"
	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
)

// craftedArchive is an archive folder at ~/dl/arch holding one crew, main,
// as a copied or downloaded archive could, with nothing else made yet.
func craftedArchive(t *testing.T, home string) cadreis.Archive {
	t.Helper()
	return craftedArchiveAt(t, home, home+"/dl/arch")
}

// craftedArchiveAt is the same at arch.
func craftedArchiveAt(t *testing.T, home, arch string) cadreis.Archive {
	t.Helper()
	os.MkdirAll(arch+"/cadreis/main/members/dev", 0o755)
	os.WriteFile(arch+"/cadreis/main/members/dev/engineer.md", []byte("# Member: Engineer\n"), 0o644)
	a := cadreis.Archive{Dir: arch, Manifest: cadreis.Manifest{Version: 1, Home: home, Steps: []string{cadreis.StepDone},
		Cadreis: []cadreis.ArchivedCrew{{Name: "main", Default: true}}}}
	// One in ~/.cadrei-archive is this machine's, as uninstall makes them.
	if filepath.Dir(arch) == home+"/.cadrei-archive" {
		a.Seal()
	}
	a.Save()
	return a
}

// outside is a folder of the user's own, beside the archive, with a file.
func outside(t *testing.T, home string) string {
	t.Helper()
	dir := home + "/outside"
	os.MkdirAll(dir+"/victim", 0o755)
	os.WriteFile(dir+"/victim/thesis.txt", []byte("my only copy\n"), 0o644)
	return dir
}

// Review round 3, item 2: an archive whose cadreis, config or other
// folder is a link is refused, and the place it points at is untouched.
func TestALinkedArchiveFolderIsRefused(t *testing.T) {
	for _, sub := range cadreis.ArchiveFolders {
		t.Run(sub, func(t *testing.T) {
			home := sandbox(t)
			a := craftedArchive(t, home)
			out := outside(t, home)
			os.RemoveAll(filepath.Join(a.Dir, sub))
			os.Symlink("../../outside", filepath.Join(a.Dir, sub))
			refused(t, "its "+sub+" folder is a link or a file", "restore", a.Dir)
			if readFile(t, out+"/victim/thesis.txt") != "my only copy\n" {
				t.Error("the outside folder changed")
			}
			if _, err := os.Lstat(home + "/.cadrei/victim"); err == nil {
				t.Error("the outside folder was moved in")
			}
		})
	}
	// The whole home folder, through other.
	home := sandbox(t)
	a := craftedArchive(t, home)
	os.Symlink("../..", a.Dir+"/other")
	refused(t, "its other folder is a link", "restore", a.Dir)
	if _, err := os.Stat(home + "/dl/arch/manifest.json"); err != nil {
		t.Error("the archive moved")
	}
}

// The plan names everything else the archive puts back in ~/.cadrei.
func TestTheRestorePlanListsTheOtherThings(t *testing.T) {
	home := sandbox(t)
	stubClaude(t, home)
	a := craftedArchive(t, home)
	os.MkdirAll(a.Dir+"/other", 0o755)
	os.WriteFile(a.Dir+"/other/notes.txt", []byte("notes\n"), 0o644)
	os.WriteFile(a.Dir+"/other/todo.md", []byte("todo\n"), 0o644)
	_, out, _ := callIn("q", "restore", a.Dir)
	if !strings.Contains(flat(out), "Put back the other things that were in ~/.cadrei: notes.txt, todo.md") {
		t.Errorf("plan:\n%s", out)
	}
}

// Review round 3, item 1: root_link is followed only when it's absolute
// and clean, from an archive in ~/.cadrei-archive, to an empty place, and
// the plan says where the crews go.
func TestRootLinkIsOnlyAHint(t *testing.T) {
	restore := func(t *testing.T, home string, a cadreis.Archive, args ...string) string {
		t.Helper()
		withTmux(t, home)
		stubClaude(t, home)
		os.WriteFile(home+"/release", nil, 0o644)
		// A fresh machine: no ~/.cadrei yet.
		os.RemoveAll(home + "/.cadrei")
		_, out, errOut := callIn("\n\n\n", append([]string{"restore"}, args...)...)
		return flat(out + errOut)
	}
	t.Run("relative", func(t *testing.T) {
		home := sandbox(t)
		a := craftedArchiveAt(t, home, home+"/.cadrei-archive/2026-10-01-1200")
		os.MkdirAll(a.Dir+"/other", 0o755)
		os.WriteFile(a.Dir+"/other/evil.plist", []byte("x\n"), 0o644)
		a.Manifest.RootLink = "Library/LaunchAgents"
		a.Save()
		out := restore(t, home, a)
		if !strings.Contains(out, "Put main back in ~/.cadrei, with main") || !strings.Contains(out, "This archive says your crews were on Library/LaunchAgents.") {
			t.Errorf("plan: %s", out)
		}
		if _, err := os.Lstat(home + "/Library/LaunchAgents"); err == nil {
			t.Error("the relative place was made")
		}
		if st, err := os.Lstat(home + "/.cadrei"); err != nil || !st.IsDir() {
			t.Error("~/.cadrei isn't a folder")
		}
	})
	t.Run("handed to restore", func(t *testing.T) {
		home := sandbox(t)
		a := craftedArchive(t, home)
		a.Manifest.RootLink = home + "/disk/cadrei"
		os.MkdirAll(home+"/disk", 0o755)
		a.Save()
		if out := restore(t, home, a, a.Dir); !strings.Contains(out, "This archive says your crews were on") {
			t.Errorf("plan: %s", out)
		}
		if _, err := os.Lstat(home + "/disk/cadrei"); err == nil {
			t.Error("the place was made for an archive from outside ~/.cadrei-archive")
		}
	})
	t.Run("not empty", func(t *testing.T) {
		home := sandbox(t)
		a := craftedArchiveAt(t, home, home+"/.cadrei-archive/2026-10-01-1200")
		os.MkdirAll(home+"/Documents", 0o755)
		os.WriteFile(home+"/Documents/thesis.txt", []byte("mine\n"), 0o644)
		a.Manifest.RootLink = home + "/Documents"
		a.Save()
		restore(t, home, a)
		if st, _ := os.Lstat(home + "/.cadrei"); st == nil || st.Mode()&os.ModeSymlink != 0 {
			t.Error("~/.cadrei was linked to a folder that isn't empty")
		}
		if _, err := os.Stat(home + "/Documents/main"); err == nil {
			t.Error("a crew went into the user's folder")
		}
	})
	// Review round 4, A: never into the home folder's Library or a
	// dot-folder, a linked path part included.
	for name, place := range map[string]func(home string) string{
		"Library":    func(home string) string { return home + "/Library/LaunchAgents" },
		"dot-folder": func(home string) string { return home + "/.config/cadrei" },
		"through a link": func(home string) string {
			os.MkdirAll(home+"/Library", 0o755)
			os.Symlink(home+"/Library", home+"/lnk")
			return home + "/lnk/LaunchAgents"
		},
		"the home itself": func(home string) string { return home },
		"capital LIBRARY": func(home string) string { return home + "/LIBRARY/Agents" },
	} {
		t.Run(name, func(t *testing.T) {
			home := sandbox(t)
			a := craftedArchiveAt(t, home, home+"/.cadrei-archive/2026-10-01-1200")
			target := place(home)
			a.Manifest.RootLink = target
			a.Save()
			if out := restore(t, home, a); !strings.Contains(out, "This archive says your crews were on") {
				t.Errorf("plan: %s", out)
			}
			if st, err := os.Lstat(home + "/.cadrei"); err != nil || st.Mode()&os.ModeSymlink != 0 {
				t.Error("~/.cadrei was linked")
			}
			if _, err := os.Stat(filepath.Join(target, "main")); err == nil {
				t.Error("a crew went there")
			}
		})
	}
	t.Run("trusted", func(t *testing.T) {
		home := sandbox(t)
		a := craftedArchiveAt(t, home, home+"/.cadrei-archive/2026-10-01-1200")
		os.MkdirAll(home+"/disk", 0o755)
		a.Manifest.RootLink = home + "/disk/cadrei"
		a.Save()
		if out := restore(t, home, a); !strings.Contains(out, "Put main back in ~/disk/cadrei, linked from ~/.cadrei as before") {
			t.Errorf("plan: %s", out)
		}
		if target, err := os.Readlink(home + "/.cadrei"); err != nil || target != home+"/disk/cadrei" {
			t.Errorf("link: %q %v", target, err)
		}
	})
}

// Review round 3, N9: a link left at ~/.cadrei, to a disk that isn't
// connected, gets the same refusal, and no folder at its mount point.
func TestADanglingCadreiLinkIsRefused(t *testing.T) {
	home := sandbox(t)
	stubClaude(t, home)
	a := craftedArchiveAt(t, home, home+"/.cadrei-archive/2026-10-01-1200")
	os.RemoveAll(home + "/.cadrei")
	os.Symlink(home+"/mnt-unplugged/disk/cadrei", home+"/.cadrei")
	_, out, errOut := callIn("\n", "restore", a.Dir)
	if !strings.Contains(flat(out+errOut), "Connect the disk it's on") {
		t.Errorf("restore: %s%s", out, errOut)
	}
	if _, err := os.Lstat(home + "/mnt-unplugged"); err == nil {
		t.Error("the mount point was made")
	}
}

// Review round 3, item 4: a restore needs ~/.cadrei empty or missing, its
// own files aside (7.8.3), so a user's main.partial or notes.txt.partial
// is never touched.
func TestARestoreNeedsAnEmptyCadreiFolder(t *testing.T) {
	home := sandbox(t)
	stubClaude(t, home)
	a := craftedArchiveAt(t, home, home+"/.cadrei-archive/2026-10-01-1200")
	os.MkdirAll(home+"/.cadrei/main.partial", 0o755)
	os.WriteFile(home+"/.cadrei/main.partial/thesis.txt", []byte("my only copy\n"), 0o644)
	os.WriteFile(home+"/.cadrei/notes.txt.partial", []byte("user draft\n"), 0o644)
	refused(t, "~/.cadrei already holds main.partial and notes.txt.partial, which isn't part of this setup.", "restore", a.Dir)
	if readFile(t, home+"/.cadrei/main.partial/thesis.txt") != "my only copy\n" || readFile(t, home+"/.cadrei/notes.txt.partial") != "user draft\n" {
		t.Error("the user's files changed")
	}
	if _, err := os.Stat(a.Dir + "/cadreis/main"); err != nil {
		t.Error("the archive's crew moved")
	}
}

// Review round 3, N6: a restore that stopped before its config step, run
// again, still keeps the config that was there aside, and finishes.
func TestARestoreStoppedBeforeItsConfigFinishes(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/release", nil, 0o644)
	must(t, "init", "main")
	must(t, "uninstall", "--archive", "--yes")
	os.MkdirAll(home+"/.cadrei/config", 0o700)
	os.WriteFile(home+"/.cadrei/config/default", []byte("trial\n"), 0o600)
	os.Chmod(home+"/.cadrei", 0o555)
	if code, _, _ := callIn("\n", "restore"); code == 0 {
		t.Fatal("the first restore went through a read-only ~/.cadrei")
	}
	os.Chmod(home+"/.cadrei", 0o755)
	if code, out, errOut := callIn("\n", "restore"); code != 0 || !strings.Contains(flat(out), "settings from before are kept in ~/.cadrei/config.before-restore-") {
		t.Fatalf("again: %d\n%s%s", code, out, errOut)
	}
	asides, _ := filepath.Glob(home + "/.cadrei/config.before-restore-*")
	if len(asides) != 1 || readFile(t, asides[0]+"/default") != "trial\n" {
		t.Errorf("asides %v", asides)
	}
}

// Review round 3, N10: a crew that comes back as a link isn't one cadrei
// sees, so the archive isn't marked as brought back.
func TestALinkedCrewLeavesTheArchiveOpen(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/release", nil, 0o644)
	a := craftedArchiveAt(t, home, home+"/.cadrei-archive/2026-10-01-1200")
	out := outside(t, home)
	os.RemoveAll(a.Dir + "/cadreis/main")
	os.Symlink(out+"/victim", a.Dir+"/cadreis/main")
	_, stdout, _ := callIn("\n\n", "restore")
	if !strings.Contains(flat(stdout), "main came back to ~/.cadrei, but cadrei doesn't see it as a crew") ||
		strings.Contains(stdout, "Your setup is back") || !strings.Contains(stdout, "The rest of your setup is back.") {
		t.Errorf("restore:\n%s", stdout)
	}
	if b, err := cadreis.ReadArchive(a.Dir); err != nil || b.Manifest.RestoredAt != nil {
		t.Errorf("the archive was marked as brought back: %v", err)
	}
	if readFile(t, out+"/victim/thesis.txt") != "my only copy\n" {
		t.Error("the link's folder changed")
	}
}

// Review round 3, data safety 11: the first run names an archive cadrei
// can't read.
func TestTheFirstRunNamesAnUnreadableArchive(t *testing.T) {
	home := firstMachine(t)
	inRepo(t, home)
	os.MkdirAll(home+"/.cadrei-archive/2026-10-01-1200/cadreis/main", 0o700)
	os.WriteFile(home+"/.cadrei-archive/2026-10-01-1200/manifest.json", []byte("{broken"), 0o600)
	_, out, _ := callIn("q")
	if !strings.Contains(flat(out), "~/.cadrei-archive holds 2026-10-01-1200, which cadrei can't read or restore from") {
		t.Errorf("first run:\n%s", out)
	}
}

// Review round 3, item 9: model and effort keys a restored cadrei.conf
// sets are named, in the archive restore's plan and after a GitHub
// restore's copy.
func TestARestoreNamesItsModelSettings(t *testing.T) {
	home := sandbox(t)
	a := craftedArchive(t, home)
	os.WriteFile(a.Dir+"/cadreis/main/cadrei.conf", []byte("MODEL=opus\nORCHESTRATOR_EFFORT=max\n"), 0o644)
	_, out, _ := callIn("q", "restore", a.Dir)
	if !strings.Contains(flat(out), "Keep main's model and effort settings: MODEL=opus, ORCHESTRATOR_EFFORT=max") {
		t.Errorf("archive plan:\n%s", out)
	}

	home = firstMachine(t)
	claudeConfig(t, home)
	app := bareRepo(t, "app")
	backup := backupOf(t, "work", app, func(src string) {
		raw, _ := os.ReadFile(src + "/cadrei.conf")
		os.WriteFile(src+"/cadrei.conf", append(raw, []byte("\nEFFORT=high\n")...), 0o644)
	})
	_, out, _ = callIn("r\n" + backup + "\np\n\n")
	if !strings.Contains(flat(out), "Its cadrei.conf sets the model and effort your team and orchestrator start with: EFFORT=high.") {
		t.Errorf("GitHub restore:\n%s", out)
	}
}

// Review round 4, B: an archived crew with a link where cadrei reads files
// or runs sessions (here its teams folder, pointing outside) is refused,
// as a GitHub restore refuses one, and nothing moves.
func TestAnArchivedCrewWithLinksIsRefused(t *testing.T) {
	for _, place := range []string{"teams", "members/dev", "playbook.md", ".claude/local"} {
		t.Run(place, func(t *testing.T) {
			home := sandbox(t)
			a := craftedArchive(t, home)
			out := outside(t, home)
			link := filepath.Join(a.Dir, "cadreis", "main", place)
			os.RemoveAll(link)
			os.MkdirAll(filepath.Dir(link), 0o755)
			os.Symlink(out+"/victim", link)
			refused(t, "the archive's crew main has links where cadrei reads or runs files ("+place+"), so nothing was restored.", "restore", a.Dir)
			if _, err := os.Lstat(home + "/.cadrei/main"); err == nil {
				t.Error("the crew was moved")
			}
		})
	}
	// Links deeper in a team's work are its own, as in a project.
	home := sandbox(t)
	stubClaude(t, home)
	a := craftedArchive(t, home)
	os.MkdirAll(a.Dir+"/cadreis/main/teams/dev/node_modules/.bin", 0o755)
	os.Symlink("../tool/cli.js", a.Dir+"/cadreis/main/teams/dev/node_modules/.bin/tool")
	if _, out, errOut := callIn("q", "restore", a.Dir); strings.Contains(out+errOut, "has links") {
		t.Errorf("a link in the work was refused: %s%s", out, errOut)
	}
}

// Audit sec-fs-01: an archive from elsewhere runs nothing from its crews'
// git settings before the user agrees, and the plan names what they would
// run once it's back.
func TestAnArchivesGitSettingsDontRunBeforeEnter(t *testing.T) {
	home := sandbox(t)
	stubClaude(t, home)
	a := craftedArchive(t, home)
	crew := a.Dir + "/cadreis/main"
	exec.Command("git", "-C", crew, "init", "-q").Run()
	ran := home + "/ran-before-enter"
	exec.Command("git", "-C", crew, "config", "core.fsmonitor", "touch "+ran+"; false").Run()
	exec.Command("git", "-C", crew, "-c", "core.fsmonitor=false", "add", "-A").Run()
	writeStub(t, crew+"/.git/hooks/pre-commit", "#!/bin/sh\ntouch "+ran+"\n")
	_, out, _ := callIn("q", "restore", a.Dir)
	if _, err := os.Stat(ran); err == nil {
		t.Error("the archive's git settings ran before Enter")
	}
	if !strings.Contains(flat(out), "! main's git settings could run programs (core.fsmonitor, hooks/pre-commit). Git would run them whenever it's used in main, by cadrei too.") ||
		!strings.Contains(out, "Nothing was changed.") {
		t.Errorf("plan:\n%s", out)
	}
	// cadrei's own pre-push hook for this program isn't named: cadrei
	// writes it again. One in its form for another program is (review of
	// fix round 5, sec N9).
	os.Remove(crew + "/.git/hooks/pre-commit")
	exec.Command("git", "-C", crew, "config", "--unset", "core.fsmonitor").Run()
	if _, err := backup.Install(crew, "/elsewhere/cadrei"); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := callIn("q", "restore", a.Dir); !strings.Contains(flat(out), "main's git settings could run programs (hooks/pre-push)") {
		t.Errorf("a hook for another program wasn't named:\n%s", out)
	}
	os.Remove(crew + "/.git/hooks/pre-push")
	if _, err := backup.Install(crew, framework.Binary()); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := callIn("q", "restore", a.Dir); strings.Contains(out, "git settings could run programs") {
		t.Errorf("cadrei's own hook was named:\n%s", out)
	}
	// A .git that is a file, sending git to settings elsewhere, is named.
	os.Rename(crew+"/.git", a.Dir+"/real-git")
	os.WriteFile(crew+"/.git", []byte("gitdir: ../../real-git\n"), 0o644)
	if _, out, _ := callIn("q", "restore", a.Dir); !strings.Contains(flat(out), "main's git settings could run programs (.git (a file that sends git elsewhere))") {
		t.Errorf("a .git file:\n%s", out)
	}
}

// Audit sec-fs-02: nothing in an archive's other folder comes back as a
// crew that skipped the crew checks, as cadrei's config or framework, or,
// from an archive made elsewhere, as a link.
func TestAnArchivesOtherFolderIsChecked(t *testing.T) {
	for name, setup := range map[string]func(other string){
		"a crew with links": func(other string) {
			os.MkdirAll(other+"/evil/members", 0o755)
			os.Symlink("../../../outside", other+"/evil/teams")
		},
		"a crew whose members are a link": func(other string) {
			os.MkdirAll(other+"/evil", 0o755)
			os.Symlink("../../../outside", other+"/evil/members")
		},
		"a linked config": func(other string) { os.Symlink("../../../outside", other+"/config") },
		"a config folder": func(other string) { os.MkdirAll(other+"/Config", 0o755) },
		"a framework":     func(other string) { os.MkdirAll(other+"/framework/skills", 0o755) },
		"a link":          func(other string) { os.Symlink("../../../outside", other+"/notes") },
	} {
		t.Run(name, func(t *testing.T) {
			home := sandbox(t)
			stubClaude(t, home)
			a := craftedArchive(t, home)
			outside(t, home)
			os.MkdirAll(a.Dir+"/other", 0o755)
			setup(a.Dir + "/other")
			code, out, errOut := callIn("\n", "restore", a.Dir)
			if code == 0 || !strings.Contains(flat(errOut), "which cadrei won't put back in ~/.cadrei, so nothing was restored.") {
				t.Errorf("exit %d\n%s%s", code, out, errOut)
			}
			if entries, _ := os.ReadDir(home + "/.cadrei"); len(entries) > 1 {
				t.Errorf("~/.cadrei holds %v", entries)
			}
		})
	}
}

// A link in other from an archive made on this machine comes back as it
// was: ~/.cadrei held it.
func TestALinkInOtherFromThisMachineComesBack(t *testing.T) {
	home := sandbox(t)
	stubClaude(t, home)
	a := craftedArchiveAt(t, home, home+"/.cadrei-archive/2026-01-02-0304")
	os.MkdirAll(home+"/notes", 0o755)
	os.MkdirAll(a.Dir+"/other", 0o755)
	os.Symlink(home+"/notes", a.Dir+"/other/notes")
	os.WriteFile(home+"/release", nil, 0o644)
	if _, out, _ := callIn("\n", "restore", a.Dir); !strings.Contains(out, "Your setup is back.") {
		t.Fatalf("restore:\n%s", out)
	}
	if target, err := os.Readlink(home + "/.cadrei/notes"); err != nil || target != home+"/notes" {
		t.Errorf("notes: %q %v", target, err)
	}
}

// Audit sec-fs-05: an archive whose uninstall stopped halfway can't be
// brought back yet. Restore says how to finish it, picked or named.
func TestAnArchiveThatStoppedHalfwaySaysHowToFinish(t *testing.T) {
	home := sandbox(t)
	a := craftedArchiveAt(t, home, home+"/.cadrei-archive/2026-01-02-0304")
	a.Manifest.Steps = []string{"stopped", "unlinked"}
	a.Save()
	// With a newer archive already brought back, restore names the one
	// that stopped halfway too (review of fix round 4).
	newer := craftedArchiveAt(t, home, home+"/.cadrei-archive/2026-01-05-0304")
	newer.Manifest.ArchivedAt = time.Date(2026, 1, 5, 3, 4, 0, 0, time.UTC)
	now := time.Now()
	newer.Manifest.RestoredAt = &now
	newer.Save()
	if _, _, errOut := call("restore"); !strings.Contains(flat(errOut), "stopped halfway. Run cadrei uninstall --archive to finish it") {
		t.Errorf("with a newer restored one: %s", errOut)
	}
	os.RemoveAll(newer.Dir)
	for _, args := range [][]string{{"restore"}, {"restore", a.Dir}} {
		code, out, errOut := call(args...)
		if code != 1 || strings.Contains(errOut, "panic") || !strings.Contains(flat(errOut), "stopped halfway, so it can't be brought back yet. Run cadrei uninstall --archive to finish it, then cadrei restore.") {
			t.Errorf("%v: exit %d\n%s%s", args, code, out, errOut)
		}
	}
}

// Audit sec-fs-07: marking an archive brought back writes its own README
// only, never a file its README links to.
func TestTheRestoredNoteDoesntFollowALink(t *testing.T) {
	home := sandbox(t)
	stubClaude(t, home)
	a := craftedArchive(t, home)
	os.WriteFile(home+"/.zshrc", []byte("# mine\n"), 0o644)
	os.Symlink("../../.zshrc", a.Dir+"/README.txt")
	os.WriteFile(home+"/release", nil, 0o644)
	if _, out, _ := callIn("\n", "restore", a.Dir); !strings.Contains(out, "Your setup is back.") {
		t.Fatalf("restore:\n%s", out)
	}
	if got := readFile(t, home+"/.zshrc"); got != "# mine\n" {
		t.Errorf(".zshrc: %q", got)
	}
	// A real README gets the note.
	home = sandbox(t)
	stubClaude(t, home)
	a = craftedArchive(t, home)
	os.WriteFile(a.Dir+"/README.txt", []byte("notes\n"), 0o600)
	os.WriteFile(home+"/release", nil, 0o644)
	callIn("\n", "restore", a.Dir)
	if got := readFile(t, a.Dir+"/README.txt"); !strings.HasPrefix(got, "notes\n\nBrought back on ") {
		t.Errorf("README: %q", got)
	}
}

// Audit, an area no one checked: a crew's notes from an archive made
// elsewhere can name any folder as a project's place. A place no project
// can be (~/.claude, ~/.cadrei) is named in the plan and left out, and up
// refuses one however it got there.
func TestAnArchivesPlacesAreChecked(t *testing.T) {
	home := sandbox(t)
	stubClaude(t, home)
	a := craftedArchive(t, home)
	local := a.Dir + "/cadreis/main/.claude/local"
	os.MkdirAll(local, 0o700)
	os.WriteFile(local+"/places.json", []byte(`{"version":1,"places":{"app":"~/code/app","skills":"~/.claude/skills","crews":"~/.cadrei"}}`), 0o600)
	os.WriteFile(a.Dir+"/cadreis/main/projects.yaml", []byte("app:\n  path: ~/code/app\nskills:\n  team: dev\n"), 0o644)
	os.WriteFile(home+"/release", nil, 0o644)
	_, out, _ := callIn("\n", "restore", a.Dir)
	if !strings.Contains(flat(out), "! main's notes put projects where no project can be: crews (~/.cadrei: it is inside ~/.cadrei, where cadrei keeps its own files), skills (~/.claude/skills: it is inside ~/.claude). Those places are left out.") {
		t.Errorf("plan:\n%s", out)
	}
	c := cadreis.Cadrei{Name: "main", Path: home + "/.cadrei/main"}
	if got := cadreis.Places(c); len(got) != 1 || got["app"] != "~/code/app" {
		t.Errorf("places: %v", got)
	}
	// Review of fix round 4, item 5: another spelling of the same folder,
	// on a disk that doesn't tell case apart, is refused too.
	if _, err := os.Stat(home + "/.CLAUDE"); err == nil {
		other := cadreis.Cadrei{Name: "other", Path: t.TempDir()}
		os.MkdirAll(cadreis.LocalDir(other.Path), 0o700)
		os.WriteFile(cadreis.PlacesFile(other), []byte(`{"version":1,"places":{"skills":"~/.CLAUDE/Skills","crews":"~/.CADREI","home":"`+strings.ToUpper(home)+`"}}`), 0o600)
		got := map[string]string{}
		for _, name := range []string{"skills", "crews", "home"} {
			got[name] = placeRefusal(cadreis.Place(other, name))
		}
		if got["skills"] == "" || got["crews"] == "" || got["home"] == "" {
			t.Errorf("other spellings: %v", got)
		}
	}
	// A place written by hand is refused when a team starts there.
	withTmux(t, home)
	os.MkdirAll(home+"/.claude/skills", 0o755)
	cadreis.SetPlace(c, "skills", home+"/.claude/skills")
	t.Setenv("CADREI_HOME", c.Path)
	refused(t, "skills's folder on this machine, ~/.claude/skills, can't hold a project: it is inside ~/.claude.", "up", "dev", "skills")
}

// Review of fix round 4 (sec N9): 0.2.0's places in an archive's config
// folder are checked as the crew's own are: named in the plan, and left
// out of what comes back.
func TestAnArchivesOldPlacesAreChecked(t *testing.T) {
	home := sandbox(t)
	stubClaude(t, home)
	a := craftedArchive(t, home)
	os.MkdirAll(a.Dir+"/config/places", 0o700)
	os.WriteFile(a.Dir+"/config/places/main.json", []byte(`{"app":"~/code/app","home":"~","skills":"~/.claude/skills"}`), 0o600)
	// Places for a crew the archive doesn't hold aren't brought back
	// (review of fix round 5, sec N5).
	os.WriteFile(a.Dir+"/config/places/ghost.json", []byte(`{"x":"~/.claude/skills"}`), 0o600)
	os.WriteFile(home+"/release", nil, 0o644)
	_, out, _ := callIn("\n", "restore", a.Dir)
	if !strings.Contains(flat(out), "main's notes put projects where no project can be: home (~: ") ||
		!strings.Contains(flat(out), ", skills (~/.claude/skills: it is inside ~/.claude).") {
		t.Errorf("plan:\n%s", out)
	}
	if _, err := os.Stat(home + "/.cadrei/config/places/ghost.json"); err == nil {
		t.Error("a crew the archive doesn't hold kept its places")
	}
	call("ls") // the next command moves 0.2.0's notes in
	c := cadreis.Cadrei{Name: "main", Path: home + "/.cadrei/main"}
	if got := cadreis.Places(c); got["skills"] != "" || got["home"] != "" || got["app"] != "~/code/app" {
		t.Errorf("places after the restore: %v", got)
	}
}

// Review of fix round 4 (S4): an archive copied into ~/.cadrei-archive
// from elsewhere isn't taken for this machine's: it carries another
// machine's id, so its crews' git settings are named and its links in
// other aren't brought back.
func TestAnArchiveCopiedInIsntThisMachines(t *testing.T) {
	home := sandbox(t)
	stubClaude(t, home)
	a := craftedArchiveAt(t, home, home+"/.cadrei-archive/2026-01-02-0304")
	if !a.MadeHere() {
		t.Fatal("this machine's archive isn't taken for its own")
	}
	a.Manifest.MadeBy = strings.Repeat("0", 32)
	a.Save()
	if a.MadeHere() {
		t.Error("another machine's archive was taken for this one's")
	}
	os.MkdirAll(home+"/notes", 0o755)
	os.MkdirAll(a.Dir+"/other", 0o755)
	os.Symlink(home+"/notes", a.Dir+"/other/notes")
	if code, _, errOut := callIn("\n", "restore", a.Dir); code == 0 || !strings.Contains(flat(errOut), "notes (a link)") {
		t.Errorf("a link from elsewhere: %d %s", code, errOut)
	}
}

// Review of fix round 4 (maint): both first-run screens offer an archive
// that waits, one or several.
func TestBothFirstRunScreensOfferArchives(t *testing.T) {
	home := firstMachine(t)
	craftedArchiveAt(t, home, home+"/.cadrei-archive/2026-01-02-0304")
	craftedArchiveAt(t, home, home+"/.cadrei-archive/2026-01-03-0304")
	if _, out, _ := callIn("q\n"); !strings.Contains(flat(out), "You have 2 archived setups. Press o to pick one.") || strings.Contains(out, "bring it back instead") {
		t.Errorf("several, new crew:\n%s", out)
	}
	home = firstMachine(t)
	craftedArchiveAt(t, home, home+"/.cadrei-archive/2026-01-02-0304")
	must(t, "init", "work")
	os.RemoveAll(home + "/.cadrei/config")
	if _, out, _ := callIn("q\n"); !strings.Contains(flat(out), "You have an archived setup from") || !strings.Contains(flat(out), "Press o to bring it back instead.") {
		t.Errorf("one, plug-in:\n%s", out)
	}
}

// Review of fix round 6 (must-fix 5): an archive's other/.git, in any
// case, isn't brought back as ~/.cadrei/.git, and the plan names a crew
// with no repo of its own.
func TestAnArchivesGitFolderIsntBroughtBack(t *testing.T) {
	home := sandbox(t)
	stubClaude(t, home)
	a := craftedArchive(t, home)
	for _, name := range []string{".git", ".GIT"} {
		os.MkdirAll(a.Dir+"/other/"+name, 0o755)
		os.WriteFile(a.Dir+"/other/"+name+"/config", []byte("[core]\n"), 0o644)
		if code, _, errOut := callIn("\n", "restore", a.Dir); code == 0 || !strings.Contains(flat(errOut), name+" (a git folder)") {
			t.Errorf("%s: %d %s", name, code, errOut)
		}
		if _, err := os.Stat(home + "/.cadrei/" + name); err == nil {
			t.Errorf("%s came back", name)
		}
		os.RemoveAll(a.Dir + "/other/" + name)
	}
	if _, out, _ := callIn("q", "restore", a.Dir); !strings.Contains(flat(out), "main has no git repo of its own, so cadrei won't commit its changes, and it has no backup.") {
		t.Errorf("plan:\n%s", out)
	}
}
