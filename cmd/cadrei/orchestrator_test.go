//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/proc"
)

// stubClaude replaces the test's stub with one that records its
// arguments, environment and folder, then waits for a release file.
// writeStub writes an executable stub. A shell writes it, not this
// process: a file this process has open for writing is busy to exec
// (ETXTBSY) while any process forked meanwhile still holds it, and a busy
// test machine forks a lot.
func writeStub(t *testing.T, path, content string) {
	t.Helper()
	// As os.WriteFile did: no folder, no stub (a test without withTmux's
	// bin folder has none on its PATH either).
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		return
	}
	cmd := exec.Command("/bin/sh", "-c", `cat > "$1.new" && chmod 755 "$1.new" && mv -f "$1.new" "$1"`, "sh", path)
	cmd.Stdin = strings.NewReader(content)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stub %s: %v %s", path, err, out)
	}
}

func stubClaude(t *testing.T, home string) {
	t.Helper()
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+"{ printf '%s\\n' \"$@\"; env; pwd; } > \""+home+"/orch-ran\"\n"+
		// A window in tmux (a member's, or the orchestrator's) shows the
		// ready footer; the orchestrator in this terminal writes nothing, as
		// its screens are compared.
		"[ -n \"$TMUX\" ] && "+stubReady+
		"i=0; while [ ! -e \""+home+"/release\" ] && [ $i -lt 100 ]; do sleep 0.05; i=$((i+1)); done\n")
}

func TestPlainCadreiOpensTheOrchestratorInThisTerminal(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	os.WriteFile(home+"/release", nil, 0o644)
	// Acceptance 24: with no setting (an upgrade from 0.2.0) it opens in
	// the terminal, and with one cadrei there's nothing to say first.
	if strings.Contains(readFile(t, c+"/cadrei.conf"), "ORCHESTRATOR") {
		t.Fatal("init wrote the tmux setting")
	}
	if out := must(t); out != "" {
		t.Errorf("an opening note with one cadrei: %q", out)
	}
	ran := readFile(t, home+"/orch-ran")
	for _, want := range []string{"--name\nwork-orchestrator\n", "--permission-mode\ndefault\n", "--append-system-prompt-file\n" + c + "/.claude/build/orchestrator.md\n",
		"CADREI_HOME=" + c + "\n", "CADREI_ORCHESTRATOR=1\n", "\n" + c + "\n"} {
		if !strings.Contains(ran, want) {
			t.Errorf("the orchestrator ran without %q:\n%s", want, ran)
		}
	}
	if strings.Contains(ran, "member-settings") || strings.Contains(ran, "CADREI_MEMBER=") {
		t.Errorf("the orchestrator got member settings or a member name:\n%s", ran)
	}
	// Acceptance 14: its own settings (the snapshot hook), the seven
	// read-only commands, and the seed last, after --.
	tail := "--settings\n" + c + "/.claude/build/orchestrator-settings.json\n--session-id\n"
	allowed := "--allowedTools\nBash(cadrei ls)\nBash(cadrei ls --json)\nBash(cadrei ls --all)\nBash(cadrei ls --json --all)\nBash(cadrei ls --all --json)\nBash(cadrei allow)\nBash(cadrei allow list)\nBash(cadrei hook skip)\n--\n(cadrei opened this chat)\n"
	if !strings.Contains(ran, tail) || !strings.Contains(ran, allowed) {
		t.Errorf("the orchestrator's command:\n%s", ran)
	}
	// Acceptance 43: and the team status hook on each prompt, synchronous
	// with a 5 second limit.
	var settings struct {
		Hooks map[string][]struct{ Hooks []map[string]any } `json:"hooks"`
	}
	json.Unmarshal([]byte(readFile(t, c+"/.claude/build/orchestrator-settings.json")), &settings)
	for ev, sub := range map[string]string{"SessionStart": "hook snapshot 2>/dev/null; exit 0", "UserPromptSubmit": "hook team-status 2>/dev/null; exit 0"} {
		if g := settings.Hooks[ev]; len(g) != 1 || len(g[0].Hooks) != 1 || !strings.HasSuffix(g[0].Hooks[0]["command"].(string), sub) || g[0].Hooks[0]["async"] != nil {
			t.Errorf("%s: %+v", ev, g)
		}
	}
	if settings.Hooks["UserPromptSubmit"][0].Hooks[0]["timeout"] != 5.0 {
		t.Error("the team status hook's time limit")
	}
	// Acceptance 15: the prompt holds no snapshot.
	if p := readFile(t, c+"/.claude/build/orchestrator.md"); !strings.Contains(p, "This session is the cadrei orchestrator") || !strings.Contains(p, "You are the orchestrator of the crew `work` (`~/.cadrei/work`).") || strings.Contains(p, "## Right now") {
		t.Errorf("prompt %q", p)
	}
	if _, err := os.Stat(c + "/.claude/build/orchestrator.lock"); err == nil {
		t.Error("the lock outlived the orchestrator")
	}
}

func TestOneOrchestratorAtATime(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	must(t, "init", "work")
	lock := home + "/.cadrei/work/.claude/build/orchestrator.lock"
	done := make(chan int)
	go func() { code, _, _ := call(); done <- code }()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(lock); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if out := must(t, "ls"); !strings.Contains(out, "\nOrchestrator  open in a terminal since ") {
		t.Errorf("ls while it runs:\n%s", out)
	}
	// Acceptance 20: the 4.4 error, with the way out.
	if code, _, errOut := call(); code != 1 || !strings.HasPrefix(errOut, "cadrei: your orchestrator is already open in another terminal (") ||
		!strings.HasSuffix(errOut, ").\n  Switch to that one, or close it first.\n") {
		t.Errorf("a second orchestrator: %d %q", code, errOut)
	}
	os.WriteFile(home+"/release", nil, 0o644)
	if code := <-done; code != 0 {
		t.Errorf("the orchestrator exited %d", code)
	}
	if out := must(t, "ls"); !strings.Contains(out, "\nOrchestrator  not running · open it with cadrei\n") {
		t.Errorf("ls after:\n%s", out)
	}
	// A lock left by a process that is gone is stale.
	os.WriteFile(lock, []byte(`{"pid": 999999, "start": "1", "command": "claude", "mode": "terminal"}`), 0o600)
	if code, _, errOut := call(); code != 0 {
		t.Errorf("a stale lock blocked cadrei: %d %q", code, errOut)
	}
}

func TestPlainCadreiFromAProjectAndNotes(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/release", nil, 0o644)
	must(t, "init", "work")
	os.MkdirAll(home+"/Developer/app", 0o755)
	register(t, home, "work", "app:\n  repo: me/app\n  path: ~/Developer/app\n")
	t.Chdir(home + "/Developer/app")
	out := must(t)
	if strings.Contains(out, "Opening your default") {
		t.Errorf("a note from inside a linked project: %q", out)
	}
	if ran := readFile(t, home+"/orch-ran"); !strings.Contains(ran, "CADREI_OPENED_PROJECT=app\n") || !strings.Contains(ran, "CADREI_OPENED_FROM="+home+"/Developer/app\n") {
		t.Errorf("where it was opened from was not passed on:\n%s", ran)
	}
	// An unlinked git repository, and a folder that looks like a cadrei.
	os.MkdirAll(home+"/src/repo", 0o755)
	exec.Command("git", "-C", home+"/src/repo", "init", "-q").Run()
	t.Chdir(home + "/src/repo")
	// The orchestrator offers it; cadrei says nothing.
	if out := must(t); strings.Contains(out, "project") || strings.Contains(out, "link") {
		t.Errorf("unlinked repo: %q", out)
	}
	os.MkdirAll(home+"/old/demo/personas", 0o755)
	os.WriteFile(home+"/old/demo/projects.yaml", nil, 0o644)
	os.WriteFile(home+"/old/demo/cadrei.conf", []byte("touch "+home+"/marker\n"), 0o644)
	t.Chdir(home + "/old/demo")
	if out := must(t); !strings.Contains(flat(out), "This folder looks like a cadre from 0.1.x. To bring it in, tell your orchestrator: bring in my old cadre from ~/old/demo") {
		t.Errorf("look-alike: %q", out)
	}
	if _, err := os.Stat(home + "/marker"); err == nil {
		t.Error("a look-alike's cadrei.conf was run")
	}
	t.Setenv("CADREI_MEMBER", "x")
	if code, _, errOut := call(); code != 1 || !strings.Contains(errOut, "team members can't start the orchestrator") {
		t.Errorf("in a member: %d %q", code, errOut)
	}
}

func TestOrchestratorInTmux(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "work")
	running := "+ Your orchestrator is running in tmux. Open it anytime with cadrei\n"
	if out := must(t, "--tmux"); out != running {
		t.Fatalf("--tmux: %q", out)
	}
	if tmuxIn(socket, "show-options", "-qv", "-t", "=cadrei-work:", "@cadrei_role") != "orchestrator" {
		t.Error("the session is not marked as the orchestrator")
	}
	if out := must(t, "--tmux"); out != running || strings.Count(tmuxIn(socket, "list-sessions", "-F", "#S"), "cadrei-work") != 1 {
		t.Errorf("second --tmux: %q", out)
	}
	if out := must(t, "ls"); !strings.Contains(out, "\nOrchestrator  open in tmux since ") || !strings.Contains(out, " · reopen with cadrei\n") {
		t.Errorf("ls:\n%s", out)
	}
	// With one in tmux, plain cadrei goes to it.
	if out := must(t); out != "  Your orchestrator is running in tmux. Opening it there.\n"+running {
		t.Errorf("plain cadrei with one in tmux: %q", out)
	}
	refused(t, "run cadrei attach in a terminal.", "attach")
	// stop leaves the orchestrator alone unless asked.
	must(t, "up", "dev/engineer")
	must(t, "stop", "--yes")
	if tmuxIn(socket, "has-session", "-t", "=cadrei-work") != "" {
		t.Error("stop stopped the orchestrator")
	}
	must(t, "stop", "--all", "--yes")
	if tmuxIn(socket, "has-session", "-t", "=cadrei-work") != "" {
		t.Error("stop --all stopped the orchestrator")
	}
}

// The orchestrator runs with the cadrei's PERMISSION_MODE, and a mode the
// runtime lacks is refused before it starts.
func TestOrchestratorPermissionMode(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/release", nil, 0o644)
	must(t, "init", "work")
	os.WriteFile(home+"/.cadrei/work/cadrei.conf", []byte("PERMISSION_MODE=yolo\n"), 0o644)
	if code, _, errOut := call(); code != 1 || !strings.Contains(errOut, "has no permission mode yolo") {
		t.Errorf("yolo: %d %q", code, errOut)
	}
	os.WriteFile(home+"/.cadrei/work/cadrei.conf", []byte("PERMISSION_MODE=plan\n"), 0o644)
	must(t)
	if ran := readFile(t, home+"/orch-ran"); !strings.Contains(ran, "--permission-mode\nplan\n") {
		t.Errorf("the orchestrator ran without the cadrei's mode:\n%s", ran)
	}
}

// plantLock writes a lock as a member's shell could: a live pid with its
// real start time, pointing at a session of the planter's choosing.
func plantLock(t *testing.T, path string, pid int, session string) {
	t.Helper()
	info, err := proc.Of(pid)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"pid": pid, "start": info.Start, "command": info.Command, "mode": "tmux", "session": session, "since": time.Now()})
	os.WriteFile(path, raw, 0o600)
}

func TestATmuxLockIsCheckedBeforeAttaching(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/release", nil, 0o644)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	lock := c + "/.claude/build/orchestrator.lock"
	os.MkdirAll(c+"/.claude/build", 0o755)
	pane := func(target string) int {
		pid, _ := strconv.Atoi(tmuxIn(socket, "display-message", "-p", "-t", target, "#{pane_pid}"))
		return pid
	}
	// Another session, a session named like the orchestrator but not
	// marked as one, and the real orchestrator session with another
	// process's pid: none is attached to.
	tmuxIn(socket, "new-session", "-d", "-s", "evil", "-n", "orchestrator", "sleep", "60")
	tmuxIn(socket, "new-session", "-d", "-s", "cadrei-work", "-n", "orchestrator", "sleep", "60")
	for _, plant := range []struct{ name, session string }{{"another session", "evil"}, {"an unmarked session", "cadrei-work"}} {
		plantLock(t, lock, pane("="+plant.session+":"), plant.session)
		out := must(t)
		if strings.Contains(out, "already running") {
			t.Errorf("%s was taken for the orchestrator: %q", plant.name, out)
		}
	}
	tmuxIn(socket, "set-option", "-t", "=cadrei-work:", "@cadrei_role", "orchestrator")
	tmuxIn(socket, "set-option", "-t", "=cadrei-work:", "@cadrei_home", c)
	// The marked session with another process's pid: the lock is not
	// trusted, and removed (cadrei then finds the session itself).
	plantLock(t, lock, pane("=evil:"), "cadrei-work")
	must(t)
	if _, err := os.Stat(lock); err == nil {
		t.Error("a lock naming another process was left")
	}
}

func TestAnOrchestratorInTmuxWithoutItsLockStillCounts(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	must(t, "init", "work")
	must(t, "--tmux")
	os.Remove(home + "/.cadrei/work/.claude/build/orchestrator.lock")
	if out := must(t); !strings.Contains(out, "Your orchestrator is running in tmux. Opening it there.") {
		t.Errorf("a second orchestrator started: %q", out)
	}
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(home + "/orch-ran"); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the tmux orchestrator did not run")
}

func TestTwoRunsStartOneOrchestrator(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	// The login check on each open asks too: those aren't starts.
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+"echo x >> \""+home+"/starts\"\n"+
		"i=0; while [ ! -e \""+home+"/release\" ] && [ $i -lt 100 ]; do sleep 0.05; i=$((i+1)); done\n")
	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i := range codes {
		wg.Add(1)
		go func() { defer wg.Done(); codes[i], _, _ = call() }()
	}
	time.Sleep(1500 * time.Millisecond)
	os.WriteFile(home+"/release", nil, 0o644)
	wg.Wait()
	if n := strings.Count(readFile(t, home+"/starts"), "x"); n != 1 {
		t.Errorf("%d orchestrators started (exit codes %v)", n, codes)
	}
	if _, err := os.Stat(home + "/.cadrei/work/.claude/build/orchestrator.lock"); err == nil {
		t.Error("the lock outlived the orchestrator")
	}
}

// cadrei waits through Ctrl-C, but the orchestrator gets the default
// disposition: an ignored signal would stay ignored across exec.
func TestTheOrchestratorKeepsDefaultSignals(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	writeStub(t, home+"/bin/claude", "#!/bin/sh\nkill -INT $$\nsleep 0.2\necho survived > \""+home+"/survived\"\n")
	call()
	if _, err := os.Stat(home + "/survived"); err == nil {
		t.Error("SIGINT was ignored in the orchestrator")
	}
}

// Acceptance 35: after the user leaves tmux, cadrei says the orchestrator
// still runs, only when its session is still there. A stub tmux stands in
// for the client: it ends at once, as a detach does, and with KILL set it
// ends the session first.
func TestLeavingTmuxSaysTheOrchestratorStillRuns(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	must(t, "--tmux", "--detach")
	real, _ := exec.LookPath("tmux")
	stub := filepath.Join(home, "stub-tmux")
	os.MkdirAll(stub, 0o755)
	writeStub(t, stub+"/tmux", "#!/bin/sh\n[ \"$3\" = attach ] || exec "+real+" \"$@\"\n[ -z \"$KILL\" ] || "+real+" -S \"$2\" kill-session -t =cadrei-work\nexit 0\n")
	t.Setenv("PATH", stub+":"+os.Getenv("PATH"))
	ttyForTests = true
	t.Cleanup(func() { ttyForTests = false })
	line := "Your orchestrator is still running. Run cadrei to come back."
	if code, out, errOut := call("attach"); code != 0 || !strings.Contains(out, line) {
		t.Errorf("after leaving: exit %d %q %q", code, out, errOut)
	}
	t.Setenv("KILL", "1")
	if code, out, _ := call("attach"); code != 0 || strings.Contains(out, line) {
		t.Errorf("after the session ended: exit %d %q", code, out)
	}
}

// Acceptance 20, 21 and 25: the machine setting, the flags that win over
// it for one run, and their conflicts.
func TestTheTmuxSettingAndFlags(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	// The stub stays up until release exists: in tmux it runs like a
	// session, in the terminal it ends once released.
	stubClaude(t, home)
	must(t, "init", "work")
	if out := must(t, "tmux"); out != "Your orchestrator opens in this terminal.\n" {
		t.Errorf("tmux: %q", out)
	}
	t.Setenv("CADREI_MEMBER", "x")
	refused(t, "team members can't", "tmux", "on")
	os.Unsetenv("CADREI_MEMBER")
	// A setting of the cadrei, committed with it.
	if out := must(t, "tmux", "on"); out != "+ Your orchestrator opens in tmux from now on.\n" || !strings.Contains(readFile(t, home+"/.cadrei/work/cadrei.conf"), "\nORCHESTRATOR=tmux\n") ||
		!strings.Contains(gitLog(t, home+"/.cadrei/work"), "Open the orchestrator in tmux") {
		t.Errorf("tmux on: %q", out)
	}
	if out := must(t, "tmux"); out != "Your orchestrator opens in tmux.\n" {
		t.Errorf("tmux: %q", out)
	}
	running := "+ Your orchestrator is running in tmux. Open it anytime with cadrei\n"
	if out := must(t); out != running || tmuxIn(socket, "show-options", "-qv", "-t", "=cadrei-work:", "@cadrei_role") != "orchestrator" {
		t.Errorf("plain cadrei with tmux on: %q", out)
	}
	// Running again finds it, and starts no second one.
	if out := must(t); out != running || strings.Count(tmuxIn(socket, "list-sessions", "-F", "#S"), "cadrei-work") != 1 {
		t.Errorf("again: %q", out)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--tmux", "--no-tmux"}, "cadrei: pick one: --tmux or --no-tmux.\n"},
		{[]string{"--no-tmux", "--detach"}, "cadrei: --detach only works with tmux.\n  Drop --no-tmux, or drop --detach.\n"},
	} {
		if code, _, errOut := call(tc.args...); code != 1 || errOut != tc.want {
			t.Errorf("%v: exit %d %q", tc.args, code, errOut)
		}
	}
	tmuxIn(socket, "kill-server")
	// The stub keeps no transcript, so each start is a fresh chat.
	fresh := "  Starting a fresh chat. The last one couldn't be found.\n"
	if out := must(t, "--detach"); out != fresh+running {
		t.Errorf("--detach: %q", out)
	}
	tmuxIn(socket, "kill-server")
	// --no-tmux opens in this terminal for one run.
	os.WriteFile(home+"/release", nil, 0o644)
	os.Remove(home + "/orch-ran")
	if out := must(t, "--no-tmux"); out != fresh || !strings.Contains(readFile(t, home+"/orch-ran"), "--name\nwork-orchestrator\n") {
		t.Errorf("--no-tmux: %q", out)
	}
	if tmuxIn(socket, "has-session", "-t", "=cadrei-work") == "" {
		t.Error("--no-tmux started it in tmux")
	}
	// With the setting on and tmux gone, it opens here this time.
	bin := filepath.Join(home, "notmux")
	os.MkdirAll(bin, 0o755)
	os.Link(home+"/bin/claude", bin+"/claude")
	git, _ := exec.LookPath("git")
	os.Symlink(git, bin+"/git")
	t.Setenv("PATH", bin)
	if out := must(t); !strings.Contains(out, "  ! tmux isn't installed, so your orchestrator opens here this time.\n") {
		t.Errorf("without tmux: %q", out)
	}
	if out := must(t, "tmux", "off"); out != "+ Your orchestrator opens in this terminal from now on.\n" {
		t.Errorf("tmux off: %q", out)
	}
}

// snapshotText runs cadrei hook snapshot for a session start from source
// and returns the text it adds to the orchestrator's context.
func snapshotText(t *testing.T, source string) string {
	t.Helper()
	code, out, errOut := callIn(`{"session_id":"x","source":"`+source+`"}`, "hook", "snapshot")
	if code != 0 || errOut != "" {
		t.Fatalf("hook snapshot: exit %d %q", code, errOut)
	}
	if out == "" {
		return ""
	}
	var v struct {
		HookSpecificOutput struct{ AdditionalContext string } `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	return v.HookSpecificOutput.AdditionalContext
}

// Audit perf-hooks-3 and perf-start-5: the snapshot never looks through
// the projects folder for a missing project (a git run per folder there),
// since it never shows what that finds.
func TestTheSnapshotDoesntSearchTheProjectsFolder(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	for i := 0; i < 5; i++ {
		dir := fmt.Sprintf("%s/Developer/r%d", home, i)
		os.MkdirAll(dir, 0o755)
		exec.Command("git", "-C", dir, "init", "-q").Run()
	}
	must(t, "project", "dir", "~/Developer")
	register(t, home, "work", "gone:\n  repo: https://example.com/me/gone.git\n")
	log := filepath.Join(t.TempDir(), "git.log")
	shim := t.TempDir()
	real, _ := exec.LookPath("git")
	writeStub(t, shim+"/git", "#!/bin/sh\nprintf '%s\\n' \"$*\" >> "+log+"\nexec "+real+" \"$@\"\n")
	t.Setenv("PATH", shim+":"+os.Getenv("PATH"))
	t.Setenv("CADREI_HOME", home+"/.cadrei/work")
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	if s := snapshotText(t, "startup"); !strings.Contains(s, "- Projects: gone (not here)\n") {
		t.Errorf("snapshot:\n%s", s)
	}
	if raw, _ := os.ReadFile(log); strings.Contains(string(raw), "remote.origin.url") {
		t.Errorf("the snapshot searched the projects folder:\n%s", raw)
	}
}

// Audit perf-start-3: the orchestrator's tmux start is watched as a
// member's is: a command that ends after half a second is caught, and its
// last line is quoted.
func TestTheOrchestratorsTmuxStartIsWatched(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+"sleep 0.9\necho 'There is an issue with the selected model (nope).'\nexit 1\n")
	code, out, errOut := call("--tmux", "--detach")
	if code == 0 || !strings.Contains(flat(errOut), "didn't start: there is an issue with the selected model (nope).") || strings.Contains(out, "running in tmux") {
		t.Errorf("exit %d\n%s%s", code, out, errOut)
	}
}

// Review of fix round 4 (S3): a first open on a screen with no footer that
// writes no transcript (Claude Code's text style, login or trust screen)
// doesn't wait the whole 3 seconds: a screen the runtime knows waits on
// the user is going.
func TestTheOrchestratorOnALoginScreenOpensSoon(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "work")
	// A long wait, which a screen that waits ends early: on a busy
	// machine too.
	waitForTests = 30 * time.Second
	t.Cleanup(func() { waitForTests = 0 })
	for _, screen := range []string{"Choose the text style that looks best with your terminal", "Select login method:", "Quick safety check: Is this a project you created or one you trust?"} {
		writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+"echo '"+screen+"'\nexec sleep 300\n")
		start := time.Now()
		must(t, "--tmux", "--detach")
		if took := time.Since(start); took > 15*time.Second {
			t.Errorf("%s: took %s", screen, took)
		}
		tmuxIn(socket, "kill-server")
	}
}

// Review of fix round 6 (still screen): a command that shows a screen
// that waits, and ends soon after, didn't start.
func TestAnOrchestratorThatShowsALoginScreenAndEndsDidntStart(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	waitForTests = 30 * time.Second
	t.Cleanup(func() { waitForTests = 0 })
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+"echo 'Select login method:'\nsleep 0.6\nexit 1\n")
	if code, out, errOut := call("--tmux", "--detach"); code != 1 || !strings.Contains(strings.ToLower(errOut), "your orchestrator didn't start") || strings.Contains(out, "running in tmux") {
		t.Errorf("exit %d\n%s%s", code, out, errOut)
	}
}

// Review of fix round 5 (S3 regression): a command that prints a line and
// ends within the start check didn't start, however still its screen held
// before it ended.
func TestAnOrchestratorThatPrintsAndEndsDidntStart(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "work")
	// A long wait, so the command ends within it on a busy machine too.
	waitForTests = 30 * time.Second
	t.Cleanup(func() { waitForTests = 0 })
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+"echo 'Something went wrong'\nsleep 1\nexit 1\n")
	code, out, errOut := call("--tmux", "--detach")
	if low := strings.ToLower(errOut); code != 1 || !strings.Contains(low, "your orchestrator didn't start") || !strings.Contains(low, "something went wrong") || strings.Contains(out, "running in tmux") {
		t.Errorf("exit %d\n%s%s", code, out, errOut)
	}
	if tmuxIn(socket, "has-session", "-t", "=cadrei-work") == "" {
		t.Error("its session is still there")
	}
}

// Review of fix round 5 (S3 regression): a resume the runtime refuses,
// with a line on the screen before it ends, starts a fresh chat.
func TestARefusedResumeInTmuxStartsAFreshChat(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	stubClaude(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	must(t, "--tmux", "--detach")
	tmuxIn(socket, "kill-server")
	var r struct{ ID string }
	json.Unmarshal([]byte(readFile(t, c+"/.claude/build/sessions/work-orchestrator.json")), &r)
	dir := home + "/.claude/projects/" + regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(c, "-")
	os.MkdirAll(dir, 0o755)
	raw, _ := os.ReadFile(filepath.Join(fixtures, "answered-seed.jsonl"))
	os.WriteFile(dir+"/"+r.ID+".jsonl", raw, 0o600)
	os.Remove(home + "/orch-ran")
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+
		"case \" $* \" in *' --resume '*) echo 'No conversation found'; sleep 1; exit 1 ;; esac\n"+
		"printf '%s\\n' \"$@\" > \""+home+"/orch-ran\"\n"+stubReady+"exec sleep 300\n")
	code, out, errOut := call("--tmux", "--detach")
	if code != 0 || !strings.Contains(out, "running in tmux") {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	if ran := readFile(t, home+"/orch-ran"); strings.Contains(ran, "--resume") || !strings.Contains(ran, "--session-id\n") {
		t.Errorf("no fresh chat:\n%s", ran)
	}
	if tmuxIn(socket, "has-session", "-t", "=cadrei-work") != "" {
		t.Error("its session isn't running")
	}
}

// Acceptance 15: the snapshot has every field, and says first run while the
// first run's state is set.
func TestHookSnapshot(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	os.MkdirAll(home+"/Developer/app", 0o755)
	register(t, home, "work", "app:\n  repo: me/app\n  team: dev\n  path: ~/Developer/app\n")
	c := home + "/.cadrei/work"
	if s := snapshotText(t, "startup"); s != "" {
		t.Errorf("outside the orchestrator: %q", s)
	}
	cadreis.SetState(cadreis.FirstTaskPending, "yes")
	for k, v := range map[string]string{"CADREI_HOME": c, "CADREI_ORCHESTRATOR": "1", "CADREI_OPENS_IN": "tmux",
		"CADREI_OPENED_FROM": home + "/Developer/app", "CADREI_OPENED_PROJECT": "app"} {
		t.Setenv(k, v)
	}
	s := snapshotText(t, "startup")
	for _, want := range []string{"## Right now\n\n- Crew: work (~/.cadrei/work)\n- Opened by: cadrei, at ", "\n- First run: yes\n",
		"\n- Orchestrator opens in: tmux (prefix C-b; the user is not inside another tmux)\n",
		"\n- Opened from: the project app (~/Developer/app)\n", "\n- Projects: app (present, team dev)\n",
		"\n- Teams: dev (engineer, reviewer); running: none\n", "\n- Members: none running\n", "\n- Stopped by safeguards: none\n",
		"\n- One-time grants left over: 0\n", "\n- Every-chat hook: off (never offered)\n", "\n- Git identity: set\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in:\n%s", want, s)
		}
	}
	// A resumed chat gets one too, still a first run until a member starts.
	if s := snapshotText(t, "resume"); !strings.Contains(s, "- First run: yes\n") {
		t.Errorf("on resume:\n%s", s)
	}
	must(t, "up", "dev/engineer")
	if s := snapshotText(t, "resume"); !strings.Contains(s, "- First run: no\n") || !strings.Contains(s, "- Members: dev: engineer starting\n") {
		t.Errorf("after a member started:\n%s", s)
	}
	must(t, "stop", "dev/engineer")
	t.Setenv("CADREI_OPENED_PROJECT", "")
	t.Setenv("CADREI_OPENED_FROM", home)
	t.Setenv("CADREI_OPENS_IN", "terminal")
	empty := filepath.Join(home, "no-identity")
	os.WriteFile(empty, nil, 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	cadreis.SetState(cadreis.HookOffer, "skipped")
	s = snapshotText(t, "startup")
	for _, want := range []string{"- First run: no\n", "- Orchestrator opens in: this terminal\n", "- Opened from: your home folder\n",
		"- Every-chat hook: off (the user said no)\n", "- Git identity: missing\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in:\n%s", want, s)
		}
	}
	t.Setenv("CADREI_MEMBER", "work-dev-engineer")
	if s := snapshotText(t, "startup"); s != "" {
		t.Errorf("in a member: %q", s)
	}
}

// Acceptance 14 and 68: a resumed orchestrator gets the seed only with
// news, and never on top of a seed it hasn't answered.
func TestTheSeedOnResume(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/release", nil, 0o644)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	must(t)
	rec := readFile(t, c+"/.claude/build/sessions/work-orchestrator.json")
	var r struct{ ID, Dir string }
	json.Unmarshal([]byte(rec), &r)
	dir := home + "/.claude/projects/" + regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(c, "-")
	os.MkdirAll(dir, 0o755)
	put := func(fixture string) {
		raw, _ := os.ReadFile(filepath.Join(fixtures, fixture))
		os.WriteFile(dir+"/"+r.ID+".jsonl", raw, 0o600)
	}
	seeded := func() bool { return strings.Contains(readFile(t, home+"/orch-ran"), "--\n(cadrei opened this chat)\n") }
	// Answered: every open gets a seed, so the orchestrator greets the
	// user, with or without news.
	put("answered-seed.jsonl")
	must(t)
	if ran := readFile(t, home+"/orch-ran"); !strings.Contains(ran, "--resume\n"+r.ID+"\n") || !seeded() {
		t.Errorf("resume:\n%s", ran)
	}
	// The last seed is still unanswered: no second one.
	put("unanswered-seed.jsonl")
	must(t)
	if seeded() {
		t.Error("a second seed on top of an unanswered one")
	}
}
