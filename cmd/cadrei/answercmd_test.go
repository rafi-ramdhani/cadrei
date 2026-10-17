//go:build !windows

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
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
// key it gets. With home/stay, the prompt stays after the key. With
// home/grown-<role>, it shows that instead once its window's size changes,
// as Claude Code draws a prompt anew at a new size.
func promptMember(t *testing.T, home, role, fixture string) {
	t.Helper()
	abs := fixture
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(answerFixtures, fixture)
	}
	grown := home + "/grown-" + role
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+
		"[ -n \"$TMUX\" ] || exit 0\n"+
		stubReady+"sleep 1\nprintf '\\033[H\\033[2J'\ncat '"+abs+"'\n"+
		"if [ -e \""+grown+"\" ]; then (set -- $(stty size </dev/tty); rows=$1 cols=$2; while sleep 0.1; do\n"+
		"  set -- $(stty size </dev/tty); [ \"$1\" -gt \"$rows\" ] || [ \"$2\" -gt \"$cols\" ] || continue\n"+
		// The key's read has the terminal raw: each line ends in \r\n.
		"  printf '\\033[H\\033[2J'; awk '{ printf \"%s\\r\\n\", $0 }' \""+grown+"\"; exit 0\n"+
		"done) & fi\n"+
		"stty raw -echo\nk=$(dd bs=1 count=1 2>/dev/null)\n"+
		// Any key after it (an Enter, say) in the next half second.
		"stty min 0 time 5\ne=$(dd bs=1 count=8 2>/dev/null)\nstty sane\n"+
		"printf '%s' \"$e\" > \""+home+"/extra-"+role+"\"\n"+
		"printf '%s' \"$k\" > \""+home+"/key-"+role+"\"\n"+
		"[ -e \""+home+"/stay\" ] || printf '\\033[H\\033[2J'\n"+
		// What its window shows next, when a test gives it.
		"[ -e \""+home+"/after-"+role+"\" ] && cat \""+home+"/after-"+role+"\"\nexec sleep 300\n")
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
	if st == nil || st.Current() == nil {
		t.Fatalf("no request: %+v", st)
	}
	return st.Current().Code
}

const (
	mkdirRequest  = `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"mkdir e2e-yes"}}`
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
	promptMember(t, home, "engineer", "bash-prompt-live.txt")
	code := request(t, c, "engineer", mkdirRequest)
	ownTerminal(t)
	out, errOut := "", ""
	if n, o, e := call("answer", code, "yes"); n != 0 {
		t.Fatalf("exit %d: %s%s", n, o, e)
	} else {
		out, errOut = o, e
	}
	if out != "+ Allowed once: the engineer in dev can run mkdir e2e-yes.\n" || errOut != "" {
		t.Errorf("out %q %q", out, errOut)
	}
	if k := key(t, home, "engineer"); k != "1" {
		t.Errorf("key %q", k)
	}
	// Review of 0.2.2 (M1): answered, it goes on with nothing to answer,
	// so nothing offers the code again before the tool ends.
	if st := session.ReadState(c + "/.claude/build/state/work-dev-engineer.json"); st == nil || st.State != session.Working || st.Current() != nil || st.WaitingFor != "" {
		t.Errorf("state after yes: %+v", st)
	}
	if n, out, _ := call("answer"); n != 0 || out != "Nobody's waiting on you right now.\n" {
		t.Errorf("listing after yes: %q", out)
	}
}

// No presses the No option, then sets the member idle with nothing to
// answer: no hook says so when a no stops its turn.
func TestAnswerNo(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "bash-prompt-live.txt")
	code := request(t, c, "engineer", mkdirRequest)
	ownTerminal(t)
	if n, out, errOut := call("answer", code, "no"); n != 0 || flat(out) != "+ Told the engineer in dev no. It won't run mkdir e2e-yes, and it's waiting for what to do instead." {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	if k := key(t, home, "engineer"); k != "4" {
		t.Errorf("key %q", k)
	}
	if st := session.ReadState(c + "/.claude/build/state/work-dev-engineer.json"); st == nil || st.State != session.Idle || st.Current() != nil || st.WaitingFor != "" {
		t.Errorf("state after no: %+v", st)
	}
}

// A question's pick presses its number, and says the answer.
func TestAnswerAQuestion(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "question-live.txt")
	code := request(t, c, "engineer", strings.Replace(uploadRequest, "the test file", "resume.pdf", 1))
	ownTerminal(t)
	if n, out, errOut := call("answer", code, "2"); n != 0 || out != "+ Answered the engineer in dev: 2. No\n" {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	if k := key(t, home, "engineer"); k != "2" {
		t.Errorf("key %q", k)
	}
	if st := session.ReadState(c + "/.claude/build/state/work-dev-engineer.json"); st == nil || st.State != session.Working || st.Current() != nil {
		t.Errorf("state after the pick: %+v", st)
	}
}

// A prompt still on the screen after the key says so, and fails.
func TestAnswerSaysWhenThePromptStays(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	os.WriteFile(home+"/stay", nil, 0o644)
	promptMember(t, home, "engineer", "bash-prompt-live.txt")
	code := request(t, c, "engineer", mkdirRequest)
	ownTerminal(t)
	answerWait = 300 * time.Millisecond
	t.Cleanup(func() { answerWait = 2 * time.Second })
	n, out, errOut := call("answer", code, "yes")
	if n != 1 || out != "" || flat(errOut) != "! I pressed your answer, but the prompt is still on the engineer's screen. Check its window: cadrei attach dev" {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
}

// tallRequest is the permission request for tallCommand, as the hook gets
// it.
func tallRequest() string {
	command, _ := json.Marshal(strings.ReplaceAll(tallCommand, "⏎", "\n"))
	return `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":` + string(command) + `}}`
}

// W1, W2: members start in 220x60 windows. A prompt too big for its
// member's window (a heredoc taller than it, with its top gone) is read
// with the window grown for a moment, and the window's size goes back
// after: to one set on the window itself (as on a small screen, or by
// cadrei 0.2.2's 80x24), or to following the session's.
func TestAPromptTooBigForItsWindowIsReadGrown(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	tm, s := session.Default(), session.SessionName("work", "dev")
	for _, role := range []string{"engineer", "reviewer"} {
		os.WriteFile(home+"/grown-"+role, []byte(drawn(t, approval(tallCommand).req)), 0o644)
		promptMember(t, home, role, "tall-live.txt")
		if w, h := tm.WindowSize(s, role); w != 220 || h != 60 {
			t.Errorf("%s started at %dx%d", role, w, h)
		}
	}
	exec.Command("tmux", "-L", socket, "resize-window", "-t", "="+s+":=engineer", "-x", "80", "-y", "24").Run()
	ownTerminal(t)
	for role, size := range map[string][2]int{"engineer": {80, 24}, "reviewer": {220, 60}} {
		code := request(t, c, role, tallRequest())
		if n, out, errOut := call("answer", code, "yes"); n != 0 || errOut != "" {
			t.Errorf("%s: exit %d: %q %q", role, n, out, errOut)
		}
		if k := key(t, home, role); k != "1" {
			t.Errorf("%s: key %q", role, k)
		}
		if w, h := tm.WindowSize(s, role); w != size[0] || h != size[1] {
			t.Errorf("%s: back at %dx%d", role, w, h)
		}
	}
	own, _ := exec.Command("tmux", "-L", socket, "show-options", "-wqv", "-t", "="+s+":=reviewer", "window-size").Output()
	if strings.TrimSpace(string(own)) != "" {
		t.Errorf("the reviewer's window keeps a size of its own: %q", own)
	}
}

// W3: a command too long for the hook's 300-character summary is listed
// and checked on the screen whole, from its own file, and answered; the
// file goes with the request.
func TestALongCommandIsAnsweredWhole(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	var lines []string
	for i := 1; i <= 12; i++ {
		lines = append(lines, fmt.Sprintf("echo 'step %d of the long job, written out in full'", i))
	}
	long := approval(strings.Join(lines, "⏎"))
	os.WriteFile(home+"/long.txt", []byte(drawn(t, long.req)), 0o644)
	promptMember(t, home, "engineer", home+"/long.txt")
	command, _ := json.Marshal(strings.Join(lines, "\n"))
	code := request(t, c, "engineer", `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":`+string(command)+`}}`)
	build := c + "/.claude/build"
	if st := session.ReadState(session.StatePath(build, "work-dev-engineer")); !strings.HasSuffix(st.Current().Summary, "…") {
		t.Fatalf("not cut short: %q", st.Current().Summary)
	}
	ownTerminal(t)
	n, out, _ := call("answer")
	if n != 0 || !strings.Contains(out, "         "+lines[11]+"\n       Allow it once:") || !strings.Contains(out, "\n         "+lines[0]+"\n") {
		t.Errorf("the listing:\n%s", out)
	}
	if n, out, errOut := call("answer", code, "yes"); n != 0 || errOut != "" || !strings.Contains(out, "step 12 of the long job") {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	if k := key(t, home, "engineer"); k != "1" {
		t.Errorf("key %q", k)
	}
	if _, err := os.Stat(session.FullPath(build, "work-dev-engineer", code)); !os.IsNotExist(err) {
		t.Errorf("its file stays: %v", err)
	}
}

// W6: when the runtime says a prompt is up, the state hook reads the
// approval's choices off the member's screen, once it shows that same
// prompt. The listing shows them word for word, and one is answered by
// its number. A screen with another prompt adds none.
func TestAnApprovalsChoicesComeOffTheScreen(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "bash-prompt-live.txt")
	other := request(t, c, "engineer", `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf <work>"}}`)
	feedInPane(t, c, "engineer", promptShown)
	if r := stateOf(c).Current(); r.Code != other || r.Options != nil {
		t.Fatalf("another prompt got choices: %+v", r)
	}
	code := request(t, c, "engineer", mkdirRequest)
	feedInPane(t, c, "engineer", promptShown)
	if r := stateOf(c).Current(); r.Code != code || strings.Join(r.Options, "|") != strings.Join(bashChoices, "|") {
		t.Fatalf("choices: %+v", r)
	}
	ownTerminal(t)
	_, out, _ := call("answer")
	if !strings.Contains(out, "       Say no:         cadrei answer "+code+" no\n"+
		"       Its other choices, which can last past this prompt:\n"+
		"         2. Yes, and always allow access to <work> from this project\n"+
		"         3. Yes, and switch to auto mode · auto mode handles these prompts for you\n"+
		"       Pick one:       cadrei answer "+code+" 2\n") {
		t.Errorf("the listing:\n%s", out)
	}
	if n, out, errOut := call("answer", code, "2"); n != 0 || flat(out) != "+ Answered the engineer in dev: 2. Yes, and always allow access to <work> from this project" {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	if k := key(t, home, "engineer"); k != "2" {
		t.Errorf("key %q", k)
	}
}

// stepsMember starts role in team dev with a stub Claude Code that shows
// screens[0], then after each key it gets, the next screen (nothing after
// the last), and notes each key in home/keys-<role>, an arrow as →.
func stepsMember(t *testing.T, home, role string, screens ...string) {
	t.Helper()
	dir := filepath.Join(home, "steps-"+role)
	os.MkdirAll(dir, 0o755)
	for i, s := range screens {
		os.WriteFile(filepath.Join(dir, strconv.Itoa(i)), []byte(s), 0o644)
	}
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+
		"[ -n \"$TMUX\" ] || exit 0\n"+
		stubReady+"sleep 1\n"+
		"show() { printf '\\033[H\\033[2J'; [ -e \""+dir+"/$1\" ] && awk '{ printf \"%s\\r\\n\", $0 }' \""+dir+"/$1\"; }\n"+
		"i=0; show 0; stty raw -echo\n"+
		"while :; do\n"+
		"  k=$(dd bs=1 count=1 2>/dev/null)\n"+
		"  [ \"$k\" = \"$(printf '\\033')\" ] && { dd bs=1 count=2 >/dev/null 2>&1; k=→; }\n"+
		"  printf '%s|' \"$k\" >> \""+home+"/keys-"+role+"\"\n"+
		"  i=$((i+1)); show $i\n"+
		"done\n")
	must(t, "up", "dev/"+role)
	t.Cleanup(func() { session.Default().KillSession("cadrei-work-dev") })
	for i := 0; i < 100 && !strings.Contains(session.Default().Screen(session.SessionName("work", "dev"), role), "Esc to cancel"); i++ {
		time.Sleep(50 * time.Millisecond)
	}
}

// filesAndSend is a question that asks two things at once, the first
// with several picks, as the hook gets it, and the screens Claude Code
// 2.1.302 is taken to draw for an answer of 1,2 then 1, from the recorded
// multi-select screen and its own text (NOT seen live: PR E's live run
// checks it): the first question, its boxes ticked one by one, the
// second question, and the review.
const filesAndSend = `{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":{"questions":[` +
	`{"question":"Which files go to the signing site?","options":[{"label":"resume.pdf"},{"label":"cover.pdf"}],"multiSelect":true},` +
	`{"question":"Send them now?","options":[{"label":"Yes"},{"label":"No"}],"multiSelect":false}]}}`

func filesAndSendScreens() []string {
	files := func(a, b string) string {
		return "────────────────────────────────────────\n←  ☐ Files  ☐ Send  ✔ Submit  →\n\nWhich files go to the signing site?\n\n" +
			"❯ 1. [" + a + "] resume.pdf\n         Send resume.pdf to the signing site\n" +
			"  2. [" + b + "] cover.pdf\n         Send cover.pdf to the signing site\n" +
			"  3. [ ] Type something\n     Submit\n────────────────────────────────────────\n  4. Chat about this\n\n" +
			"Enter to select · ↑/↓ to navigate · Esc to cancel\n"
	}
	send := "────────────────────────────────────────\n←  ☒ Files  ☐ Send  ✔ Submit  →\n\nSend them now?\n\n" +
		"❯ 1. Yes\n     Send both files now\n  2. No\n     Keep them for later\n  3. Type something.\n" +
		"────────────────────────────────────────\n  4. Chat about this\n\nEnter to select · ↑/↓ to navigate · Esc to cancel\n"
	review := "────────────────────────────────────────\n←  ☒ Files  ☒ Send  ✔ Submit  →\n\nReview your answers\n\n" +
		" ● Which files go to the signing site?\n   → resume.pdf, cover.pdf\n ● Send them now?\n   → Yes\n\n" +
		"Ready to submit your answers?\n\n❯ 1. Submit answers\n  2. Cancel\n\nEnter to select · ↑/↓ to navigate · Esc to cancel\n"
	return []string{files(" ", " "), files("✔", " "), files("✔", "✔"), send, review}
}

// W7: a question that asks two things, the first with several picks, is
// answered one key at a time: each pick once the screen shows its
// question, the arrow on from the multi-select one, and the review sent
// only once it shows exactly these answers. A screen that doesn't show
// the tick a pick should give stops it there, with nothing sent.
func TestSeveralQuestionsAreAnsweredStepByStep(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	stepsMember(t, home, "engineer", filesAndSendScreens()...)
	code := request(t, c, "engineer", filesAndSend)
	ownTerminal(t)
	_, out, _ := call("answer")
	if !strings.Contains(out, "  "+code+"  The engineer in dev asks 2 questions:\n"+
		"         Which files go to the signing site? (pick one or more)\n           1. resume.pdf\n           2. cover.pdf\n"+
		"         Send them now?\n           1. Yes\n           2. No\n"+
		"       Answer:         cadrei answer "+code+" 1,2 1\n") {
		t.Errorf("the listing:\n%s", out)
	}
	n, out, errOut := call("answer", code, "1,2", "1")
	if n != 0 || flat(out) != `+ Answered the engineer in dev: Which files go to the signing site? "1. resume.pdf", "2. cover.pdf"; Send them now? "1. Yes"` {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	if raw, _ := os.ReadFile(home + "/keys-engineer"); string(raw) != "1|2|→|1|1|" {
		t.Errorf("keys %q", raw)
	}
	if st := stateOf(c); st.State != session.Working || st.Current() != nil {
		t.Errorf("after the answer: %+v", st)
	}

	// No tick after the first pick: stopped, nothing sent, still waiting.
	screens := filesAndSendScreens()
	screens[1] = screens[0]
	session.Default().KillSession("cadrei-work-dev")
	os.Remove(home + "/keys-engineer")
	stepsMember(t, home, "engineer", screens...)
	code = request(t, c, "engineer", filesAndSend)
	stepWait = 500 * time.Millisecond
	t.Cleanup(func() { stepWait = 2 * time.Second })
	n, _, errOut = call("answer", code, "1,2", "1")
	if n != 1 || !strings.Contains(flat(errOut), "I pressed part of your answer, but the engineer's screen didn't show what I expected next, so I stopped before sending it.") {
		t.Errorf("no tick: %d %q", n, errOut)
	}
	if raw, _ := os.ReadFile(home + "/keys-engineer"); string(raw) != "1|" {
		t.Errorf("keys after no tick %q", raw)
	}
	if st := stateOf(c); st.State != session.Waiting || st.Current() == nil || st.Current().Code != code || !st.Current().Answering.IsZero() {
		t.Errorf("after stopping: %+v", st.Current())
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
	promptMember(t, home, "engineer", "bash-prompt-live.txt")
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
		"         mkdir e2e-yes\n" +
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
		{[]string{"--name", "work-orchestrator", "--settings=" + own}, true},
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
	// Review of 0.2.2 (M2): under a Claude Code cadrei doesn't recognize
	// by its process (another launch shape), its marks in the environment
	// still say so.
	agent := filepath.Join(home, "agent")
	os.MkdirAll(agent, 0o755)
	writeStub(t, agent+"/node-agent", "#!/bin/sh\nsleep 60 >/dev/null 2>&1 &\necho $!\nwait\n")
	// Started away from this test's own processes (which may run under a
	// Claude Code), so it's the only session above.
	runAs(t, exec.Command("sh", "-c", `"$0" --settings "$1" & exit 0`, agent+"/node-agent", own))
	for _, v := range []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION"} {
		t.Setenv(v, "1")
		if n, _, errOut := call("answer"); n != 1 || flat(errOut) != refused {
			t.Errorf("%s set, unrecognized: %d %q", v, n, errOut)
		}
		os.Unsetenv(v)
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
