//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// startFixtures holds the start-up screens, found before any test moves
// to its own folder.
var startFixtures, _ = filepath.Abs("testdata/start")

// startScreen is a start-up screen from testdata/start.
func startScreen(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(startFixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// startOf is the record cadrei makes of a start-up screen from testdata,
// for the engineer in dev, started in dir.
func startOf(t *testing.T, name, dir string) *waitingOn {
	t.Helper()
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		t.Fatal(err)
	}
	s, ok := rt.Sessions().StartScreen(startScreen(t, name))
	if !ok {
		t.Fatalf("%s isn't read as a start-up screen", name)
	}
	return &waitingOn{team: "dev", role: "engineer", dir: dir, req: &session.Request{Code: "s7k", Kind: session.ForStart, Tool: s.Kind,
		Question: s.Title, Options: s.Options, Multi: s.Multi, Source: session.FromScreen}}
}

// gitRepo is a new git repository's top folder, in the sandbox home.
func gitRepo(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, "app")
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	return dir
}

// N5: what an answer may pick on each start-up screen. Any option of one
// cadrei knows, but bypass mode's yes (a mode switch, user decision 6);
// only Yes and No on one it doesn't; none on one it can't read whole.
func TestTheChoicesOnAStartUpScreen(t *testing.T) {
	sandbox(t)
	for name, want := range map[string][]int{
		"trust.txt": {1, 2}, "login.txt": {1, 2, 3}, "server.txt": {1, 2, 3}, "apikey.txt": {1, 2},
		"bypass.txt": {1}, "imports.txt": {1, 2}, "other.txt": {1, 2}, "style.txt": {1, 2, 3, 4, 5, 6, 7},
	} {
		w := startOf(t, name, "")
		if got := startChoices(w.req); !slices.Equal(got, want) || !answerable(w.req) {
			t.Errorf("%s: %v %v", name, got, answerable(w.req))
		}
	}
	if w := startOf(t, "servers.txt", ""); answerable(w.req) {
		t.Errorf("several servers at once: %+v", w.req)
	}
}

// N5: a trust screen's yes is refused where cadrei trust refuses the
// folder (here, the home folder), and kept off the choices; the top of a
// git repository may be trusted. Bypass mode's yes is never picked.
func TestATrustYesOnlyWhereCadreiWouldTrust(t *testing.T) {
	home := sandbox(t)
	refused := startOf(t, "trust.txt", home)
	if why := checkChoice(refused, "s7k", "2", plain); why != "Cadrei won't trust ~ for you: it's your home folder. If you're sure, answer it in its window: cadrei attach dev" {
		t.Errorf("home: %q", why)
	}
	if why := checkChoice(refused, "s7k", "1", plain); why != "" {
		t.Errorf("no, exit: %q", why)
	}
	if n := refused.note(); n != "The engineer in dev (s7k) waits on a start-up screen: Is this a project you created or one you trust? Options: 1. No, exit."+
		" Cadrei won't trust its folder from here (it's your home folder): only its window can. The user picks one by typing ! cadrei answer s7k <n>." {
		t.Errorf("note: %q", n)
	}
	repo := startOf(t, "trust.txt", gitRepo(t, home))
	if why := checkChoice(repo, "s7k", "2", plain); why != "" {
		t.Errorf("a repository: %q", why)
	}
	if got := askReason(repo, "2"); got != `Cadrei: answer the start-up screen of the engineer in dev with "2. Yes, I trust this folder"?`+
		" That's saved: ~/app is trusted from now on, with its own settings and hooks. It asks: Is this a project you created or one you trust?" {
		t.Errorf("ask: %q", got)
	}
	bypass := startOf(t, "bypass.txt", "")
	if why := checkChoice(bypass, "s7k", "2", plain); why != "Cadrei never turns on bypass mode for you: that choice stays in the engineer's window: cadrei attach dev" {
		t.Errorf("bypass: %q", why)
	}
	for choice, want := range map[string]string{
		"3": "s7k has options 1 to 2. Use cadrei answer s7k 1, for example.",
		"x": "s7k has options 1 to 2. Use cadrei answer s7k 1, for example.",
	} {
		if why := checkChoice(bypass, "s7k", choice, plain); why != want {
			t.Errorf("%s: %q", choice, why)
		}
	}
	other := startOf(t, "other.txt", "")
	if why := checkChoice(other, "s7k", "3", plain); why != "Cadrei only picks 1 or 2 on that screen. Use cadrei answer s7k 1, or answer it in its window: cadrei attach dev" {
		t.Errorf("other: %q", why)
	}
	if why := checkChoice(startOf(t, "servers.txt", ""), "s7k", "1", plain); why != "Cadrei can't answer that start-up screen from here, so answer it in its window: cadrei attach dev" {
		t.Errorf("servers: %q", why)
	}
}

// The ask says what a pick does past the screen: saved for the project,
// billed to an API key, or a login that goes on in the window.
func TestTheAskForAStartUpScreen(t *testing.T) {
	sandbox(t)
	for name, want := range map[string]string{
		"server.txt 2": `Cadrei: answer the start-up screen of the engineer in dev with "2. Use this and all future MCP servers in this project"?` +
			" That's saved: it won't ask about this project's MCP servers again. It asks: New MCP server found in this project: docs-search",
		"apikey.txt 2": `Cadrei: answer the start-up screen of the engineer in dev with "2. Yes"? It then runs on that API key, and its use is billed to the key.` +
			" It asks: Detected a custom API key in your environment. Do you want to use this API key?",
		"login.txt 1": `Cadrei: answer the start-up screen of the engineer in dev with "1. Claude account with subscription · Pro, Max, Team, or Enterprise"?` +
			" The login goes on in its window, and may open a browser on its machine. It asks: Select login method",
		"imports.txt 1": `Cadrei: answer the start-up screen of the engineer in dev with "1. No, disable external imports"? It asks: Allow external CLAUDE.md file imports?`,
	} {
		file, choice, _ := strings.Cut(name, " ")
		if got := askReason(startOf(t, file, ""), choice); got != want {
			t.Errorf("%s: %q", name, got)
		}
	}
}

// The screen check: the same screen, wherever its pointer is, but never
// another one, one with other options, or one that's gone.
func TestAStartUpScreenMustStillShow(t *testing.T) {
	sandbox(t)
	w := startOf(t, "server.txt", "")
	screen := startScreen(t, "server.txt")
	moved := withPointer(screen, w.req.Options, 0)
	for name, s := range map[string]string{"as recorded": screen, "pointer moved": moved} {
		if key, why := keyOn(s, w, "1", plain); key != "1" || why != "" {
			t.Errorf("%s: %q %q", name, key, why)
		}
	}
	for name, s := range map[string]string{
		"other screen":  startScreen(t, "trust.txt"),
		"other options": strings.Replace(screen, "Use this MCP server ", "Use that MCP server ", 1),
		"other server":  strings.Replace(screen, "docs-search", "docs-searcher", 1),
		"gone":          "? for shortcuts\n",
	} {
		if key, why := keyOn(s, w, "1", plain); key != "" || why != "I couldn't find that prompt on the engineer's screen, so I didn't press anything. Answer it in its window: cadrei attach dev" {
			t.Errorf("%s: %q %q", name, key, why)
		}
	}
}

// withPointer is a start-up screen with its menu's pointer at option at
// of labels, as the runtime draws it after an arrow key.
func withPointer(screen string, labels []string, at int) string {
	lines := strings.Split(screen, "\n")
	for i, l := range lines {
		for n, label := range labels {
			idx := strings.Index(l, label)
			if idx < 0 || strings.TrimRight(l[idx:], " │") != label {
				continue
			}
			prefix, ok := strings.CutSuffix(l[:idx], "❯ ")
			if !ok {
				if prefix, ok = strings.CutSuffix(l[:idx], "  "); !ok {
					continue
				}
			}
			mark := "  "
			if n == at {
				mark = "❯ "
			}
			lines[i] = prefix + mark + l[idx:]
		}
	}
	return strings.Join(lines, "\n")
}

// startMember starts the engineer in dev, in dir when it's set, with a
// stub Claude Code that shows start-up screen fixture, its pointer at
// option at, and moves it on arrow keys as the runtime does (but not
// while home/stuck exists). On Enter it notes in home/picked the option
// the pointer was at, and shows its ready footer. It returns what up
// said.
func startMember(t *testing.T, home, fixture string, at int, dir string) string {
	t.Helper()
	rt, _ := runtime.Get(runtimeName())
	screen := startScreen(t, fixture)
	s, _ := rt.Sessions().StartScreen(screen)
	for n := range s.Options {
		os.WriteFile(filepath.Join(home, "start-"+strconv.Itoa(n)+".txt"), []byte(withPointer(screen, s.Options, n)), 0o644)
	}
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+
		"[ -n \"$TMUX\" ] || exit 0\n"+
		"at="+strconv.Itoa(at)+" last="+strconv.Itoa(len(s.Options)-1)+"\n"+
		"draw() { printf '\\033[H\\033[2J'; cat \""+home+"/start-$at.txt\"; }\n"+
		"draw\nstty -icanon -echo min 1\n"+
		"while :; do\n"+
		"  k=$(dd bs=1 count=1 2>/dev/null)\n"+
		"  case \"$k\" in\n"+
		"  \"$(printf '\\033')\") r=$(dd bs=1 count=2 2>/dev/null)\n"+
		"    [ -e \""+home+"/stuck\" ] && continue\n"+
		"    case \"$r\" in '[A') [ $at -gt 0 ] && at=$((at-1)) ;; '[B') [ $at -lt $last ] && at=$((at+1)) ;; esac\n"+
		"    draw ;;\n"+
		"  '') echo $at > \""+home+"/picked\"; printf '\\033[H\\033[2J'; echo '? for shortcuts'; exec sleep 300 ;;\n"+
		"  esac\n"+
		"done\n")
	args := []string{"up", "dev/engineer"}
	if dir != "" {
		args = append(args, dir)
	}
	out := must(t, args...)
	t.Cleanup(func() { session.Default().KillSession(session.SessionName("work", "dev")) })
	if dir != "" {
		t.Cleanup(func() { session.Default().KillSession(session.SessionName("work", session.Key("dev", "app"))) })
	}
	return out
}

// pointerPick is the option the stub member's pointer was at on Enter, or
// "" when it got no Enter in two seconds.
func pointerPick(t *testing.T, home string) string {
	t.Helper()
	for i := 0; i < 40; i++ {
		if raw, err := os.ReadFile(home + "/picked"); err == nil {
			return strings.TrimSpace(string(raw))
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ""
}

// startState is the engineer in dev's state, as cadrei ls reads it.
func startState(t *testing.T, c, member string) *session.State {
	t.Helper()
	return session.ReadState(c + "/.claude/build/state/" + member + ".json")
}

// N5, e2e case 10: a start-up screen gets a code at up, the listing shows
// its choices (a trust yes for a team folder only in its window), the
// refused yes presses nothing, and an answer by code picks its option
// with Enter. The member is starting again after, and the prompt log
// notes it was answered by code.
func TestAStartUpScreenGetsACode(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	out := flat(startMember(t, home, "trust.txt", 0, ""))
	if !strings.Contains(out, "It's waiting on a start-up screen and won't get going until you answer it: Is this a project you created or one you trust?"+
		" See its choices with cadrei answer, or answer it in its window: cadrei attach dev") {
		t.Errorf("up: %q", out)
	}
	st := startState(t, c, "work-dev-engineer")
	if st == nil || st.State != session.Waiting || st.WaitingFor != session.ForStart || st.Current() == nil || st.Current().Tool != runtime.StartTrust {
		t.Fatalf("state: %+v", st)
	}
	code := st.Current().Code
	ownTerminal(t)
	_, list, _ := call("answer")
	for _, want := range []string{code + "  The engineer in dev waits on a start-up screen:", "1. No, exit",
		"2. Yes, I trust this folder (only in its window: it's inside ~/.cadrei, where cadrei keeps its own files)", "Answer:         cadrei answer " + code + " 1"} {
		if !strings.Contains(list, want) {
			t.Errorf("listing has no %q:\n%s", want, list)
		}
	}
	if n, _, errOut := call("answer", code, "2"); n != 1 || flat(errOut) != "cadrei: cadrei won't trust ~/.cadrei/work/teams/dev for you: it's inside ~/.cadrei,"+
		" where cadrei keeps its own files. If you're sure, answer it in its window: cadrei attach dev" {
		t.Errorf("trust a team folder: %d %q", n, errOut)
	}
	if n, out, errOut := call("answer", code, "1"); n != 0 || out != "+ Answered the start-up screen of the engineer in dev: 1. No, exit\n" {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	if p := pointerPick(t, home); p != "0" {
		t.Errorf("picked %q", p)
	}
	if st := startState(t, c, "work-dev-engineer"); st == nil || st.State != session.Starting || len(st.Open) != 0 {
		t.Errorf("state after: %+v", st)
	}
	log := readFile(t, c+"/.claude/build/state/prompts.log")
	if !strings.Contains(log, `"code":"`+code+`","kind":"start","tool":"trust","source":"screen","event":"ended","ended":"by_code"`) {
		t.Errorf("log:\n%s", log)
	}
}

// The pointer is moved a key at a time to the option picked, and Enter
// pressed there: from the last option up to the first.
func TestAStartUpAnswerMovesThePointer(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	startMember(t, home, "server.txt", 2, "")
	code := startState(t, c, "work-dev-engineer").Current().Code
	ownTerminal(t)
	if n, out, errOut := call("answer", code, "1"); n != 0 || out != "+ Answered the start-up screen of the engineer in dev: 1. Use this MCP server\n" {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	if p := pointerPick(t, home); p != "0" {
		t.Errorf("picked %q", p)
	}
}

// A trust yes for a project's folder, the top of its git repository, is
// picked: the pointer goes down to it.
func TestATrustYesForAProject(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	startMember(t, home, "trust.txt", 0, gitRepo(t, home))
	code := startState(t, c, "work-dev-app-engineer").Current().Code
	ownTerminal(t)
	if n, out, errOut := call("answer", code, "2"); n != 0 || flat(out) != "+ Answered the start-up screen of the engineer on app: 2. Yes, I trust this folder" {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	if p := pointerPick(t, home); p != "1" {
		t.Errorf("picked %q", p)
	}
}

// A screen whose pointer doesn't move as cadrei expects gets no Enter.
func TestAStartUpScreenThatDoesntMoveGetsNoEnter(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	startMember(t, home, "server.txt", 2, "")
	os.WriteFile(home+"/stuck", nil, 0o644)
	code := startState(t, c, "work-dev-engineer").Current().Code
	ownTerminal(t)
	stepWait = 300 * time.Millisecond
	t.Cleanup(func() { stepWait = 2 * time.Second })
	n, out, errOut := call("answer", code, "1")
	if n != 1 || out != "" || flat(errOut) != "! The engineer's screen didn't move as I expected, so I stopped before picking anything. Answer it in its window: cadrei attach dev" {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	if p := pointerPick(t, home); p != "" {
		t.Errorf("picked %q", p)
	}
	if st := startState(t, c, "work-dev-engineer"); st == nil || st.Current() == nil || st.Current().Code != code || st.Current().ByCode(time.Now()) {
		t.Errorf("state after: %+v", st)
	}
}

// Answered in its window, a start-up screen's request ends at the next
// look (cadrei ls), noted in the prompt log as answered in its window;
// a screen that shows up after the first look gets a code then.
func TestAStartUpScreenIsLookedAtAgain(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	// The screen comes a moment after up's own look, which ends at the
	// ready footer.
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+"[ -n \"$TMUX\" ] || exit 0\n"+stubReady+"sleep 1\nprintf '\\033[H\\033[2J'\ncat '"+
		filepath.Join(startFixtures, "login.txt")+"'\nstty -icanon -echo min 1\ndd bs=1 count=1 >/dev/null 2>&1\nprintf '\\033[H\\033[2J'\n"+stubReady+"exec sleep 300\n")
	if out := flat(must(t, "up", "dev/engineer")); strings.Contains(out, "start-up screen") {
		t.Errorf("up: %q", out)
	}
	t.Cleanup(func() { session.Default().KillSession(session.SessionName("work", "dev")) })
	var st *session.State
	for i := 0; i < 80; i++ {
		call("ls")
		if st = startState(t, c, "work-dev-engineer"); st != nil && st.State == session.Waiting {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st == nil || st.Current() == nil || st.Current().Tool != runtime.StartLogin {
		t.Fatalf("no code after up: %+v", st)
	}
	code := st.Current().Code
	session.Default().SendKeys(session.SessionName("work", "dev"), "engineer", false, "Enter")
	for i := 0; i < 80; i++ {
		call("ls")
		if st = startState(t, c, "work-dev-engineer"); st != nil && st.State == session.Starting {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st == nil || st.State != session.Starting || len(st.Open) != 0 {
		t.Errorf("after the window's answer: %+v", st)
	}
	if log := readFile(t, c+"/.claude/build/state/prompts.log"); !strings.Contains(log, `"code":"`+code+`","kind":"start","tool":"login","source":"screen","event":"ended","ended":"in_window"`) {
		t.Errorf("log:\n%s", log)
	}
}
