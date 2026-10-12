//go:build !windows

package main

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	cadrei "github.com/rafi-ramdhani/cadrei"
	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// versions are the tool versions a screen shows, which differ by machine.
var versions = regexp.MustCompile(`(git|tmux) [0-9][^ \n]*`)

// screenOf is a first-run screen with the machine's tool versions masked.
func screenOf(out string) string { return versions.ReplaceAllString(out, "$1 <version>") }

// inRepo makes a git repository my-app in home and moves into it.
func inRepo(t *testing.T, home string) string {
	t.Helper()
	dir := home + "/code/my-app"
	os.MkdirAll(dir, 0o755)
	exec.Command("git", "-C", dir, "init", "-q").Run()
	t.Chdir(dir)
	return dir
}

// claudeConfig gives the sandbox a Claude Code config, as a machine where
// Claude Code ran once has.
func claudeConfig(t *testing.T, home string) {
	t.Helper()
	os.WriteFile(home+"/.claude.json", []byte("{}\n"), 0o600)
}

// Acceptance 1, 2, 10 and 69: one Enter sets everything up with no other
// question, and opens the orchestrator in tmux.
func TestFirstRunEnter(t *testing.T) {
	home := firstMachine(t)
	os.Remove(home + "/release") // in tmux, the stub runs like a session
	stubAttach(t, home)
	claudeConfig(t, home)
	repo := inRepo(t, home)
	code, out, errOut := callIn("\n\n")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	golden(t, "first-run.txt", screenOf(out))
	c := home + "/.cadrei/main"
	w, _ := cadreis.Find("main")
	if cadreis.Default() != "main" || cadreis.Place(w, "my-app") != repo || !strings.Contains(readFile(t, c+"/cadrei.conf"), "\nORCHESTRATOR=tmux\n") {
		t.Error("the crew, its project or its setting")
	}
	cfg := readFile(t, home+"/.claude.json")
	if !strings.Contains(cfg, `"`+c+`"`) || !strings.Contains(cfg, `"`+repo+`"`) || strings.Count(cfg, "hasTrustDialogAccepted") != 2 {
		t.Errorf("trust:\n%s", cfg)
	}
	if _, err := os.Stat(home + "/.claude.json.bak-cadrei"); err != nil {
		t.Error("no backup of the Claude Code config")
	}
	if to, _ := os.Readlink(home + "/.claude/skills/cadrei"); to != framework.SkillDir() {
		t.Errorf("skill link %q", to)
	}
	if strings.Contains(out, "ask to trust ~/.cadrei/main") || strings.Contains(errOut, "?") {
		t.Errorf("a trust line or a question:\n%s%s", out, errOut)
	}
	if tmuxIn(os.Getenv("CADREI_TMUX_SOCKET"), "show-options", "-qv", "-t", "=cadrei-main:", "@cadrei_role") != "orchestrator" {
		t.Error("the orchestrator didn't open in tmux")
	}
	if cadreis.GetState(cadreis.FirstTaskPending) == "" {
		t.Error("the first task isn't pending")
	}
}

// Acceptance 3 and 69: p opens it here, with no tutorial; with no Claude
// Code config yet, the trust line shows instead.
func TestFirstRunPlainTerminal(t *testing.T) {
	home := firstMachine(t)
	code, out, errOut := callIn("p\n")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	golden(t, "first-run-plain.txt", screenOf(out))
	if !strings.Contains(readFile(t, home+"/.cadrei/main/cadrei.conf"), "\nORCHESTRATOR=terminal\n") || !strings.Contains(readFile(t, home+"/orch-ran"), "--name\nmain-orchestrator\n") {
		t.Error("the setting, or the orchestrator here")
	}
	if strings.Contains(out, "tmux. Three things") {
		t.Error("a tmux tutorial")
	}
}

// Acceptance 4: q, or the end of the input, changes nothing.
func TestFirstRunQuit(t *testing.T) {
	for _, input := range []string{"q\n", ""} {
		home := firstMachine(t)
		claudeConfig(t, home)
		code, out, _ := callIn(input)
		if code != 1 || !strings.HasSuffix(out, "\n  Nothing was changed.\n") {
			t.Errorf("%q: exit %d\n%s", input, code, out)
		}
		for _, p := range []string{home + "/.cadrei", home + "/.claude/skills/cadrei"} {
			if _, err := os.Lstat(p); err == nil {
				t.Errorf("%q made %s", input, p)
			}
		}
		if readFile(t, home+"/.claude.json") != "{}\n" {
			t.Errorf("%q changed the Claude Code config", input)
		}
	}
}

// Acceptance 5: outside a git repository, no project line, no project.
func TestFirstRunOutsideARepo(t *testing.T) {
	firstMachine(t)
	_, out, _ := callIn("p\n")
	if strings.Contains(out, "as your first project") || strings.Contains(must(t, "ls"), "\n  None yet.") == false {
		t.Errorf("a project:\n%s", out)
	}
}

// Acceptance 51: a repo with its own Claude Code settings is left out of
// the plan unless a adds it, with its warning.
func TestFirstRunRepoWithItsOwnSettings(t *testing.T) {
	home := firstMachine(t)
	claudeConfig(t, home)
	repo := inRepo(t, home)
	os.MkdirAll(repo+"/.claude", 0o755)
	os.WriteFile(repo+"/.claude/settings.json", []byte("{}"), 0o644)
	_, out, _ := callIn("p\n")
	golden(t, "first-run-own-settings.txt", screenOf(out))
	if strings.Contains(readFile(t, home+"/.claude.json"), repo) {
		t.Error("the repo was trusted")
	}
	home = firstMachine(t)
	claudeConfig(t, home)
	repo = inRepo(t, home)
	os.WriteFile(repo+"/.mcp.json", []byte("{}"), 0o644)
	// a adds it, a again leaves it out, a once more adds it; then p.
	_, out, _ = callIn("a\na\na\np\n")
	if !strings.Contains(out, "! my-app's own settings (.mcp.json) will run for your team too.") || !strings.Contains(out, "Or press a to leave it out,") {
		t.Errorf("after a:\n%s", out)
	}
	if !strings.Contains(readFile(t, home+"/.claude.json"), `"`+repo+`"`) {
		t.Error("a then Enter didn't add and trust it")
	}
}

// Acceptance 8 and 36: a missing or broken tool stops setup, changing
// nothing; a Claude Code that isn't logged in only says so.
func TestFirstRunChecksTheTools(t *testing.T) {
	stub := func(t *testing.T, home, name, script string) {
		writeStub(t, home+"/bin/"+name, "#!/bin/sh\n"+script)
	}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, home string)
		want  string
	}{
		{"no claude", func(t *testing.T, home string) {
			// Only the tools setup needs, without claude: never the real one.
			bin := home + "/noclaude"
			os.MkdirAll(bin, 0o755)
			for _, tool := range []string{"git", "sh", "tmux"} {
				p, _ := exec.LookPath(tool)
				os.Symlink(p, bin+"/"+tool)
			}
			t.Setenv("PATH", bin)
		}, "x Claude Code isn't installed."},
		{"git fails", func(t *testing.T, home string) { stub(t, home, "git", "exit 1\n") }, "x git isn't installed."},
		{"no tmux", func(t *testing.T, home string) {
			// Only the tools setup needs, without tmux.
			bin := home + "/notmux"
			os.MkdirAll(bin, 0o755)
			os.Link(home+"/bin/claude", bin+"/claude")
			for _, tool := range []string{"git", "sh"} {
				p, _ := exec.LookPath(tool)
				os.Symlink(p, bin+"/"+tool)
			}
			t.Setenv("PATH", bin)
		}, "x tmux isn't installed. Your team runs in it."},
		{"old tmux", func(t *testing.T, home string) { stub(t, home, "tmux", "echo 'tmux 3.1'\n") }, "x tmux 3.1 is too old. Cadrei needs 3.2 or newer."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := firstMachine(t)
			tc.setup(t, home)
			code, out, _ := callIn("\n\n")
			if code != 1 || !strings.Contains(out, tc.want) || !strings.HasSuffix(out, "\n  Nothing was changed. Run cadrei again once that's fixed.\n") || strings.Contains(out, "Here's the plan") {
				t.Errorf("exit %d:\n%s", code, out)
			}
			if _, err := os.Stat(home + "/.cadrei"); err == nil {
				t.Error("something was made")
			}
		})
	}
	home := firstMachine(t)
	stub(t, home, "claude", "case \"$1 $2\" in '--version '*) echo '2.1.300 (Claude Code)';; 'auth status') echo '{\"loggedIn\": false}'; exit 1;; esac\nexit 0\n")
	if code, out, _ := callIn("p\n"); code != 0 || !strings.Contains(flat(out), "! Claude Code 2.1.300, not logged in yet The setup logs you in before your orchestrator opens.") {
		t.Errorf("not logged in: %d\n%s", code, out)
	}
}

// loggedOut makes the claude stub logged out, with a login that ends
// with exit and notes each call in home/calls.
func loggedOut(t *testing.T, home string, exit int) {
	t.Helper()
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+
		"case \"$1 $2\" in\n"+
		"  '--version '*) echo '2.1.300 (Claude Code)'; exit 0 ;;\n"+
		"  'auth status') echo '{\"loggedIn\": false}'; exit 1 ;;\n"+
		"  'auth login') echo login >> \""+home+"/calls\"; exit "+strconv.Itoa(exit)+" ;;\n"+
		"esac\n"+
		"echo orchestrator >> \""+home+"/calls\"\n")
}

// A Claude Code that ran before but is logged out now opens on its prompt,
// so the orchestrator's first message would only get "Not logged in" back.
// The first run logs the user in first, in this terminal, and opens the
// orchestrator once that went through.
func TestFirstRunLogsInBeforeTheOrchestratorOpens(t *testing.T) {
	home := firstMachine(t)
	loggedOut(t, home, 0)
	code, out, errOut := callIn("p\n")
	if code != 0 {
		t.Fatalf("exit %d:\n%s%s", code, out, errOut)
	}
	for _, want := range []string{
		"- Log in to Claude Code (it opens your browser)",
		"Now log in to Claude Code.",
		"+ Logged in to Claude Code",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if got := readFile(t, home+"/calls"); got != "login\norchestrator\n" {
		t.Errorf("calls:\n%s", got)
	}
	// The checks are one block: no blank line inside it.
	checks := out[strings.Index(out, "Checking your setup"):strings.Index(out, "Here's the plan")]
	if strings.Count(checks, "\n\n") != 1 || !strings.HasSuffix(checks, "\n\n  ") {
		t.Errorf("a blank line in the checks:\n%q", checks)
	}
}

// A login that fails or is cancelled (Ctrl-C ends it with 130) leaves the
// crew made and says how to log in and go on. The orchestrator doesn't
// open: it would only answer "Not logged in".
func TestFirstRunSaysHowToLogInWhenTheLoginFails(t *testing.T) {
	for _, exit := range []int{1, 130} {
		t.Run(strconv.Itoa(exit), func(t *testing.T) {
			home := firstMachine(t)
			loggedOut(t, home, exit)
			code, out, errOut := callIn("p\n")
			all := flat(out + errOut)
			if code != 1 ||
				!strings.Contains(all, "not logged in to Claude Code yet, so your orchestrator didn't open.") ||
				!strings.Contains(all, "Your crew is ready. Log in with claude auth login, then run cadrei.") {
				t.Errorf("exit %d:\n%s%s", code, out, errOut)
			}
			if got := readFile(t, home+"/calls"); got != "login\n" {
				t.Errorf("calls:\n%s", got)
			}
			if _, err := os.Stat(home + "/.cadrei/main"); err != nil {
				t.Errorf("no crew: %v", err)
			}
		})
	}
}

// Review of fix round 5 (tests, Probe.Says): cadrei --check says what the
// first run's screen says about Claude Code: not logged in, a login it
// can't check, and one that won't start, with how to fix it.
func TestTheFullCheckSaysWhatTheFirstRunSays(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	stub := func(script string) {
		writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+script)
	}
	for _, tc := range []struct{ script, want string }{
		{"case \"$1 $2\" in '--version '*) echo '2.1.300 (Claude Code)';; 'auth status') echo '{\"loggedIn\": false}'; exit 1;; esac\nexit 0\n",
			"! Claude Code 2.1.300, not logged in yet Log in with claude auth login."},
		{"case \"$1 $2\" in '--version '*) echo '2.1.300 (Claude Code)';; 'auth status') echo 'unknown command'; exit 2;; esac\nexit 0\n",
			"! Claude Code 2.1.300. Couldn't check your login. If it asks, log in."},
		{"exit 1\n", "Claude Code is installed but won't start. Reinstall it with brew reinstall"},
	} {
		stub(tc.script)
		_, out, errOut := call("--check")
		if !strings.Contains(flat(out+errOut), tc.want) {
			t.Errorf("--check lacks %q:\n%s%s", tc.want, out, errOut)
		}
	}
}

// Audit qual-maint-3: the first run, cadrei --check and cadrei up check
// tmux the same way and say the same thing: 3.1a is too old in all three,
// and a version that can't be read is a warning, not a stop.
func TestEveryTmuxCheckSaysTheSame(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	must(t, "init", "work")
	real, _ := exec.LookPath("tmux")
	stub := func(t *testing.T, home, name, script string) {
		writeStub(t, home+"/bin/"+name, "#!/bin/sh\n"+script)
	}
	for version, want := range map[string]string{"tmux 3.1a": "tmux 3.1a is too old. Cadrei needs 3.2 or newer.", "tmux master": "Cadrei can't tell which tmux this is (master). It needs 3.2 or newer."} {
		stub(t, home, "tmux", "[ \"$1\" = -V ] && { echo '"+version+"'; exit 0; }\nexec "+real+" \"$@\"\n")
		_, _, check := call("--check")
		if !strings.Contains(flat(check), want) {
			t.Errorf("--check with %s:\n%s", version, check)
		}
		code, _, up := call("up", "dev/engineer")
		if strings.Contains(want, "too old") != (code == 1 && strings.Contains(flat(up), want)) {
			t.Errorf("up with %s: exit %d %s", version, code, up)
		}
		if c := tmuxCheck(); c.what != want {
			t.Errorf("the first run's row with %s: %q", version, c.what)
		}
		call("stop", "dev/engineer")
	}
}

// Acceptance 36: in raw mode, only a read that is one key counts.
func TestRawKeys(t *testing.T) {
	for read, want := range map[string]byte{"\r": keyEnter, "p": 'p', "\x03": 'q', "\x04": 'q'} {
		if got, ok := keyOf([]byte(read), "prq"); !ok || got != want {
			t.Errorf("%q: %q %v", read, got, ok)
		}
	}
	for _, read := range []string{"x\r", "\x1b[A", "x", "pp"} {
		if _, ok := keyOf([]byte(read), "prq"); ok {
			t.Errorf("%q counted", read)
		}
	}
}

// Acceptance 9: no git identity, one line on the first run, once.
func TestFirstRunWithoutAGitIdentity(t *testing.T) {
	home := firstMachine(t)
	empty := filepath.Join(home, "no-identity")
	os.WriteFile(empty, nil, 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	_, out, errOut := callIn("p\n")
	if strings.Count(out+errOut, "Git has no name or email set") != 1 || !strings.Contains(out, "! Git has no name or email set, so history isn't saved. See cadrei --check") {
		t.Errorf("first run:\n%s%s", out, errOut)
	}
	// Review 5: nothing about it on later commands, the ones that commit too.
	os.MkdirAll(home+"/src/app", 0o755)
	exec.Command("git", "-C", home+"/src/app", "init", "-q").Run()
	for _, args := range [][]string{{}, {"project", "add", "app", "--path", home + "/src/app"}, {"up", "dev/engineer"}, {"allow", "add", "Bash(npm test)"}, {"project", "unlink", "app"}, {"new", "life"}} {
		if _, out, errOut := call(args...); strings.Contains(out+errOut, "Git has no name") || strings.Contains(out+errOut, "left uncommitted") {
			t.Errorf("cadrei %v:\n%s%s", args, out, errOut)
		}
	}
	// cadrei --check is where the full fix is.
	if _, out, errOut := call("--check"); !strings.Contains(flat(out+errOut), "! Git has no name or email set, so cadrei can't save its history.") {
		t.Errorf("--check:\n%s%s", out, errOut)
	}
}

// Acceptance 11 and 12: narrow, plain and styled.
func TestFirstRunScreenModes(t *testing.T) {
	home := firstMachine(t)
	claudeConfig(t, home)
	inRepo(t, home)
	columnsForTests = 50
	t.Cleanup(func() { columnsForTests = 0 })
	_, out, _ := callIn("q\n")
	golden(t, "first-run-narrow.txt", screenOf(out))
	// Wider than 50 only for one path, command or URL too long on its own
	// (review: a "/" anywhere didn't excuse a line).
	for _, l := range strings.Split(out, "\n") {
		if len([]rune(l)) <= 50 {
			continue
		}
		longest := 0
		for _, f := range strings.Fields(l) {
			longest = max(longest, len([]rune(f)))
		}
		if longest <= 46 {
			t.Errorf("wider than 50: %q", l)
		}
	}
	columnsForTests = 0
	if strings.Contains(out, "\x1b[") || !strings.Contains(flat(out), "+ Claude Code 2.1.300 (logged in), git and tmux are ready") || !strings.Contains(out, "  - Create your crew") {
		t.Errorf("plain:\n%s", out)
	}
	t.Setenv("FORCE_COLOR", "1")
	if _, out, _ := callIn("q\n"); !strings.Contains(out, "\x1b[32m✓\x1b[0m Claude Code 2.1.300 (logged in), git and tmux are ready") || !strings.Contains(out, "\x1b[2m·\x1b[0m Create your crew \x1b[1") {
		t.Errorf("styled:\n%q", out)
	}
}

// Acceptance 13: no terminal, no crew.
func TestFirstRunWithoutATerminal(t *testing.T) {
	home := firstMachine(t)
	ttyForTests = false
	code, out, errOut := call()
	if code != 1 || out != "" || errOut != "cadrei: there's no crew on this machine yet.\n  Run cadrei in a terminal to set one up.\n" {
		t.Errorf("exit %d %q %q", code, out, errOut)
	}
	if _, err := os.Stat(home + "/.cadrei"); err == nil {
		t.Error("something was made")
	}
}

// Acceptance 7, 63, 66 and 70: a restore from GitHub, its refusals, and
// which projects it trusts.
func TestFirstRunRestore(t *testing.T) {
	home := firstMachine(t)
	claudeConfig(t, home)
	app, blog := bareRepo(t, "app"), bareRepo(t, "blog")
	exec.Command("git", "clone", "-q", blog, home+"/blog-src").Run()
	os.WriteFile(home+"/blog-src/.mcp.json", []byte("{}"), 0o644)
	exec.Command("git", "-C", home+"/blog-src", "add", "-A").Run()
	exec.Command("git", "-C", home+"/blog-src", "commit", "-qm", "tools").Run()
	exec.Command("git", "-C", home+"/blog-src", "push", "-q", "origin", "HEAD").Run()
	backup := backupOf(t, "work", app, func(src string) {
		os.WriteFile(src+"/projects.yaml", []byte("app:\n  repo: "+app+"\n  team: dev\nblog:\n  repo: "+blog+"\n  team: dev\n"), 0o644)
	})
	code, out, errOut := callIn("r\n" + backup + "\np\n\n")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	for _, want := range []string{"Restore your crew from GitHub", "  - Copy " + backup + " into ~/.cadrei/work", "+ Restored your crew work into ~/.cadrei/work",
		"+ Cloned app into ~/Developer/app and trusted it", "! Cloned blog into ~/Developer/blog. It has its own Claude Code settings (.mcp.json), so it isn't trusted yet.",
		"Claude Code will ask you to trust ~/.cadrei/work. It holds what you restored from " + backup + ", so say yes only if that's your backup."} {
		if !strings.Contains(flat(out), flat(want)) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	cfg := readFile(t, home+"/.claude.json")
	if !strings.Contains(cfg, home+"/Developer/app") || strings.Contains(cfg, home+"/Developer/blog") || strings.Contains(cfg, home+"/.cadrei/work") {
		t.Errorf("trust after a restore:\n%s", cfg)
	}
	// The pick is the crew's setting, not a machine override, so a later
	// cadrei tmux on|off holds (review 4).
	if _, err := os.Stat(home + "/.cadrei/work/.claude/local/cadrei.conf"); err == nil && strings.Contains(readFile(t, home+"/.cadrei/work/.claude/local/cadrei.conf"), "ORCHESTRATOR") {
		t.Error("the restore wrote a machine override")
	}
	must(t, "tmux", "on")
	if out := must(t, "tmux"); out != "Your orchestrator opens in tmux.\n" {
		t.Errorf("tmux after a restore: %q", out)
	}
	if cadreis.GetState(cadreis.FirstTaskPending) != "" {
		t.Error("a restore set the first-run state")
	}
	// Not a crew: nothing is kept.
	firstMachine(t)
	if code, out, _ := callIn("r\n" + app + "\n\n"); code != 1 || !strings.Contains(flat(out), "x "+app+" isn't a crew (it has no members folder). Nothing was kept.") {
		t.Errorf("not a crew: %d\n%s", code, out)
	}
	if list, _ := cadreis.List(); len(list) != 0 {
		t.Error("something was kept")
	}
}

// Acceptance 74 and 80: crews already in ~/.cadrei, with no default, are
// plugged back in.
func TestPlugBackIn(t *testing.T) {
	home := firstMachine(t)
	must(t, "init", "work")
	must(t, "init", "life")
	os.RemoveAll(home + "/.cadrei/config")
	os.RemoveAll(home + "/.claude/skills/cadrei")
	// d picks the next crew as the default; p opens here.
	code, out, errOut := callIn("d\np\n")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	golden(t, "plug-in.txt", screenOf(out))
	if cadreis.Default() != "work" {
		t.Errorf("default %q", cadreis.Default())
	}
	if to, _ := os.Readlink(home + "/.claude/skills/cadrei"); to == "" {
		t.Error("the skill isn't linked")
	}
}

// Audit qual-bugs-5: a new crew's name is checked as it's typed: an empty
// answer keeps the name (still taken: asked again), a name a crew can't
// have and one that differs only in case are refused, each saying why.
func TestTheNewCrewNameIsChecked(t *testing.T) {
	home := firstMachine(t)
	must(t, "init", "main")
	os.RemoveAll(home + "/.cadrei/config")
	_, out, errOut := callIn("n\nmy crew\nMAIN\nside\nq\n")
	out += errOut
	for _, want := range []string{"You already have a crew called main. Name for the new one?",
		"A crew's name can use letters, digits, - and _, so my crew won't work. Name for the new one?",
		"Create your crew side in ~/.cadrei/side"} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "You already have a crew called main."); n != 2 {
		t.Errorf("MAIN wasn't refused as main: %d\n%s", n, out)
	}
	_, out, _ = callIn("n\n\n\n\n")
	if strings.Contains(out, "Create your crew") || !strings.Contains(out, "Nothing was changed.") {
		t.Errorf("an empty name reached the plan:\n%s", out)
	}
	// A restore's name from a repo that can't be a crew's name says so.
	firstMachine(t)
	app := bareRepo(t, "app")
	backup := backupOf(t, "crew.v2", app)
	_, out, errOut = callIn("r\n" + backup + "\nkept\nq\n")
	out += errOut
	if strings.Contains(out, "You already have a crew called crew.v2") || !strings.Contains(flat(out), "so crew.v2 won't work. Name for this one?") ||
		!strings.Contains(flat(out), "Copy "+backup+" into ~/.cadrei/kept") {
		t.Errorf("restore:\n%s", out)
	}
}

// stubAttach puts a tmux in front of the real one that ends at once for
// attach, as a user leaving at once would: tests have no terminal for it.
func stubAttach(t *testing.T, home string) {
	t.Helper()
	real, _ := exec.LookPath("tmux")
	dir := filepath.Join(home, "stub-tmux")
	os.MkdirAll(dir, 0o755)
	writeStub(t, dir+"/tmux", "#!/bin/sh\n[ \"$3\" = attach ] && exit 0\nexec "+real+" \"$@\"\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// Review 5 and 8.3.1: with an unsafe install, the first run says each
// thing once, on its screen, and no health-check rows follow it.
func TestFirstRunSaysEachWarningOnce(t *testing.T) {
	home := firstMachine(t)
	hookAnywhereForTests = false
	empty := filepath.Join(home, "no-identity")
	os.WriteFile(empty, nil, 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	_, out, errOut := callIn("p\n")
	all := out + errOut
	for _, once := range []string{"Git has no name or email set", "in a folder others can change"} {
		if strings.Count(all, once) != 1 {
			t.Errorf("%q %d times:\n%s%s", once, strings.Count(all, once), out, errOut)
		}
	}
	for _, never := range []string{"no check for passwords and keys", "Team members pick up where they left off"} {
		if strings.Contains(all, never) {
			t.Errorf("%q repeated:\n%s%s", never, out, errOut)
		}
	}
}

// Audit qual-tests-01, acceptance 12 and 100: in a styled terminal, a
// changes the plan in place (the cursor goes back up over the plan drawn
// before); with NO_COLOR or TERM=dumb nothing moves the cursor, and the
// new plan is printed below.
func TestThePlanRedrawsOnlyWhenStyled(t *testing.T) {
	for _, screen := range []string{"new crew", "plug-in"} {
		for _, mode := range []struct {
			name, env, value string
			inPlace          bool
		}{{"styled", "FORCE_COLOR", "1", true}, {"NO_COLOR", "NO_COLOR", "1", false}, {"dumb", "TERM", "dumb", false}} {
			t.Run(screen+" "+mode.name, func(t *testing.T) {
				home := firstMachine(t)
				keys := "a\nq\n"
				if screen == "plug-in" {
					// d picks another default, which draws the plan again
					// (review of fix round 4, S5).
					must(t, "init", "work")
					must(t, "init", "life")
					os.RemoveAll(home + "/.cadrei/config")
					keys = "d\nq\n"
				} else {
					repo := inRepo(t, home)
					os.WriteFile(repo+"/.mcp.json", []byte("{}"), 0o644)
				}
				terminalForTests = true
				t.Cleanup(func() { terminalForTests = false })
				t.Setenv("TERM", "xterm-256color")
				t.Setenv(mode.env, mode.value)
				// The screen of a single draw: q quits and changes nothing.
				_, once, _ := callIn("q\n")
				_, out, _ := callIn(keys)
				plans := strings.Count(out, "Here's the plan")
				moved := regexp.MustCompile(`\x1b\[\d+A\r\x1b\[J`).MatchString(out)
				switch {
				case mode.inPlace && (!moved || plans != 2):
					t.Errorf("styled: moved %v, %d plans:\n%q", moved, plans, out)
				case mode.inPlace && (strings.Count(onScreen(out), "Here's the plan") != 1 || !strings.Contains(onScreen(out), "git and tmux are ready") ||
					!maps.Equal(repeated(onScreen(out)), repeated(onScreen(once))) ||
					above(onScreen(out)) != above(onScreen(once))):
					// The cursor goes up exactly over the plan drawn before:
					// one plan, the checks above it still there, and no row
					// of the old one left, blank or not (review of fix round
					// 5, tests N4a): above the plan, the rows of a single
					// draw, and the lines that show twice are its own.
					t.Errorf("the screen after the redraw:\n%s\nafter one draw:\n%s", onScreen(out), onScreen(once))
				case !mode.inPlace && (strings.Contains(out, "\x1b") || plans != 2):
					t.Errorf("plain: %d plans:\n%q", plans, out)
				}
			})
		}
	}
}

// above is what a screen shows above its plan.
func above(screen string) string {
	before, _, _ := strings.Cut(screen, "Here's the plan")
	return before
}

// repeated counts the lines a screen shows more than once.
func repeated(screen string) map[string]int {
	n := map[string]int{}
	for _, l := range strings.Split(screen, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			n[l]++
		}
	}
	maps.DeleteFunc(n, func(_ string, c int) bool { return c < 2 })
	return n
}

// onScreen is what a terminal shows after out: each cursor-up, return and
// clear-below removes the lines it goes back over.
func onScreen(out string) string {
	up := regexp.MustCompile(`\x1b\[(\d+)A\r\x1b\[J`)
	var rows []string
	row := ""
	for out != "" {
		loc := up.FindStringSubmatchIndex(out)
		text := out
		if loc != nil {
			text = out[:loc[0]]
		}
		for _, r := range text {
			if r == '\n' {
				rows, row = append(rows, row), ""
			} else {
				row += string(r)
			}
		}
		if loc == nil {
			break
		}
		n, _ := strconv.Atoi(out[loc[2]:loc[3]])
		// From the current row, n rows up, and everything below goes.
		keep := len(rows) - n
		if keep < 0 {
			keep = 0
		}
		rows, row = rows[:keep], ""
		out = out[loc[1]:]
	}
	return strings.Join(append(rows, row), "\n")
}

// Audit qual-bugs-3: a folder that can't be linked (here, a home folder
// kept in git) isn't offered as the first project, and the trust line
// names only the crew.
func TestFirstRunLeavesOutAFolderItCantLink(t *testing.T) {
	home := firstMachine(t)
	claudeConfig(t, home)
	exec.Command("git", "-C", home, "init", "-q").Run()
	os.MkdirAll(home+"/notes", 0o755)
	t.Chdir(home + "/notes")
	_, out, errOut := callIn("p\n")
	if strings.Contains(out, "as your first project") || !strings.Contains(flat(out), "! ~ can't be a project: it is your home folder. So it's left out.") ||
		!strings.Contains(out, "+ Trusted it in Claude Code") || strings.Contains(out, "Trusted both") {
		t.Errorf("first run:\n%s%s", out, errOut)
	}
}

// Audit qual-tests-09, acceptance 67: offline, a login check that hangs
// gives up after 3 seconds, the row says so, and the plan comes next.
func TestFirstRunGoesOnWhenTheLoginCheckHangs(t *testing.T) {
	home := firstMachine(t)
	writeStub(t, home+"/bin/claude", "#!/bin/sh\ncase \"$1 $2\" in\n  '--version '*) echo '2.1.300 (Claude Code)'; exit 0 ;;\n  'auth status') sleep 60 ;;\nesac\n")
	start := time.Now()
	_, out, _ := callIn("q\n")
	took := time.Since(start)
	if !strings.Contains(out, "! Claude Code 2.1.300. Couldn't check your login. If it asks, log in.") || !strings.Contains(out, "Here's the plan") {
		t.Errorf("first run:\n%s", out)
	}
	limit := 3*time.Second + 2*time.Second
	if raceOn {
		limit += 5 * time.Second
	}
	if took > limit {
		t.Errorf("took %s", took)
	}
}

// Audit perf-start-1: with a quick-check finding on a first run, Claude
// Code's version and login are checked once, by the screen, and the login
// line shows once.
func TestFirstRunAsksTheRuntimeOnce(t *testing.T) {
	home := firstMachine(t)
	hookAnywhereForTests = false // a quick-check finding
	calls := home + "/calls"
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+
		"case \"$1 $2\" in\n"+
		"  '--version '*) echo version >> "+calls+"; echo '2.1.300 (Claude Code)'; exit 0 ;;\n"+
		"  'auth status') echo auth >> "+calls+"; echo '{\"loggedIn\": false}'; exit 1 ;;\n"+
		"esac\n")
	_, out, errOut := callIn("p\n")
	got := readFile(t, calls)
	if strings.Count(got, "auth") != 1 || strings.Count(got, "version") != 1 {
		t.Errorf("calls:\n%s", got)
	}
	if n := strings.Count(out+errOut, "not logged in yet"); n != 1 {
		t.Errorf("the login line %d times:\n%s%s", n, out, errOut)
	}
}

// Review 11 (L11): Caps Lock doesn't make a key unknown.
func TestCapitalKeysWork(t *testing.T) {
	if k, ok := keyOf([]byte("P"), "pq"); !ok || k != 'p' {
		t.Errorf("P: %q %v", k, ok)
	}
	if k, ok := keyOf([]byte("X"), "pq"); ok {
		t.Errorf("X: %q", k)
	}
}

// Acceptance 109 to 111: the first run starts with the compact header, the
// figure beside its three lines with 256 colors or more when they fit,
// else the lines at the margin; the success screen has no figure.
func TestTheFirstRunHeader(t *testing.T) {
	run := func(cols int, colorterm, term string) string {
		t.Helper()
		home := firstMachine(t)
		os.Remove(home + "/release")
		stubAttach(t, home)
		claudeConfig(t, home)
		inRepo(t, home)
		columnsForTests = cols
		t.Cleanup(func() { columnsForTests = 0 })
		t.Setenv("FORCE_COLOR", "")
		if term != "" {
			t.Setenv("FORCE_COLOR", "1")
		}
		t.Setenv("COLORTERM", colorterm)
		t.Setenv("TERM", term)
		t.Setenv("COLORFGBG", "")
		_, out, _ := callIn("\n\n")
		return out
	}
	// The figure as the asset files have it, read here, so a wrong pick of
	// file (256 colors for truecolor, say) fails (audit qual-tests-14).
	for _, c := range []struct{ colorterm, term, art string }{{"truecolor", "xterm-256color", "truecolor.txt"}, {"", "xterm-256color", "256.txt"}} {
		raw, err := os.ReadFile(filepath.Join(repoRoot, "mascot", c.art))
		if err != nil {
			t.Fatal(err)
		}
		fig := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		out := run(70, c.colorterm, c.term)
		want := "\n  " + fig[0] + "  \x1b[1mcadrei\x1b[0m\x1b[2m, development build\x1b[0m\n" +
			"  " + fig[1] + "  \x1b[2mA team of Claude Code helpers you lead from one chat.\x1b[0m\n" +
			"  " + fig[2] + "  \x1b[2m~/code/my-app\x1b[0m\n\n"
		if !strings.HasPrefix(out, want) {
			t.Errorf("%+v: header:\n%q", c, out[:min(len(out), 400)])
		}
		if strings.Count(out, fig[0]) != 1 {
			t.Errorf("%+v: the figure showed %d times", c, strings.Count(out, fig[0]))
		}
	}
	plain := "\n  cadrei, development build\n  A team of Claude Code helpers you lead from one chat.\n  ~/code/my-app\n\n"
	for _, c := range []struct {
		cols            int
		colorterm, term string
		want            string
	}{
		{69, "truecolor", "xterm", "\n  \x1b[1mcadrei\x1b[0m\x1b[2m, development build\x1b[0m\n  \x1b[2mA team of Claude Code helpers you lead from one chat.\x1b[0m\n"},
		{80, "", "xterm", "\n  \x1b[1mcadrei\x1b[0m\x1b[2m, development build\x1b[0m\n"},
		{80, "truecolor", "", plain},
		{50, "", "", "\n  cadrei, development build\n  A team of Claude Code helpers you lead from one\n  chat.\n  ~/code/my-app\n\n"},
	} {
		if out := run(c.cols, c.colorterm, c.term); !strings.HasPrefix(out, c.want) {
			t.Errorf("%+v: no figure expected:\n%q", c, out[:min(len(out), 300)])
		}
	}
}

// Acceptance 108: cadrei --version in a terminal is the compact header,
// with the crew and how many of its members run; through a pipe, the one
// line.
func TestVersionInATerminal(t *testing.T) {
	sandbox(t)
	if _, out, _ := call("--version"); out != "cadrei, a development build\n" {
		t.Errorf("pipe: %q", out)
	}
	ttyForTests = true
	t.Cleanup(func() { ttyForTests = false })
	if _, out, _ := call("--version"); out != "  cadrei, development build\n  no crew yet · run cadrei to set one up\n" {
		t.Errorf("no crew: %q", out)
	}
	teamOf(t)
	if _, out, _ := call("--version"); !strings.HasSuffix(out, "\n  crew work · 2 members running\n  ~/.cadrei/work\n") {
		t.Errorf("running: %q", out)
	}
	must(t, "stop", "dev", "--yes")
	if _, out, _ := call("--version"); !strings.HasSuffix(out, "\n  crew work · nothing running\n  ~/.cadrei/work\n") {
		t.Errorf("stopped: %q", out)
	}
	must(t, "up", "dev/engineer")
	if _, out, _ := call("--version"); !strings.HasSuffix(out, "\n  crew work · 1 member running\n  ~/.cadrei/work\n") {
		t.Errorf("one: %q", out)
	}
}

// Acceptance 112: every row of each figure is the same width, made of half
// blocks, spaces and color codes alone, in the header's three rows. The
// lower half block draws a cell whose top pixel is transparent.
func TestTheArtIsHalfBlocks(t *testing.T) {
	for _, name := range []string{"mascot/truecolor.txt", "mascot/256.txt"} {
		raw, err := cadrei.Assets.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		rows := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(rows) != 3 || artWidth(rows[0]) > 14 {
			t.Errorf("%s: %d rows of %d", name, len(rows), artWidth(rows[0]))
		}
		color := regexp.MustCompile(`\x1b\[[0-9;]*m`)
		for i, l := range rows {
			if artWidth(l) != artWidth(rows[0]) {
				t.Errorf("%s row %d: %d columns", name, i, artWidth(l))
			}
			if rest := strings.Trim(color.ReplaceAllString(l, ""), "\u2580\u2584 "); rest != "" {
				t.Errorf("%s row %d: %q", name, i, rest)
			}
		}
	}
}

// Audit qual-tests-04, acceptance 7: a backup with files Claude Code loads
// into the orchestrator (CLAUDE.md, commands) lists them and asks, with No
// as the default: Enter doesn't open it, y does.
func TestARestoreAsksBeforeLoadingItsFiles(t *testing.T) {
	for _, tc := range []struct {
		answer string
		opens  bool
	}{{"\n", false}, {"y\nn\n", true}} {
		home := firstMachine(t)
		claudeConfig(t, home)
		app := bareRepo(t, "app")
		backup := backupOf(t, "work", app, func(src string) {
			os.WriteFile(src+"/CLAUDE.md", []byte("Be helpful.\n"), 0o644)
			os.MkdirAll(src+"/.claude/commands", 0o755)
			os.WriteFile(src+"/.claude/commands/x.md", []byte("Do x.\n"), 0o644)
		})
		_, out, errOut := callIn("r\n" + backup + "\np\n" + tc.answer)
		all := flat(out + errOut)
		for _, want := range []string{"Your backup has files Claude Code loads into your orchestrator:", "~/.cadrei/work/CLAUDE.md", "~/.cadrei/work/.claude/commands", "Open your orchestrator with them?"} {
			if !strings.Contains(all, want) {
				t.Errorf("%q: no %q in:\n%s", tc.answer, want, all)
			}
		}
		_, err := os.Stat(home + "/orch-ran")
		if opened := err == nil; opened != tc.opens {
			t.Errorf("%q: opened %v", tc.answer, opened)
		}
		if !tc.opens && (!strings.Contains(all, "Your orchestrator wasn't opened. Look at those files") || strings.Contains(all, "Its projects")) {
			t.Errorf("Enter:\n%s", all)
		}
	}
}

// Audit qual-tests-04: a backup named as a crew that's here is restored
// under the name the user gives, and the crew that's here stays as it is.
func TestARestoreUnderAnotherName(t *testing.T) {
	home := firstMachine(t)
	claudeConfig(t, home)
	must(t, "init", "work")
	os.RemoveAll(home + "/.cadrei/config")
	playbook := readFile(t, home+"/.cadrei/work/playbook.md")
	app := bareRepo(t, "app")
	backup := backupOf(t, "work", app)
	_, out, errOut := callIn("r\n" + backup + "\nw2\np\nn\n")
	if !strings.Contains(errOut, "You already have a crew called work. Name for this one?") {
		t.Errorf("no name question:\n%s%s", out, errOut)
	}
	if _, err := os.Stat(home + "/.cadrei/w2/members/dev/engineer.md"); err != nil {
		t.Errorf("w2 wasn't restored:\n%s%s", out, errOut)
	}
	if readFile(t, home+"/.cadrei/work/playbook.md") != playbook {
		t.Error("the crew that was here changed")
	}
}

// Audit qual-tests-07, acceptance 63: a restored crew's project that's
// already in the projects folder is linked where it is, not cloned again;
// the other one is cloned. The copy is under another name, where a sync
// wouldn't clone to (review of fix round 4, S5).
func TestARestoreLinksAProjectThatsHere(t *testing.T) {
	home := firstMachine(t)
	claudeConfig(t, home)
	app, blog := bareRepo(t, "app"), bareRepo(t, "blog")
	cadreis.SetProjectsDir(home + "/Developer")
	exec.Command("git", "clone", "-q", app, home+"/Developer/app-copy").Run()
	before, _ := os.Stat(home + "/Developer/app-copy")
	backup := backupOf(t, "work", app, func(src string) {
		os.WriteFile(src+"/projects.yaml", []byte("app:\n  repo: "+app+"\n  team: dev\nblog:\n  repo: "+blog+"\n  team: dev\n"), 0o644)
	})
	_, out, errOut := callIn("r\n" + backup + "\np\n\n")
	all := flat(out + errOut)
	for _, want := range []string{"found in ~/Developer/app-copy", "will be cloned", "Link the one that's here, clone the other into ~/Developer?"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in:\n%s", want, all)
		}
	}
	w, _ := cadreis.Find("work")
	if after, err := os.Stat(cadreis.Place(w, "app")); err != nil || !os.SameFile(before, after) {
		t.Errorf("app's place: %q", cadreis.Place(w, "app"))
	}
	if _, err := os.Stat(home + "/Developer/blog/.git"); err != nil {
		t.Error("blog wasn't cloned")
	}
}

// tmuxLog puts a tmux in front of the real one that logs every call, and
// takes switch-client as done (a test has no client to switch).
func tmuxLog(t *testing.T, home string) string {
	t.Helper()
	real, _ := exec.LookPath("tmux")
	dir := filepath.Join(home, "log-tmux")
	log := filepath.Join(home, "tmux.log")
	os.MkdirAll(dir, 0o755)
	writeStub(t, dir+"/tmux", "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+log+"'\ncase \"$*\" in *switch-client*) exit 0 ;; esac\nexec "+real+" \"$@\"\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return log
}

// Audit qual-tests-03, acceptance 6 and 34: a first run inside tmux on
// cadrei's own server opens the orchestrator as its own session and
// switches to it (no attach, which would nest a client); inside tmux on
// another server, it opens right here, and never touches that server.
func TestFirstRunInsideTmux(t *testing.T) {
	home := firstMachine(t)
	claudeConfig(t, home)
	os.Remove(home + "/release") // in tmux, the stub runs like a session
	log := tmuxLog(t, home)
	t.Setenv("TMUX", session.Default().Path()+",1,0")
	_, out, errOut := callIn("\n")
	for _, want := range []string{"Open your orchestrator as its own tmux session (recommended)", "Ctrl-b L back to here", "Switching to your orchestrator."} {
		if !strings.Contains(out, want) {
			t.Errorf("same server: no %q in:\n%s%s", want, out, errOut)
		}
	}
	calls := readFile(t, log)
	if !strings.Contains(calls, "switch-client -t =cadrei-main:") || strings.Contains(calls, " attach") {
		t.Errorf("same server, tmux calls:\n%s", calls)
	}
	// Every call that reaches a server names cadrei's (-V asks none).
	socket := filepath.Base(session.Default().Path())
	for _, l := range strings.Split(strings.TrimSpace(calls), "\n") {
		if l != "-V" && !strings.Contains(l, "/"+socket+" ") {
			t.Errorf("a call without cadrei's server: %s", l)
		}
	}

	home = firstMachine(t)
	claudeConfig(t, home)
	log = tmuxLog(t, home)
	t.Setenv("TMUX", "/tmp/tmux-other/default,1,0")
	code, out, errOut := callIn("\n")
	if code != 0 || !strings.Contains(flat(out), "! You're in tmux on another server, so your orchestrator opens right here this time.") {
		t.Errorf("another server: exit %d\n%s%s", code, out, errOut)
	}
	// It ran right here: with this terminal's TMUX, not in a session on
	// cadrei's server (review of fix round 4, S5).
	if ran := readFile(t, home+"/orch-ran"); !strings.Contains(ran, "TMUX=/tmp/tmux-other/default,1,0") {
		t.Errorf("the orchestrator didn't open here:\n%s", ran)
	}
	calls = readFile(t, log)
	if strings.Contains(calls, "switch-client") || strings.Contains(calls, "attach") || strings.Contains(calls, "new-session") || strings.Contains(calls, "tmux-other") {
		t.Errorf("another server, tmux calls:\n%s", calls)
	}
}

// Audit qual-tests-14, acceptance 101, 103, 108 and 111: the plan's tmux
// keys come from the server's real prefix; without Homebrew on macOS the
// tmux hint points to brew.sh; --version in a 256-color terminal draws the
// 256-color figure; and no other screen draws a figure.
func TestFirstImpressions(t *testing.T) {
	firstMachine(t)
	socket := os.Getenv("CADREI_TMUX_SOCKET")
	tmuxIn(socket, "new-session", "-d", "-s", "keep", "sleep", "300")
	tmuxIn(socket, "set-option", "-g", "prefix", "C-a")
	if _, out, _ := callIn("q\n"); !strings.Contains(out, "It keeps going if you close this window. Ctrl-a s switch, Ctrl-a d leave") {
		t.Errorf("prefix C-a:\n%s", out)
	}
	tmuxIn(socket, "kill-server")

	// In its own subtest, so the PATH without tmux ends with it.
	t.Run("no Homebrew", func(t *testing.T) {
		if goruntime.GOOS != "darwin" {
			t.Skip("the brew.sh hint is for macOS")
		}
		home := firstMachine(t)
		bin := home + "/nobrew"
		os.MkdirAll(bin, 0o755)
		os.Link(home+"/bin/claude", bin+"/claude")
		for _, tool := range []string{"git", "sh"} {
			p, _ := exec.LookPath(tool)
			os.Symlink(p, bin+"/"+tool)
		}
		t.Setenv("PATH", bin)
		if _, out, _ := callIn("q\n"); !strings.Contains(flat(out), "Install Homebrew from https://brew.sh, then run brew install tmux") {
			t.Errorf("no Homebrew, no tmux:\n%s", out)
		}
	})

	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/release", nil, 0o644)
	must(t, "init", "work")
	ttyForTests = true
	t.Cleanup(func() { ttyForTests = false })
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "")
	raw, _ := os.ReadFile(filepath.Join(repoRoot, "mascot", "256.txt"))
	row, _, _ := strings.Cut(string(raw), "\n")
	if _, out, _ := call("--version"); !strings.HasPrefix(out, "  "+row+"  ") {
		t.Errorf("--version in 256 colors:\n%q", out)
	}
	t.Setenv("COLORTERM", "truecolor")
	for name, args := range map[string][]string{"ls": {"ls"}, "help": {"help"}, "help advanced": {"help", "advanced"}, "a later open": nil} {
		_, out, errOut := call(args...)
		if strings.ContainsAny(out+errOut, "▀▄") {
			t.Errorf("%s drew a figure:\n%s", name, out)
		}
	}
	// Reattaching to an orchestrator running in tmux.
	stubAttach(t, home)
	os.Remove(home + "/release")
	must(t, "tmux", "on")
	call()
	if _, out, errOut := call(); strings.ContainsAny(out+errOut, "▀▄") {
		t.Errorf("reattach drew a figure:\n%s", out)
	}
}
