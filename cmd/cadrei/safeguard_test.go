//go:build !windows

package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// stopped is a safeguard stop's record, as cadrei makes it, for the
// engineer in dev.
func stopped() *waitingOn {
	return &waitingOn{team: "dev", role: "engineer", name: "work-dev-engineer", req: &session.Request{Code: "g4t", Kind: session.ForSafeguard,
		At: time.Date(2026, 1, 1, 10, 0, 5, 0, time.UTC), Options: session.SafeguardOptions, Source: session.FromTranscript}}
}

// N7: a safeguard stop's code takes 1 and a model (it starts again on its
// chat with that model, for this start only) or 2 (it starts fresh).
// Picking the model in its window stays off the code: Claude Code 2.1.304
// saves a model picked there as the default for new sessions unless s is
// pressed, which would change the user's own settings. The guard's ask
// and the orchestrator's note say what each does.
func TestASafeguardStopsChoices(t *testing.T) {
	sandbox(t)
	w := stopped()
	if !answerable(w.req) {
		t.Fatal("not answerable")
	}
	takes := "g4t takes 1 and a model (start it again on another model) or 2 (start it fresh). Use cadrei answer g4t 1 <model> or cadrei answer g4t 2."
	for choice, want := range map[string]string{
		"2":                     "",
		"1 sonnet":              "",
		"1 claude-sonnet-5-5":   "",
		"1":                     "Say which model it starts again on, like cadrei answer g4t 1 sonnet. Use an alias such as opus, sonnet or fable, or a full name such as claude-sonnet-5-5.",
		"1 -sonnet":             "\"-sonnet\" isn't a model name. Use an alias such as opus, sonnet or fable, or a full name such as claude-sonnet-5-5, like cadrei answer g4t 1 sonnet.",
		"1 son net":             "\"son net\" isn't a model name. Use an alias such as opus, sonnet or fable, or a full name such as claude-sonnet-5-5, like cadrei answer g4t 1 sonnet.",
		"1 2":                   takes,
		"3":                     takes,
		"yes":                   takes,
		"2 sonnet":              takes,
		"1,2":                   takes,
		"Pick its model again":  takes,
		"1 sonnet --fresh":      "\"sonnet --fresh\" isn't a model name. Use an alias such as opus, sonnet or fable, or a full name such as claude-sonnet-5-5, like cadrei answer g4t 1 sonnet.",
		"Start it fresh please": takes,
	} {
		if why := checkChoice(w, "g4t", choice, plain); why != want {
			t.Errorf("%s: %q", choice, why)
		}
	}
	after := " after Claude's safeguards stopped its last reply."
	for choice, want := range map[string]string{
		"2":        "Cadrei: start the engineer in dev fresh? It stops, and starts again in a new chat that won't remember this one," + after,
		"1 sonnet": "Cadrei: start the engineer in dev again on sonnet? It stops, and starts again on its chat with sonnet for this start only (your default model stays as it is)," + after,
	} {
		if got := askReason(w, choice); got != want {
			t.Errorf("ask %s: %q", choice, got)
		}
		if key, why := keyOn("anything", w, choice, plain); key != choice || why != "" {
			t.Errorf("key %s: %q %q", choice, key, why)
		}
	}
	if n := w.note(); n != "The engineer in dev (g4t) was stopped by Claude's safeguards, so it won't answer on that model. To start it again on its chat with another model,"+
		" for this start only, the user types ! cadrei answer g4t 1 <model>. To start it fresh (a new chat that won't remember this one), they type ! cadrei answer g4t 2." {
		t.Errorf("note: %q", n)
	}
	// A request with the old options (or any others) is the window's.
	old := stopped()
	old.req.Options = []string{"Pick its model again", "Start it fresh"}
	if answerable(old.req) {
		t.Error("old options are answerable")
	}
}

// The guard takes 1 and a model on its own, with no character the shell
// would expand or run: a model with [ ] has to go through cadrei up
// --model by hand.
func TestTheGuardTakesAStopsModel(t *testing.T) {
	for command, want := range map[string]bool{
		"cadrei answer g4t 1 sonnet":                    true,
		"cadrei answer g4t 1 claude-sonnet-5-5":         true,
		"cadrei answer g4t 1 us.anthropic.claude:0@x/y": true,
		"cadrei answer g4t 1   opus  ":                  true,
		"cadrei answer g4t 1 2":                         true, // two picks, for a question
		"cadrei answer g4t 1 opus[1m]":                  false,
		"cadrei answer g4t 1 sonnet; ls":                false,
		"cadrei answer g4t 1 $(id)":                     false,
		"cadrei answer g4t 1 `id`":                      false,
		"cadrei answer g4t 1 ~/x":                       false,
		"cadrei answer g4t 1 'sonnet'":                  false,
		"cadrei answer g4t 1 -sonnet":                   false,
		"cadrei answer g4t 1 sonnet opus":               false,
		"cadrei answer g4t 2 sonnet":                    false,
		"cadrei answer g4t 1 sonnet > x":                false,
	} {
		if got := strictAnswer.MatchString(command); got != want {
			t.Errorf("%q: %v", command, got)
		}
	}
	if strictAnswer.MatchString("cadrei answer g4t 1 " + strings.Repeat("a", 201)) {
		t.Error("a model past 200 characters")
	}
}

// stopMember starts the engineer in dev, idle, with a safeguard stop in
// its transcript long enough ago for the runtime's own retry to be over,
// and returns the stop's code, from cadrei answer's listing. The stub
// notes each start's arguments in args.
func stopMember(t *testing.T, home, c string) string {
	t.Helper()
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+"[ -n \"$TMUX\" ] || exit 0\necho \"$*\" >> "+home+"/args\n"+stubReady+"exec sleep 300\n")
	must(t, "up", "dev/engineer")
	t.Cleanup(func() { session.Default().KillSession(session.SessionName("work", "dev")) })
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop"}`)
	placeTranscript(t, c, "work-dev-engineer", "safeguard-stuck.jsonl", nil)
	ownTerminal(t)
	_, list, _ := call("answer")
	for _, want := range []string{"Claude's safeguards stopped the last reply of the engineer in dev, so it won't answer on that model:",
		"1. Start it again on another model (on its chat, for this start only)", "2. Start it fresh (a new chat that won't remember this one)",
		"Another model:  cadrei answer ", " 1 <model>", "Start it fresh: cadrei answer "} {
		if !strings.Contains(list, want) {
			t.Fatalf("listing has no %q:\n%s", want, list)
		}
	}
	st := startState(t, c, "work-dev-engineer")
	if st == nil || st.Current() == nil || st.Current().Kind != session.ForSafeguard {
		t.Fatalf("state: %+v", st)
	}
	return st.Current().Code
}

// lastArgs is the stub's last start's arguments.
func lastArgs(t *testing.T, home string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(readFile(t, home+"/args")), "\n")
	return lines[len(lines)-1]
}

// N7, e2e case 11: 2 stops it and starts it again in a new chat; the
// prompt log notes the stop was answered by code. 1 alone presses nothing.
func TestASafeguardStopStartsItFresh(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	code := stopMember(t, home, c)
	if n, _, errOut := call("answer", code, "1"); n != 1 || !strings.Contains(flat(errOut), "which model it starts again on, like cadrei answer "+code+" 1 sonnet") {
		t.Errorf("1: %d %q", n, errOut)
	}
	before := session.ReadRecord(c + "/.claude/build/sessions/work-dev-engineer.json")
	n, out, errOut := call("answer", code, "2")
	if n != 0 || !strings.Contains(out, "+ Stopped the engineer\n") || !strings.Contains(out, "+ Started the engineer (fresh chat)\n") {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	if after := session.ReadRecord(c + "/.claude/build/sessions/work-dev-engineer.json"); after == nil || before == nil || after.ID == before.ID {
		t.Errorf("the same chat: %+v %+v", before, after)
	}
	if args := lastArgs(t, home); strings.Contains(args, "--resume") || strings.Contains(args, "--model") {
		t.Errorf("args: %q", args)
	}
	if r := row(t, "engineer"); !strings.Contains(r, "starting") {
		t.Errorf("ls: %q", r)
	}
	if log := readFile(t, c+"/.claude/build/state/prompts.log"); !strings.Contains(log, `"code":"`+code+`","kind":"safeguard","source":"transcript","event":"ended","ended":"by_code"`) {
		t.Errorf("log:\n%s", log)
	}
}

// The user's choice for N7: 1 <model> stops it and starts it again on its
// chat (claude --resume) with --model for this start only, so the user's
// default model is never touched, and the next plain start goes back to
// its own model. Its chat still ends in the stop until its next reply, and
// that stop no longer counts. A stop that comes back gets a new code, and
// 2 starts it fresh.
func TestASafeguardStopStartsAgainOnAnotherModel(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	code := stopMember(t, home, c)
	recPath := c + "/.claude/build/sessions/work-dev-engineer.json"
	before := session.ReadRecord(recPath)
	if n, _, errOut := call("answer", code, "1", "-x"); n != 1 || !strings.Contains(flat(errOut), "\"-x\" isn't a model name") {
		t.Errorf("-x: %d %q", n, errOut)
	}
	if args := lastArgs(t, home); strings.Contains(args, "--resume") {
		t.Fatalf("a refused answer restarted it: %q", args)
	}
	n, out, errOut := call("answer", code, "1", "sonnet")
	if n != 0 || !strings.Contains(out, "+ Stopped the engineer\n") || !strings.Contains(flat(out), "It picks up its chat where the safeguards stopped it, and waits for its next message.") ||
		!strings.Contains(flat(out), "If they stop it again, it gets a new code. Start it fresh with that code and 2: cadrei answer <code> 2.") || strings.Contains(out, "safeguards stopped the engineer's") {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	after := session.ReadRecord(recPath)
	if after == nil || before == nil || after.ID != before.ID || after.Model != "sonnet" || after.ModelFrom != session.FromStart ||
		!after.PastStop.Equal(time.Date(2026, 1, 1, 10, 0, 5, 100e6, time.UTC)) {
		t.Errorf("record: %+v %+v", before, after)
	}
	if args := lastArgs(t, home); !strings.Contains(args, "--resume "+before.ID) || !strings.Contains(args, "--model sonnet") {
		t.Errorf("args: %q", args)
	}
	if log := readFile(t, c+"/.claude/build/state/prompts.log"); !strings.Contains(log, `"code":"`+code+`","kind":"safeguard","source":"transcript","event":"ended","ended":"by_code"`) {
		t.Errorf("log:\n%s", log)
	}
	// Its first hook: idle, on its chat, and the old stop doesn't count.
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"SessionStart"}`)
	if r := row(t, "engineer"); !strings.Contains(r, "idle") || strings.Contains(r, "safeguards") {
		t.Errorf("ls: %q", r)
	}
	if st := startState(t, c, "work-dev-engineer"); st.Current() != nil {
		t.Errorf("a request after the restart: %+v", st.Current())
	}
	// A plain start resumes it again, past the stop, on its own model.
	must(t, "stop", "dev/engineer")
	if _, out, _ := call("up", "dev/engineer"); strings.Contains(out, "safeguards") {
		t.Errorf("up: %q", out)
	}
	if args := lastArgs(t, home); !strings.Contains(args, "--resume "+before.ID) || strings.Contains(args, "--model") {
		t.Errorf("args: %q", args)
	}
	// The stop comes back: a new code, and 2 starts it fresh.
	transcript := placeTranscript(t, c, "work-dev-engineer", "safeguard-stuck.jsonl", func(b []byte) []byte {
		return []byte(strings.ReplaceAll(string(b), "2026-01-01T10:00:05.100Z", "2026-01-01T10:30:00.000Z"))
	})
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop"}`)
	_, list, _ := call("answer")
	again := startState(t, c, "work-dev-engineer").Current()
	if again == nil || again.Kind != session.ForSafeguard || again.Code == code || !strings.Contains(list, again.Code+"  Claude's safeguards stopped") {
		t.Fatalf("the stop again: %+v\n%s", again, list)
	}
	if n, out, errOut := call("answer", again.Code, "2"); n != 0 || !strings.Contains(out, "+ Started the engineer (fresh chat)\n") {
		t.Errorf("fresh: %d %q %q", n, out, errOut)
	}
	if rec := session.ReadRecord(recPath); rec == nil || rec.ID == before.ID || !rec.PastStop.IsZero() {
		t.Errorf("fresh record: %+v", rec)
	}
	os.Remove(transcript)
}

// A start that fails after the stop leaves no mark: the stop still counts
// on the next start.
func TestAFailedRestartKeepsTheStop(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	code := stopMember(t, home, c)
	// The stub ends at once on --model: the start fails.
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+"[ -n \"$TMUX\" ] || exit 0\ncase \"$*\" in *--model*) exit 1 ;; esac\n"+stubReady+"exec sleep 300\n")
	if n, _, _ := call("answer", code, "1", "sonnet"); n == 0 {
		t.Error("a failed start exits 0")
	}
	if rec := session.ReadRecord(c + "/.claude/build/sessions/work-dev-engineer.json"); rec == nil || !rec.PastStop.IsZero() {
		t.Errorf("record: %+v", rec)
	}
	if _, out, _ := call("up", "dev/engineer"); !strings.Contains(flat(out), "safeguards stopped the engineer's last reply") {
		t.Errorf("up: %q", out)
	}
}
