//go:build !windows

package main

import (
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

// N7: a safeguard stop's code starts it fresh. Picking its model again
// stays in its window: Claude Code 2.1.304 saves a model picked there as
// the default for new sessions unless s is pressed, which would change
// the user's own settings. The guard's ask and the orchestrator's note
// say so.
func TestASafeguardStopsChoices(t *testing.T) {
	sandbox(t)
	w := stopped()
	if !answerable(w.req) {
		t.Fatal("not answerable")
	}
	window := "run /model in its window and press s to keep the pick to its session (a pick there is saved as your default for new sessions otherwise): cadrei attach dev"
	for choice, want := range map[string]string{
		"2":   "",
		"1":   "Cadrei doesn't pick its model for you. To pick it again, " + window,
		"3":   "g4t takes 2 (start it fresh) from here. Use cadrei answer g4t 2, or pick its model again in its window: cadrei attach dev",
		"yes": "g4t takes 2 (start it fresh) from here. Use cadrei answer g4t 2, or pick its model again in its window: cadrei attach dev",
	} {
		if why := checkChoice(w, "g4t", choice, plain); why != want {
			t.Errorf("%s: %q", choice, why)
		}
	}
	if got := askReason(w, "2"); got != "Cadrei: start the engineer in dev fresh? It stops, and starts again in a new chat that won't remember this one,"+
		" after Claude's safeguards stopped its last reply." {
		t.Errorf("ask: %q", got)
	}
	if key, why := keyOn("anything", w, "2", plain); key != "2" || why != "" {
		t.Errorf("key: %q %q", key, why)
	}
	if n := w.note(); n != "The engineer in dev (g4t) was stopped by Claude's safeguards, so it won't answer. To start it fresh (a new chat that won't remember this one),"+
		" the user types ! cadrei answer g4t 2. To pick its model again instead, they "+window+"." {
		t.Errorf("note: %q", n)
	}
}

// stopMember starts the engineer in dev, idle, with a safeguard stop in
// its transcript long enough ago for the runtime's own retry to be over,
// and returns the stop's code, from cadrei answer's listing.
func stopMember(t *testing.T, home, c string) string {
	t.Helper()
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+"[ -n \"$TMUX\" ] || exit 0\n"+stubReady+"exec sleep 300\n")
	must(t, "up", "dev/engineer")
	t.Cleanup(func() { session.Default().KillSession(session.SessionName("work", "dev")) })
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop"}`)
	placeTranscript(t, c, "work-dev-engineer", "safeguard-stuck.jsonl", nil)
	ownTerminal(t)
	_, list, _ := call("answer")
	for _, want := range []string{"Claude's safeguards stopped the last reply of the engineer in dev, so it won't answer:",
		"1. Pick its model again (only in its window)", "2. Start it fresh (a new chat that won't remember this one)",
		"Start it fresh: cadrei answer "} {
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

// N7, e2e case 11: 2 stops it and starts it again in a new chat; the
// prompt log notes the stop was answered by code. 1 presses nothing.
func TestASafeguardStopStartsItFresh(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	code := stopMember(t, home, c)
	if n, _, errOut := call("answer", code, "1"); n != 1 || !strings.Contains(flat(errOut), "doesn't pick its model for you") {
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
	if r := row(t, "engineer"); !strings.Contains(r, "starting") {
		t.Errorf("ls: %q", r)
	}
	if log := readFile(t, c+"/.claude/build/state/prompts.log"); !strings.Contains(log, `"code":"`+code+`","kind":"safeguard","source":"transcript","event":"ended","ended":"by_code"`) {
		t.Errorf("log:\n%s", log)
	}
}
