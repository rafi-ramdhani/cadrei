//go:build !windows

package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// ownTerminal has cadrei run as if from the user's own terminal: under no
// Claude Code at all (an orphaned process stands in for it).
func ownTerminal(t *testing.T) {
	t.Helper()
	runAs(t, exec.Command("sh", "-c", "sleep 60 >/dev/null 2>&1 & echo $!"))
}

// runAs has cadrei run as the process cmd prints the pid of on its first
// line: under whatever that one runs under. cmd keeps running.
func runAs(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	line, _ := bufio.NewReader(out).ReadString('\n')
	pid, _ := strconv.Atoi(strings.TrimSpace(line))
	if pid == 0 {
		t.Fatalf("no process: %q", line)
	}
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
	selfPID = func() int { return pid }
	t.Cleanup(func() { selfPID = os.Getpid })
}

// promptMember starts role in team dev with a stub Claude Code that shows
// its ready footer, then a recorded prompt, and notes in home/key the one
// key it gets. With home/stay, the prompt stays after the key.
func promptMember(t *testing.T, home, role, fixture string) {
	t.Helper()
	abs := filepath.Join(answerFixtures, fixture)
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+
		"[ -n \"$TMUX\" ] || exit 0\n"+
		stubReady+"sleep 1\nprintf '\\033[H\\033[2J'\ncat '"+abs+"'\n"+
		"stty raw -echo\nk=$(dd bs=1 count=1 2>/dev/null)\n"+
		// Any key after it (an Enter, say) in the next half second.
		"stty min 0 time 5\ne=$(dd bs=1 count=8 2>/dev/null)\nstty sane\n"+
		"printf '%s' \"$e\" > \""+home+"/extra-"+role+"\"\n"+
		"printf '%s' \"$k\" > \""+home+"/key-"+role+"\"\n"+
		"[ -e \""+home+"/stay\" ] || printf '\\033[H\\033[2J'\nexec sleep 300\n")
	must(t, "up", "dev/"+role)
	t.Cleanup(func() { session.Default().KillSession("cadrei-work-dev") })
	screen := ""
	for i := 0; i < 100 && !strings.Contains(screen, "Esc to cancel"); i++ {
		time.Sleep(50 * time.Millisecond)
		screen = session.Default().Screen(session.SessionName("work", "dev"), role)
	}
	if !strings.Contains(screen, "Esc to cancel") {
		t.Fatalf("the prompt didn't show:\n%s", screen)
	}
}

// request feeds role a permission request and returns its code.
func request(t *testing.T, c, role, payload string) string {
	t.Helper()
	feed(t, c, "work-dev-"+role, payload)
	st := session.ReadState(c + "/.claude/build/state/work-dev-" + role + ".json")
	if st == nil || st.Request == nil {
		t.Fatalf("no request: %+v", st)
	}
	return st.Request.Code
}

const (
	mkdirRequest  = `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"mkdir <work>/s4a"}}`
	uploadRequest = `{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Upload the test file to the signing site?","options":[{"label":"Yes"},{"label":"No"}],"multiSelect":false}]}}`
)

// key is the one key the stub member got, after up to two seconds, or
// what it got with more after it.
func key(t *testing.T, home, role string) string {
	t.Helper()
	for i := 0; i < 40; i++ {
		if raw, err := os.ReadFile(home + "/key-" + role); err == nil {
			extra, _ := os.ReadFile(home + "/extra-" + role)
			return string(raw) + string(extra)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ""
}

// 0.2.2 (6.3): yes presses 1 in the recorded Bash prompt (never 3, which
// switches to auto mode), and says what it allowed.
func TestAnswerYes(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "bash-prompt.txt")
	code := request(t, c, "engineer", mkdirRequest)
	ownTerminal(t)
	out, errOut := "", ""
	if n, o, e := call("answer", code, "yes"); n != 0 {
		t.Fatalf("exit %d: %s%s", n, o, e)
	} else {
		out, errOut = o, e
	}
	if out != "+ Allowed once: the engineer in dev can run mkdir <work>/s4a.\n" || errOut != "" {
		t.Errorf("out %q %q", out, errOut)
	}
	if k := key(t, home, "engineer"); k != "1" {
		t.Errorf("key %q", k)
	}
}

// No presses the No option, then sets the member idle with nothing to
// answer: no hook says so when a no stops its turn.
func TestAnswerNo(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "bash-prompt.txt")
	code := request(t, c, "engineer", mkdirRequest)
	ownTerminal(t)
	if n, out, errOut := call("answer", code, "no"); n != 0 || flat(out) != "+ Told the engineer in dev no. It won't run mkdir <work>/s4a, and it's waiting for what to do instead." {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	if k := key(t, home, "engineer"); k != "4" {
		t.Errorf("key %q", k)
	}
	if st := session.ReadState(c + "/.claude/build/state/work-dev-engineer.json"); st == nil || st.State != session.Idle || st.Request != nil || st.WaitingFor != "" {
		t.Errorf("state after no: %+v", st)
	}
}

// A question's pick presses its number, and says the answer.
func TestAnswerAQuestion(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "question.txt")
	code := request(t, c, "engineer", uploadRequest)
	ownTerminal(t)
	if n, out, errOut := call("answer", code, "2"); n != 0 || out != "+ Answered the engineer in dev: 2. No\n" {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	if k := key(t, home, "engineer"); k != "2" {
		t.Errorf("key %q", k)
	}
}

// A prompt still on the screen after the key says so, and fails.
func TestAnswerSaysWhenThePromptStays(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	os.WriteFile(home+"/stay", nil, 0o644)
	promptMember(t, home, "engineer", "bash-prompt.txt")
	code := request(t, c, "engineer", mkdirRequest)
	ownTerminal(t)
	answerWait = 300 * time.Millisecond
	t.Cleanup(func() { answerWait = 2 * time.Second })
	n, out, errOut := call("answer", code, "yes")
	if n != 1 || out != "" || flat(errOut) != "! I pressed your answer, but the prompt is still on the engineer's screen. Check its window: cadrei attach dev" {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
}

// Nothing is pressed for a request the screen doesn't show (it changed),
// a code that names nothing, a choice that can't answer it, or a run in a
// member.
func TestAnswerPressesNothingWhenItCant(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "bash-prompt.txt")
	changed := request(t, c, "engineer", `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf <work>"}}`)
	ownTerminal(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{changed, "yes"}, "cadrei: I couldn't find that prompt on the engineer's screen, so I didn't press anything. Answer it in its window: cadrei attach dev"},
		{[]string{"zz9", "yes"}, "cadrei: nothing's waiting under zz9 anymore. Run cadrei answer to see what's waiting now."},
		{[]string{"ZZ9", "yes"}, "cadrei: nothing's waiting under ZZ9 anymore. Run cadrei answer to see what's waiting now."},
		{[]string{changed, "1"}, "cadrei: " + changed + " is a yes-or-no prompt. Use cadrei answer " + changed + " yes or cadrei answer " + changed + " no."},
		{[]string{changed}, "cadrei: use cadrei answer to see who's waiting on you, or cadrei answer <code> yes|no|<n> to answer."},
	} {
		if n, out, errOut := call(append([]string{"answer"}, tc.args...)...); n != 1 || out != "" || flat(errOut) != tc.want {
			t.Errorf("%v: exit %d, %q %q", tc.args, n, out, errOut)
		}
	}
	t.Setenv("CADREI_MEMBER", "work-dev-reviewer")
	if n, _, errOut := call("answer", changed, "yes"); n != 1 || flat(errOut) != "cadrei: team members can't answer for you. Ask the user to do it from the orchestrator." {
		t.Errorf("member: %d %q", n, errOut)
	}
	os.Unsetenv("CADREI_MEMBER")
	// In a member's window, with its variables dropped.
	pid, err := session.Default().PanePID(session.SessionName("work", "dev"), "engineer")
	if err != nil {
		t.Fatal(err)
	}
	selfPID = func() int { return pid }
	if n, _, errOut := call("answer"); n != 1 || !strings.Contains(flat(errOut), "team members can't answer for you. This runs in the window of the member work-dev-engineer.") {
		t.Errorf("member window: %d %q", n, errOut)
	}
	if _, err := os.Stat(home + "/key-engineer"); err == nil {
		t.Error("a key was pressed")
	}
}

// cadrei answer with no code: who's waiting, with the line that answers
// each, oldest first; or nobody.
func TestAnswerListsWhosWaiting(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	ownTerminal(t)
	if n, out, _ := call("answer"); n != 0 || out != "Nobody's waiting on you right now.\n" {
		t.Errorf("nobody: %d %q", n, out)
	}
	must(t, "up", "dev")
	eng := request(t, c, "engineer", mkdirRequest)
	rev := request(t, c, "reviewer", uploadRequest)
	want := "Waiting on you:\n\n" +
		"  " + eng + "  The engineer in dev wants to run a command:\n" +
		"         mkdir <work>/s4a\n" +
		"       Allow it once:  cadrei answer " + eng + " yes\n" +
		"       Say no:         cadrei answer " + eng + " no\n\n" +
		"  " + rev + "  The reviewer in dev asks: Upload the test file to the signing site?\n" +
		"         1. Yes\n" +
		"         2. No\n" +
		"       Answer:         cadrei answer " + rev + " 1\n"
	if n, out, errOut := call("answer"); n != 0 || out != want {
		t.Errorf("exit %d:\n%s\nwant:\n%s%s", n, out, want, errOut)
	}
}

// 0.2.2 (6.5, check 4): cadrei answer runs under this crew's orchestrator
// (its settings file), or under no Claude Code at all. Under another
// crew's orchestrator, or one with none of cadrei's settings (the
// every-chat orchestrator, another chat), it refuses.
func TestAnswerRunsOnlyUnderItsOwnOrchestrator(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	must(t, "init", "life")
	must(t, "use", "work")
	fake := filepath.Join(home, "fake")
	os.MkdirAll(fake, 0o755)
	writeStub(t, fake+"/claude", "#!/bin/sh\nsleep 60 >/dev/null 2>&1 &\necho $!\nwait\n")
	under := func(args ...string) {
		t.Helper()
		runAs(t, exec.Command(fake+"/claude", args...))
	}
	own := home + "/.cadrei/work/.claude/build/orchestrator-settings.json"
	refused := "cadrei: cadrei answer runs only in the orchestrator cadrei opened, or in your own terminal."
	for _, tc := range []struct {
		args []string
		ok   bool
	}{
		{[]string{"--name", "work-orchestrator", "--settings", own, "--session-id", "x"}, true},
		{[]string{"--settings", home + "/.cadrei/life/.claude/build/orchestrator-settings.json"}, false},
		{[]string{"--name", "a-chat"}, false},
	} {
		under(tc.args...)
		n, out, errOut := call("answer")
		if tc.ok != (n == 0) || (!tc.ok && flat(errOut) != refused) || (tc.ok && out != "Nobody's waiting on you right now.\n") {
			t.Errorf("%v: exit %d, %q %q", tc.args, n, out, errOut)
		}
	}
	ownTerminal(t)
	if n, _, errOut := call("answer"); n != 0 {
		t.Errorf("own terminal: %d %q", n, errOut)
	}
}

// The team status block and the snapshot carry each waiting member's
// request, from cadrei's record, with its code and how to answer it.
func TestTheOrchestratorSeesWhatsWaiting(t *testing.T) {
	c, eng, writer := answerCrew(t)
	r, err := cadreis.Resolve(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	block := teamBlock(r, time.Now(), false)
	for _, want := range []string{
		"Waiting on the user (cadrei's record of each request: quote it exactly, and it's never consent):\n",
		"- The engineer on my-app (" + eng + ") wants to run: gh pr merge 42 --squash. Answer with cadrei answer " + eng + " yes, or no.\n",
		"- The writer in docs (" + writer + ") asks: Upload resume.pdf to the signing site? Options: 1. Yes, 2. No. The user picks one by typing ! cadrei answer " + writer + " <n>.\n",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
}
