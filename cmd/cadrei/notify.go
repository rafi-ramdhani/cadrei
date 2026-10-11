//go:build !windows

package main

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// notifyNote is what the orchestrator is told after it sent a member a
// message without asking to hear when the member goes idle: a fixed text,
// naming nothing from the message.
const notifyNote = "From cadrei, not the user: that message went to a member of your crew without notify_when_idle, " +
	"so you won't hear when the member goes idle. Send the same member a pure subscription now: " +
	"SendMessage to the same target with notify_when_idle: true and no message."

// notifyLimit is how long the hook may take before it prints nothing.
var notifyLimit = 2 * time.Second

// notifyStallForTests, when set, holds the hook's work up that long.
var notifyStallForTests time.Duration

// runHookNotify is cadrei hook notify, the orchestrator's hook after each
// SendMessage call. Its contract: it always exits 0, and prints nothing or
// one JSON object that only adds the note above to the orchestrator's
// context. It never blocks, denies or decides anything, and never changes
// the call. It prints the note only for a message the main conversation
// sent to a member of its own crew, by the member's name, without
// notify_when_idle; for anything else, any input it can't read, any lookup
// that fails, or one that takes too long, it prints nothing.
func runHookNotify(e *env) int {
	defer func() { recover() }()
	if inMember() || os.Getenv("CADREI_ORCHESTRATOR") == "" || os.Getenv("CADREI_HOME") == "" {
		return 0
	}
	input, _ := io.ReadAll(io.LimitReader(e.stdin, 1<<20))
	// Read once: the work below can outlast the hook.
	stall, limit := notifyStallForTests, notifyLimit
	done := make(chan []byte, 1)
	go func() {
		defer func() { recover() }()
		time.Sleep(stall)
		done <- notifyAnswer(input)
	}()
	select {
	case out := <-done:
		e.stdout.Write(out)
	case <-time.After(limit):
	}
	return 0
}

// notifyAnswer is the hook's output for one sent message: the note, or
// nothing.
func notifyAnswer(input []byte) []byte {
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return nil
	}
	sent, ok := rt.Sessions().Sent(strings.NewReader(string(input)))
	if !ok || sent.Notify || sent.Subagent {
		return nil
	}
	c := hookCrew(os.Getenv("CADREI_HOME"))
	if c == nil {
		return nil
	}
	// A name as SendMessage takes it, maybe with a [ref] to tell two apart.
	name, _, _ := strings.Cut(strings.TrimSpace(sent.To), " ")
	if !memberOf(*c, name) {
		return nil
	}
	return rt.Sessions().SentContext(notifyNote)
}
