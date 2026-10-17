package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// The prompt log is how each request ended, crew-wide, so cadrei can tell
// whether every prompt went through the orchestrator: a line when a
// request opens, and one when it ends, by its code (cadrei answer), in
// the member's window (its call ended, or it left the screen, with no
// answer by code), or
// cancelled (its turn ended, or another took its place). In the window is
// the miss it counts.
//
// It notes the code, the member, the kind, the tool and where the record
// came from: never what was asked. It lives in the build folder's
// state/prompts.log, and past promptLogLimit moves to prompts.log.1.

// PromptEntry is one line of the prompt log.
type PromptEntry struct {
	T      time.Time `json:"t"`
	Member string    `json:"member"`
	Code   string    `json:"code"`
	Kind   string    `json:"kind"`
	Tool   string    `json:"tool,omitempty"`
	Source string    `json:"source,omitempty"`
	Event  string    `json:"event"`           // PromptOpened or PromptEnded
	Ended  string    `json:"ended,omitempty"` // how, for PromptEnded
}

// Prompt log events, and how a request ended.
const (
	PromptOpened = "opened"
	PromptEnded  = "ended"

	EndedByCode    = "by_code"
	EndedInWindow  = "in_window"
	EndedCancelled = "cancelled"
)

// promptLogLimit is how big the prompt log grows before it's moved aside.
const promptLogLimit = 256 << 10

// PromptLogPath is a cadrei's prompt log in its build folder.
func PromptLogPath(build string) string { return filepath.Join(build, "state", "prompts.log") }

// LogPrompts notes in build's prompt log what changed for member's
// requests from prev to next: each one that opened, and each one that
// ended, as how says for its code, or by its code when an answer by code
// had begun (whatever ended it first: the answer, or the tool's end).
func LogPrompts(build, member string, prev, next *State, how func(code string) string, now time.Time) {
	was, is := map[string]bool{}, map[string]bool{}
	for _, r := range prev.Requests() {
		was[r.Code] = true
	}
	for _, r := range next.Requests() {
		is[r.Code] = true
	}
	var lines []PromptEntry
	for _, r := range prev.Requests() {
		if r.Code != "" && !is[r.Code] {
			ended := how(r.Code)
			if r.ByCode(now) {
				ended = EndedByCode
			}
			lines = append(lines, PromptEntry{T: now, Member: member, Code: r.Code, Kind: r.Kind, Tool: r.Tool, Source: r.Source, Event: PromptEnded, Ended: ended})
		}
	}
	for _, r := range next.Requests() {
		if r.Code != "" && !was[r.Code] {
			lines = append(lines, PromptEntry{T: now, Member: member, Code: r.Code, Kind: r.Kind, Tool: r.Tool, Source: r.Source, Event: PromptOpened})
		}
	}
	if len(lines) == 0 {
		return
	}
	path := PromptLogPath(build)
	// Every member's hooks write here: one takes the log's own lock to
	// move it aside, and appends don't need it.
	if st, err := os.Lstat(path); err == nil && st.Size() > promptLogLimit {
		if lock, err := fsx.Acquire(path+".lock", time.Second); err == nil {
			if st, err := os.Lstat(path); err == nil && st.Mode().IsRegular() && st.Size() > promptLogLimit {
				os.Rename(path, path+".1")
			}
			lock.Release()
		}
	}
	for _, l := range lines {
		appendJSON(path, l)
	}
}

// Ending says how a member's requests end on an event: the end of their
// call, or an idle reminder with no prompt on the member's screen (a no
// or Esc there), is an answer in the member's window, and anything else
// (a new prompt, the turn's end, another request taking a place) a
// cancel.
func Ending(event string) func(string) string {
	return func(string) string {
		if event == runtime.EvToolDone || event == runtime.EvIdleReminder {
			return EndedInWindow
		}
		return EndedCancelled
	}
}

// PromptCounts sums up a prompt log.
type PromptCounts struct {
	Opened, ByCode, InWindow, Cancelled int
}

// CountPrompts reads build's prompt log, both files.
func CountPrompts(build string) PromptCounts {
	var c PromptCounts
	path := PromptLogPath(build)
	for _, p := range []string{path + ".1", path} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var l PromptEntry
			if json.Unmarshal(sc.Bytes(), &l) != nil {
				continue
			}
			switch {
			case l.Event == PromptOpened:
				c.Opened++
			case l.Ended == EndedByCode:
				c.ByCode++
			case l.Ended == EndedInWindow:
				c.InWindow++
			case l.Ended == EndedCancelled:
				c.Cancelled++
			}
		}
		f.Close()
	}
	return c
}
