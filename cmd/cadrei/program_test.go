//go:build !windows

package main

import (
	"os"
	"strings"
	"testing"
)

// fakeBrew puts a brew on PATH that records each run, and whether cadrei's
// own files were still there then, and exits with exit (printing why when
// it fails). The real brew is never run.
func fakeBrew(t *testing.T, home string, exit string) string {
	t.Helper()
	bin := home + "/brew-bin"
	os.MkdirAll(bin, 0o755)
	log := home + "/brew-ran"
	writeStub(t, bin+"/brew", "#!/bin/sh\n{ echo \"$*\"; echo \"no-update=$HOMEBREW_NO_AUTO_UPDATE\"; [ -e \""+home+"/.cadrei/config\" ] && echo config-left; } >> \""+log+"\"\n"+
		"[ "+exit+" = 0 ] || echo 'Error: No such keg: /opt/homebrew/Cellar/cadrei' >&2\nexit "+exit+"\n")
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	kindForTests = "homebrew"
	t.Cleanup(func() { kindForTests = "" })
	return log
}

// setUpAgain gives a plain uninstall something to remove again: it keeps
// the crews, so a new one would clash.
func setUpAgain(home string) {
	os.MkdirAll(home+"/.cadrei/config", 0o700)
	os.WriteFile(home+"/.cadrei/config/default", []byte("work\n"), 0o600)
}

// lastStep is a plan's last "  - " line.
func lastStep(out string) string {
	last := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "  - ") {
			last = l
		}
	}
	return last
}

// On a Homebrew install, removing the program is the plan's last step, and
// the one yes covers it: no second question. --keep-program leaves it.
func TestUninstallRemovesAHomebrewProgramLast(t *testing.T) {
	home := sandbox(t)
	ran := fakeBrew(t, home, "0")
	must(t, "init", "work")

	out := must(t, "uninstall", "--dry-run")
	if lastStep(out) != "  - remove the cadrei program (brew uninstall cadrei)" || strings.Contains(out, "Then:") {
		t.Errorf("dry run:\n%s", out)
	}
	if out := must(t, "uninstall", "--dry-run", keepFlag); strings.Contains(out, "remove the cadrei program") || !strings.Contains(out, "Then: brew uninstall cadrei") {
		t.Errorf("dry run with %s:\n%s", keepFlag, out)
	}

	// In a terminal: one question, and a no changes nothing.
	ttyForTests = true
	t.Cleanup(func() { ttyForTests = false })
	if _, out, errOut := callIn("n\n", "uninstall"); strings.Count(out+errOut, "?") != 1 || exists(ran) {
		t.Errorf("a no:\n%s%s", out, errOut)
	}
	code, out, errOut := callIn("y\n", "uninstall")
	if code != 0 || strings.Count(out+errOut, "?") != 1 || !strings.Contains(out, "Removed the cadrei program (brew uninstall cadrei)") || strings.Contains(out, "To finish") {
		t.Errorf("a yes: exit %d\n%s%s", code, out, errOut)
	}
	if got := readFile(t, ran); got != "uninstall cadrei\nno-update=1\n" {
		t.Errorf("brew ran as %q", got)
	}
	os.Remove(ran)
	ttyForTests = false

	// --yes is the yes, as the orchestrator runs it.
	setUpAgain(home)
	if out := must(t, "uninstall", "--yes"); !strings.Contains(out, "Removed the cadrei program") || !exists(ran) {
		t.Errorf("--yes:\n%s", out)
	}
	os.Remove(ran)
	setUpAgain(home)
	if out := must(t, "uninstall", "--yes", keepFlag); !strings.Contains(flat(out), "To finish: brew uninstall cadrei") || exists(ran) {
		t.Errorf("%s:\n%s", keepFlag, out)
	}
}

// A brew that fails says why and how, after everything else is done.
func TestAFailedBrewSaysWhatToRun(t *testing.T) {
	home := sandbox(t)
	ran := fakeBrew(t, home, "1")
	must(t, "init", "work")
	code, out, errOut := call("uninstall", "--yes")
	all := flat(out + errOut)
	if code != 2 || !strings.Contains(all, "Couldn't remove the cadrei program: brew says No such keg: /opt/homebrew/Cellar/cadrei. Everything else is done.") ||
		!strings.Contains(all, "Run brew uninstall cadrei yourself.") {
		t.Errorf("exit %d\n%s", code, all)
	}
	if !exists(ran) || strings.Contains(readFile(t, ran), "config-left") || exists(home+"/.cadrei/config") {
		t.Error("brew ran before cadrei's own cleanup was done")
	}
}

// --all and --archive end the same way: the program is their plan's last
// step, removed after the deletes or the move, and the archive's next step
// is a fresh install.
func TestEveryUninstallRemovesTheProgramLast(t *testing.T) {
	home := sandbox(t)
	ran := fakeBrew(t, home, "0")
	must(t, "init", "work")
	if out := must(t, "uninstall", "--archive", "--dry-run"); lastStep(out) != "  - remove the cadrei program (brew uninstall cadrei)" {
		t.Errorf("--archive's plan:\n%s", out)
	}
	if out := must(t, "uninstall", "--archive", "--yes"); !strings.Contains(out, "Removed the cadrei program") ||
		!strings.Contains(out, "brew install rafi-ramdhani/cadrei/cadrei") || strings.Contains(out, "brew uninstall cadrei &&") {
		t.Errorf("--archive:\n%s", out)
	}
	if got := readFile(t, ran); got != "uninstall cadrei\nno-update=1\n" {
		t.Errorf("--archive: brew ran as %q", got)
	}
	os.Remove(ran)
	must(t, "init", "work")
	out := must(t, "uninstall", "--all", "--dry-run")
	if lastStep(out) != "  - remove the cadrei program (brew uninstall cadrei)" || !strings.HasSuffix(strings.TrimSpace(flat(out)), "type delete everything.") {
		t.Errorf("--all's plan:\n%s", out)
	}
	if out := must(t, "uninstall", "--all", "--confirm", deletePhrase); !strings.Contains(out, "Removed the cadrei program") || strings.Contains(out, "but the program") || !exists(ran) || exists(home+"/.cadrei") {
		t.Errorf("--all:\n%s", out)
	}
	os.Remove(ran)
	must(t, "init", "work")
	if out := must(t, "uninstall", "--all", "--confirm", deletePhrase, keepFlag); exists(ran) || !strings.Contains(flat(out), "To finish: brew uninstall cadrei") {
		t.Errorf("--all %s:\n%s", keepFlag, out)
	}
}

// Any other install is a file for the user to delete: the plan doesn't
// offer it, the last line names it, and brew never runs.
func TestOnlyAHomebrewProgramIsRemoved(t *testing.T) {
	home := sandbox(t)
	ran := fakeBrew(t, home, "0")
	must(t, "init", "work")
	for _, kind := range []string{"release", "dev"} {
		kindForTests = kind
		setUpAgain(home)
		if plan := must(t, "uninstall", "--dry-run"); strings.Contains(plan, "remove the cadrei program") {
			t.Errorf("%s's plan:\n%s", kind, plan)
		}
		out := flat(must(t, "uninstall", "--yes"))
		if !strings.Contains(out, "To finish: ") || exists(ran) {
			t.Errorf("%s:\n%s", kind, out)
		}
	}
}

// Review of fix round 8 (S1): when cadrei's own cleanup partly failed, the
// program stays, so the same command can run again and finish, and brew
// never runs.
func TestAPartlyFailedUninstallKeepsTheProgram(t *testing.T) {
	home := sandbox(t)
	ran := fakeBrew(t, home, "0")
	must(t, "init", "work")
	stuck := home + "/.cadrei/work/teams/dev/stuck"
	os.MkdirAll(stuck, 0o755)
	os.WriteFile(stuck+"/f", []byte("x\n"), 0o644)
	os.Chmod(stuck, 0o500)
	t.Cleanup(func() { os.Chmod(stuck, 0o755) })
	code, out, errOut := call("uninstall", "--all", "--confirm", deletePhrase)
	if all := flat(out + errOut); code != 2 || !strings.Contains(all, "The cadrei program stays, since not all of cadrei could be removed. Fix what's above, then run the same command again: it removes the program last.") ||
		strings.Contains(all, "To finish") || exists(ran) {
		t.Errorf("exit %d\n%s", code, all)
	}
	// Fixed, the same command finishes, and removes the program last.
	os.Chmod(stuck, 0o755)
	if out := must(t, "uninstall", "--all", "--confirm", deletePhrase); !strings.Contains(out, "Removed the cadrei program") || !exists(ran) {
		t.Errorf("the second run:\n%s", out)
	}
}

// Review of fix round 9: plain uninstall keeps the program too when its
// own cleanup partly failed, and the same command finishes after.
func TestAPartlyFailedPlainUninstallKeepsTheProgram(t *testing.T) {
	home := sandbox(t)
	ran := fakeBrew(t, home, "0")
	must(t, "init", "work")
	stuck := home + "/.cadrei/config/stuck"
	os.MkdirAll(stuck, 0o755)
	os.WriteFile(stuck+"/f", []byte("x\n"), 0o644)
	os.Chmod(stuck, 0o500)
	t.Cleanup(func() { os.Chmod(stuck, 0o755) })
	code, out, errOut := call("uninstall", "--yes")
	if all := flat(out + errOut); code != 2 || !strings.Contains(all, "The cadrei program stays, since not all of cadrei could be removed. Fix what's above, then run the same command again: it removes the program last.") ||
		strings.Contains(all, "To finish") || exists(ran) {
		t.Errorf("exit %d\n%s", code, all)
	}
	os.Chmod(stuck, 0o755)
	if out := must(t, "uninstall", "--yes"); !strings.Contains(out, "Removed the cadrei program") || !exists(ran) {
		t.Errorf("the second run:\n%s", out)
	}
}
