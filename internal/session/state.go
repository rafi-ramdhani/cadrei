package session

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// What a running member is doing, as its hooks report it: cadrei learns it
// from the runtime's hook events, never by reading the member's screen.
const (
	Starting = "starting" // its window runs, and no hook has written yet
	Working  = "working"
	Waiting  = "waiting" // on the user: an approval or a question in its window
	Idle     = "idle"
	Unknown  = "unknown" // no state to go by
	// SafeguardStopped: the runtime's safeguards stopped its last reply,
	// so it won't answer (read from its transcript, not from a hook).
	SafeguardStopped = "stopped_by_safeguards"
)

// What waiting is for.
const (
	ForApproval = "approval"
	ForQuestion = "question"
	// ForPrompt: a prompt cadrei read off the member's screen, when no
	// hook said what it asks (the catch-all).
	ForPrompt = "prompt"
)

// Where a request's record came from.
const (
	FromHook   = "hook"   // the runtime's hook input
	FromScreen = "screen" // the member's screen
)

// Limits on what a state holds: a member rarely has more than one prompt
// open, so the oldest go first past these.
const (
	MaxRequests = 8
	maxCalls    = 32
)

// State is a member's state file, <build>/state/<member name>.json.
type State struct {
	State      string    `json:"state"`
	Since      time.Time `json:"since"`
	Session    string    `json:"session,omitempty"`     // the conversation it is about
	WaitingFor string    `json:"waiting_for,omitempty"` // approval, question or prompt
	Error      bool      `json:"error,omitempty"`       // idle because the last turn hit an error
	Transcript string    `json:"transcript,omitempty"`  // the conversation's transcript, for its last activity
	PromptAt   time.Time `json:"prompt_at,omitempty"`   // when the last prompt arrived
	EndedAt    time.Time `json:"ended_at,omitempty"`    // when a turn last ended (Stop or StopFailure)
	// Open is what it waits on the user for, oldest first, while it
	// waits: each prompt its own request, with its own code.
	Open []*Request `json:"requests,omitempty"`
	// Calls are its tool calls that started and haven't ended, so a
	// permission request, which names no call, is matched to its own.
	Calls []Call `json:"calls,omitempty"`
	// Legacy is the one request a 0.2.2 state file held. ReadState moves
	// it into Open.
	Legacy *Request `json:"request,omitempty"`
}

// Call is a tool call that started: the runtime's id for it, the tool and
// a digest of what the tool got, never the input itself.
type Call struct {
	ID   string `json:"id"`
	Tool string `json:"tool,omitempty"`
	Key  string `json:"key,omitempty"`
}

// Request is what a waiting member asks the user, as the runtime recorded
// it, with the code cadrei gave it: an answer names the code, so it never
// lands on a request that changed.
type Request struct {
	Code     string   `json:"code"`
	Kind     string   `json:"kind"` // ForApproval, ForQuestion or ForPrompt
	Tool     string   `json:"tool,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Question string   `json:"question,omitempty"` // a question's text, or a screen prompt's box
	Options  []string `json:"options,omitempty"`
	Multi    bool     `json:"multi,omitempty"` // only its window can answer it
	// Call is the id of the tool call it belongs to, when cadrei knows it:
	// that call's end ends the request.
	Call string `json:"call,omitempty"`
	// Maybe are the open calls it may belong to, when cadrei couldn't tell
	// which: it's theirs once all but one of them end.
	Maybe  []string `json:"maybe,omitempty"`
	Source string   `json:"source,omitempty"` // FromHook or FromScreen
	// Answering is when an answer by its code began: its key is pressed
	// right after, so the tool's end may come before the answer drops it.
	Answering time.Time `json:"answering,omitempty"`
}

// answeringFor is how long an answer by code's mark counts: the answer
// takes seconds, and a mark left by one that died must not count later.
const answeringFor = 30 * time.Second

// ByCode reports whether r is being answered by its code at now.
func (r *Request) ByCode(now time.Time) bool {
	return !r.Answering.IsZero() && now.Sub(r.Answering) < answeringFor
}

// Requests are the state's open requests, oldest first.
func (s *State) Requests() []*Request {
	if s == nil {
		return nil
	}
	return s.Open
}

// Current is the oldest open request, or nil.
func (s *State) Current() *Request {
	if s == nil || len(s.Open) == 0 {
		return nil
	}
	return s.Open[0]
}

// Same reports whether two states say the same.
func Same(a, b *State) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}

// codeChars are what a request's code is made of: no 0, 1, l or o, which
// read alike.
const codeChars = "abcdefghijkmnpqrstuvwxyz23456789"

// CodeRule is a request code's shape.
var CodeRule = regexp.MustCompile("^[" + codeChars + "]{3}$")

// GiveCode gives each new request in s a code that no other request in
// its folder's state files, nor in s, has.
func GiveCode(path string, s *State) {
	if s == nil {
		return
	}
	taken := map[string]bool{}
	need := false
	for _, r := range s.Open {
		taken[r.Code] = true
		need = need || r.Code == ""
	}
	if !need {
		return
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.json"))
	for _, f := range files {
		if f == path {
			continue
		}
		for _, r := range ReadState(f).Requests() {
			taken[r.Code] = true
		}
	}
	for _, r := range s.Open {
		for r.Code == "" {
			var b [3]byte
			rand.Read(b[:])
			code := string([]byte{codeChars[int(b[0])%len(codeChars)], codeChars[int(b[1])%len(codeChars)], codeChars[int(b[2])%len(codeChars)]})
			if !taken[code] {
				r.Code, taken[code] = code, true
			}
		}
	}
}

// queued is how close a Stop may come after a prompt and still belong to
// the turn before it: a prompt queued at the end of a turn can arrive
// just before that turn's Stop.
const queued = time.Second

// Apply gives the state after event, from prev (nil for none). It returns
// nil when the state file should go (the session ended), and prev itself
// when nothing changes.
//
// Each prompt is its own request. A tool call's start is noted (Calls), and
// a question is recorded right then, from what the tool got, before its
// dialog draws. A permission request, which names no call, is matched to
// the open call with the same tool and input. A call's end ends only its
// own request, so another tool finishing while a prompt waits leaves it
// waiting. A new prompt, a turn's end and a new session end them all.
func Apply(prev *State, ev runtime.StateEvent, now time.Time) *State {
	if ev.Name == runtime.EvEnd {
		return nil
	}
	next := State{}
	if prev != nil {
		next = *prev
		next.Open = slices.Clone(prev.Open)
		next.Calls = slices.Clone(prev.Calls)
	}
	if ev.Session != "" {
		next.Session = ev.Session
	}
	if ev.Transcript != "" {
		next.Transcript = ev.Transcript
	}
	become := func(state string) {
		if next.State != state || prev == nil {
			next.Since = now
		}
		next.State = state
		next.Error = false
	}
	clear := func() { next.Open, next.Calls, next.WaitingFor = nil, nil, "" }
	// settle: waiting while a request is open, working once none is.
	settle := func() {
		if len(next.Open) > 0 {
			become(Waiting)
			return
		}
		if next.State == Waiting {
			next.WaitingFor = ""
		}
		become(Working)
	}
	switch ev.Name {
	case runtime.EvPrompt:
		clear()
		become(Working)
		next.PromptAt = now
	case runtime.EvToolUse:
		if ev.ToolID != "" {
			next.addCall(Call{ID: ev.ToolID, Tool: ev.Tool, Key: ev.InputKey})
		}
		if ev.Question && ev.Request != nil && (ev.ToolID == "" || next.requestOf(ev.ToolID) == nil) {
			next.add(record(ForQuestion, ev.Request, ev.ToolID, FromHook))
		}
		settle()
	case runtime.EvToolDone:
		next.end(ev.ToolID, ev.Tool)
		settle()
	case runtime.EvPermission:
		kind := ForApproval
		if ev.Question {
			kind = ForQuestion
		}
		call := next.callFor(ev.Tool, ev.InputKey)
		switch {
		case ev.Request == nil:
			next.WaitingFor = kind
		case call != "" && next.requestOf(call) != nil:
			// Recorded at its call's start already: the same request,
			// with the same code.
		default:
			r := record(kind, ev.Request, call, FromHook)
			if call == "" {
				// With no call to go by, a new prompt takes the place of
				// any other that names none: two such at once is rare, and
				// one left from a prompt already answered isn't.
				next.Open = slices.DeleteFunc(next.Open, func(r *Request) bool { return r.Call == "" })
				r.Maybe = next.openCalls(ev.Tool)
			}
			next.add(r)
		}
		become(Waiting)
	case runtime.EvPromptShown:
		// The catch-all: a prompt shows and no hook recorded one, so the
		// one read off the screen stands for it.
		if len(next.Open) > 0 || ev.Request == nil {
			return unchanged(prev)
		}
		next.add(record(ForPrompt, ev.Request, next.loneCall(), FromScreen))
		become(Waiting)
	case runtime.EvStop:
		if prev != nil && prev.State == Working && !prev.PromptAt.IsZero() && now.Sub(prev.PromptAt) < queued {
			return &next // the turn the prompt starts is still going
		}
		clear()
		become(Idle)
		next.EndedAt = now
	case runtime.EvStopFailure:
		clear()
		become(Idle)
		next.Error, next.EndedAt = true, now
	case runtime.EvStart:
		clear()
		become(Idle)
	case runtime.EvIdleReminder:
		// The runtime reminds the user of an idle input line: over a
		// prompt cadrei still has open only when the member's screen shows
		// none (a no or Esc in its window ended it, with no hook to say
		// so), since the reminder can come while a slow prompt is up.
		if next.State != Idle && (len(next.Open) == 0 || ev.NoPrompt) {
			clear()
			become(Idle)
		}
	default:
		return unchanged(prev)
	}
	if r := next.Current(); r != nil {
		next.WaitingFor = r.Kind
	} else if next.State != Waiting {
		next.WaitingFor = ""
	}
	return &next
}

// unchanged is Apply's answer for an event that changes nothing.
func unchanged(prev *State) *State {
	if prev == nil {
		return nil
	}
	return prev
}

// record is a new request from what the runtime (or the screen) said.
func record(kind string, r *runtime.Request, call, source string) *Request {
	return &Request{Kind: kind, Tool: r.Tool, Summary: r.Summary, Question: r.Question,
		Options: slices.Clone(r.Options), Multi: r.Multi, Call: call, Source: source}
}

// add opens a request, dropping the oldest past MaxRequests.
func (s *State) add(r *Request) {
	if len(s.Open) >= MaxRequests {
		s.Open = s.Open[len(s.Open)-MaxRequests+1:]
	}
	s.Open = append(s.Open, r)
}

// addCall notes a call's start, dropping the oldest past maxCalls.
func (s *State) addCall(c Call) {
	s.Calls = slices.DeleteFunc(s.Calls, func(o Call) bool { return o.ID == c.ID })
	if len(s.Calls) >= maxCalls {
		s.Calls = s.Calls[len(s.Calls)-maxCalls+1:]
	}
	s.Calls = append(s.Calls, c)
}

// requestOf is the open request of call id, or nil.
func (s *State) requestOf(id string) *Request {
	for _, r := range s.Open {
		if id != "" && r.Call == id {
			return r
		}
	}
	return nil
}

// callFor is the open call a permission request for tool, with input
// digest key, belongs to: the oldest with the same tool and input that has
// no request yet, else the oldest with the same tool and input, else the
// one open call of that tool. "" when none fits.
func (s *State) callFor(tool, key string) string {
	if tool == "" {
		return ""
	}
	var same []Call
	for _, c := range s.Calls {
		if c.Tool == tool && key != "" && c.Key == key {
			same = append(same, c)
		}
	}
	for _, c := range same {
		if s.requestOf(c.ID) == nil {
			return c.ID
		}
	}
	if len(same) > 0 {
		return same[0].ID
	}
	var only string
	for _, c := range s.Calls {
		if c.Tool == tool {
			if only != "" {
				return ""
			}
			only = c.ID
		}
	}
	return only
}

// openCalls are the open calls of tool with no request, when there's more
// than one: those a request that names none may belong to.
func (s *State) openCalls(tool string) []string {
	var ids []string
	for _, c := range s.Calls {
		if c.Tool == tool && s.requestOf(c.ID) == nil {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) < 2 {
		return nil
	}
	return ids
}

// loneCall is the one open call with no request, or "".
func (s *State) loneCall() string {
	var only string
	for _, c := range s.Calls {
		if s.requestOf(c.ID) == nil {
			if only != "" {
				return ""
			}
			only = c.ID
		}
	}
	return only
}

// end ends call id: its note, and its request. A request that may be one
// of several calls' is none of the call that ended (a call waiting on a
// prompt doesn't end), and once one is left, it's that one's. A call
// whose start cadrei never saw, with no request of its own, ends the
// oldest request of the same tool that names no call: its permission
// request came without the call's start too.
func (s *State) end(id, tool string) {
	if id == "" {
		return
	}
	known := slices.ContainsFunc(s.Calls, func(c Call) bool { return c.ID == id })
	s.Calls = slices.DeleteFunc(s.Calls, func(c Call) bool { return c.ID == id })
	if r := s.requestOf(id); r != nil {
		s.Open = slices.DeleteFunc(s.Open, func(o *Request) bool { return o == r })
		return
	}
	for i, r := range s.Open {
		if slices.Contains(r.Maybe, id) {
			// A copy: the request is prev's too.
			c := *r
			c.Maybe = slices.DeleteFunc(slices.Clone(r.Maybe), func(m string) bool { return m == id })
			if len(c.Maybe) == 1 {
				c.Call, c.Maybe = c.Maybe[0], nil
			}
			s.Open[i] = &c
			return
		}
	}
	if known {
		return
	}
	for i, r := range s.Open {
		if r.Call == "" && len(r.Maybe) == 0 && tool != "" && r.Tool == tool {
			s.Open = slices.Delete(s.Open, i, i+1)
			return
		}
	}
}

// Drop ends the open request under code: the user answered it. It reports
// whether there was one.
func (s *State) Drop(code string) bool {
	n := len(s.Open)
	s.Open = slices.DeleteFunc(s.Open, func(r *Request) bool { return r.Code == code })
	return len(s.Open) < n
}

// StatePath is a member's state file in a cadrei's build folder, or "" for
// a name cadrei does not make.
func StatePath(build, name string) string {
	if !nameRule.MatchString(name) {
		return ""
	}
	return filepath.Join(build, "state", name+".json")
}

// LockPath is the lock a member's state hooks take around a change of its
// state file, or "" for a name cadrei does not make.
func LockPath(build, name string) string {
	p := StatePath(build, name)
	if p == "" {
		return ""
	}
	return strings.TrimSuffix(p, ".json") + ".lock"
}

// ChangeState changes member name's state file in build under the member's
// lock, waiting up to wait for it: change gets what the file holds (nil
// for none) and returns what it should hold (nil removes it). A new
// request gets its code. The file is written only when it changes. It
// returns the state before and after; on an error, nothing changed.
func ChangeState(build, name string, wait time.Duration, change func(prev *State) *State) (prev, next *State, err error) {
	path, lockPath := StatePath(build, name), LockPath(build, name)
	if path == "" {
		return nil, nil, errors.New("no state file for " + name)
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, nil, err
	}
	lock, err := fsx.Acquire(lockPath, wait)
	if err != nil {
		return nil, nil, err
	}
	defer lock.Release()
	prev = ReadState(path)
	next = change(prev)
	if (prev == nil) != (next == nil) || (next != nil && !Same(next, prev)) {
		GiveCode(path, next)
		if err := WriteState(path, next); err != nil {
			return prev, prev, err
		}
	}
	return prev, next, nil
}

// ReadState reads a state file cadrei wrote, or nil.
func ReadState(path string) *State {
	raw, err := fsx.ReadOwn(path, 64<<10)
	if err != nil {
		return nil
	}
	var s State
	if json.Unmarshal(raw, &s) != nil || s.State == "" {
		return nil
	}
	if s.Legacy != nil {
		if len(s.Open) == 0 {
			s.Open = []*Request{s.Legacy}
		}
		s.Legacy = nil
	}
	return &s
}

// WriteState writes a state file whole (a new file, then a rename), or
// removes it for nil.
func WriteState(path string, s *State) error {
	if s == nil {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	// A temp file and a rename, without a sync: a state is throwaway, and
	// a sync on every tool event costs more than it's worth.
	return fsx.WriteFileNoSync(path, append(raw, '\n'), 0o600)
}

// safeguardCheck is a member's last safeguard check, kept with the
// transcript's path, size and modification time, so an unchanged
// transcript is not read again on every prompt.
type safeguardCheck struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Mod     time.Time `json:"mod"`
	Stopped bool      `json:"stopped"`
	At      time.Time `json:"at,omitempty"`
}

// Safeguard says whether the runtime's safeguards stopped member name's
// last reply in transcript, and when, through its cache in the build
// folder's state/<name>.safeguard.
func Safeguard(ops runtime.SessionOps, build, name, transcript string) (time.Time, bool) {
	st, err := os.Stat(transcript)
	if transcript == "" || err != nil || !st.Mode().IsRegular() {
		return time.Time{}, false
	}
	cache := ""
	if p := StatePath(build, name); p != "" {
		cache = strings.TrimSuffix(p, ".json") + ".safeguard"
	}
	var c safeguardCheck
	if raw, err := fsx.ReadOwn(cache, 4096); err == nil && json.Unmarshal(raw, &c) == nil &&
		c.Path == transcript && c.Size == st.Size() && c.Mod.Equal(st.ModTime()) {
		return c.At, c.Stopped
	}
	at, stopped := ops.SafeguardStop(transcript)
	if cache != "" {
		c = safeguardCheck{Path: transcript, Size: st.Size(), Mod: st.ModTime(), Stopped: stopped, At: at}
		if raw, err := json.Marshal(c); err == nil {
			// Throwaway, as a state is: a torn or lost cache only costs one
			// more read of the transcript's tail, so it isn't flushed to
			// disk (a full flush on macOS costs milliseconds per member).
			if os.MkdirAll(filepath.Dir(cache), 0o755) == nil {
				fsx.WriteFileNoSync(cache, append(raw, '\n'), 0o600)
			}
		}
	}
	return at, stopped
}
