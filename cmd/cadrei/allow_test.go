//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func gitLog(t *testing.T, dir string) string {
	t.Helper()
	out, _ := exec.Command("git", "-C", dir, "log", "--format=%s").Output()
	return string(out)
}

func TestAllowAddListRemove(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	file := c + "/.claude/member-settings.json"
	if out := must(t, "allow"); out != "What your team can do without asking\n  Nothing extra yet.\nBuilt-in limits keep your team away from cadrei's own files.\n" {
		t.Errorf("allow with no file: %q", out)
	}
	out := must(t, "allow", "add", "Bash(git push origin HEAD:main)")
	if _, err := os.Stat(file); err != nil || !strings.Contains(out, "+ Allowed: Bash(git push origin HEAD:main)") ||
		!strings.Contains(out, "Team members you start from now on get this change.") || strings.Contains(out, "created") {
		t.Errorf("add: %q", out)
	}
	if !strings.Contains(gitLog(t, c), "Allow for members: Bash(git push origin HEAD:main)") {
		t.Errorf("not committed:\n%s", gitLog(t, c))
	}
	if out := must(t, "allow", "add", "Bash(git push origin HEAD:main)"); !strings.Contains(out, "is already allowed. Nothing was changed.") {
		t.Errorf("duplicate: %q", out)
	}
	before := readFile(t, file)
	refused(t, "refused: Bash(bash *) lets bash run anything", "allow", "add", "Bash(bash *)")
	refused(t, "refused: an --auto entry may not speak about permissions", "allow", "add", "--auto", "Changing permissions is fine")
	if readFile(t, file) != before {
		t.Error("a refusal changed the file")
	}
	if out := must(t, "allow", "add", "--auto", "Setting up a local test database is expected"); !strings.Contains(out, "+ Allowed: Setting up a local test database is expected") {
		t.Errorf("--auto: %q", out)
	}
	if out := must(t, "allow", "add", "--once", "Bash(make deploy)"); !strings.Contains(out, "+ Allowed once: Bash(make deploy)") {
		t.Errorf("--once: %q", out)
	}
	if out := must(t, "allow", "add", "Bash(ls docs/*)"); !strings.Contains(out, "contains * and may allow more") {
		t.Errorf("wide: %q", out)
	}
	out = must(t, "allow", "list")
	for _, want := range []string{"  1. rule  Bash(git push origin HEAD:main)", "  2. rule  Bash(make deploy)   [once, added just now]",
		"  3. rule  Bash(ls docs/*)   [wide: contains *]", "  4. auto  Setting up a local test database is expected", "Built-in limits keep your team away from cadrei's own files."} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if out := must(t, "allow", "remove", "--once"); !strings.Contains(out, "+ Removed: Bash(make deploy)") {
		t.Errorf("remove --once: %q", out)
	}
	if out := must(t, "allow", "remove", "--once"); !strings.Contains(out, "There's nothing allowed just once.") {
		t.Errorf("remove --once again: %q", out)
	}
	if out := must(t, "allow", "remove", "1"); !strings.Contains(out, "+ Removed: Bash(git push origin HEAD:main)") {
		t.Errorf("remove 1: %q", out)
	}
	refused(t, "there is no grant number 9", "allow", "remove", "9")
	refused(t, "Bash(nope) is not granted", "allow", "remove", "Bash(nope)")
	refused(t, "remove what? For example:", "allow", "remove", "--auto", "x")
	refused(t, "there's no option --bogus for cadrei allow add", "allow", "add", "--bogus", "x")
	log := gitLog(t, c)
	for _, want := range []string{"Allow for members once: Bash(make deploy)", "Remove one-time grants for members", "Remove grant for members: Bash(git push origin HEAD:main)"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	t.Setenv("CADREI_MEMBER", "x")
	refused(t, "team members can't change what the team is allowed to do", "allow", "add", "Bash(true)")
	refused(t, "team members can't change what the team is allowed to do", "allow", "remove", "1")
}

func TestAllowWarnsAboutAnEditMadeOutside(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	must(t, "allow", "add", "Bash(npm test)")
	file := home + "/.cadrei/work/.claude/member-settings.json"
	os.WriteFile(file, []byte(strings.Replace(readFile(t, file), `"Bash(npm test)"`, `"Bash(npm test)", "Bash(curl *)"`, 1)), 0o644)
	_, _, errOut := call("allow", "add", "Bash(make)")
	if !strings.Contains(errOut, "was changed outside cadrei allow") {
		t.Errorf("no warning: %q", errOut)
	}
	// An unusable file is not changed.
	os.WriteFile(file, []byte(`{"hooks": {}}`), 0o644)
	refused(t, "can't be changed: it has the key hooks", "allow", "add", "Bash(make test)")
}

// Concurrent adds all land: the per-cadrei lock serializes read, change and write.
func TestConcurrentAdds(t *testing.T) {
	sandbox(t)
	must(t, "init", "work")
	must(t, "allow", "add", "Bash(echo first)")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			call("allow", "add", "Bash(echo p"+string(rune('0'+i))+")")
		}(i)
	}
	wg.Wait()
	out := must(t, "allow", "list")
	for i := 0; i < 8; i++ {
		if !strings.Contains(out, "Bash(echo p"+string(rune('0'+i))+")") {
			t.Errorf("a concurrent add was lost:\n%s", out)
		}
	}
}

func TestAllowRestartNote(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	must(t, "up", "dev/engineer")
	out := must(t, "allow", "add", "Bash(true)")
	if !strings.Contains(out, "Team members already running keep what they started with until they restart:") ||
		!strings.Contains(out, "  CADREI_HOME=~/.cadrei/work cadrei stop dev/engineer && CADREI_HOME=~/.cadrei/work cadrei up dev/engineer\n") {
		t.Errorf("restart note:\n%s", out)
	}
}

// Acceptance 64 and 65: members can't edit the files Claude Code loads into
// the orchestrator, nor read or edit archived setups, and cadrei allow
// refuses a rule that names the archive.
func TestMembersStayOffOrchestratorFilesAndArchives(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	must(t, "up", "dev/engineer")
	copies, _ := filepath.Glob(c + "/.claude/build/member-settings.*.json")
	if len(copies) != 1 {
		t.Fatalf("copies %v", copies)
	}
	s := readFile(t, copies[0])
	for _, want := range []string{"Edit(//**/.cadrei/*/CLAUDE.md)", "Edit(//**/.cadrei/*/CLAUDE.local.md)", "Edit(//**/.cadrei/*/.mcp.json)",
		"Edit(/" + c + "/CLAUDE.md)", "Edit(/" + c + "/.mcp.json)", "Read(//**/.cadrei-archive/**)",
		// Acceptance 77: this machine's notes, under .claude/local.
		"Edit(//**/.cadrei/*/.claude/**)", "Edit(/" + c + "/.claude/**)", "Read(/" + home + "/.cadrei-archive/**)", "Edit(/" + home + "/.cadrei-archive/**)"} {
		if !strings.Contains(s, `"`+want+`"`) {
			t.Errorf("no %s in the member's copy", want)
		}
	}
	refused(t, "reaches ~/.cadrei-archive", "allow", "add", "Read(~/.cadrei-archive/**)")
	refused(t, "reaches ~/.cadrei-archive", "allow", "add", "Edit(//**/.cadrei-archive/x)")
	os.WriteFile(c+"/CLAUDE.md", []byte("x"), 0o644)
	if _, _, errOut := call("--check"); !strings.Contains(flat(errOut), "~/.cadrei/work/CLAUDE.md exists. Claude Code loads it into your orchestrator, and cadrei never writes it.") {
		t.Errorf("--check: %q", errOut)
	}
}
