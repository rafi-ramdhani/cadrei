package session

import (
	"crypto/rand"
	"encoding/json"
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
)

// State is a member's state file, <build>/state/<member name>.json.
type State struct {
	State      string    `json:"state"`
	Since      time.Time `json:"since"`
	Session    string    `json:"session,omitempty"`     // the conversation it is about
	WaitingFor string    `json:"waiting_for,omitempty"` // approval or question
	Error      bool      `json:"error,omitempty"`       // idle because the last turn hit an error
	Transcript string    `json:"transcript,omitempty"`  // the conversation's transcript, for its last activity
	PromptAt   time.Time `json:"prompt_at,omitempty"`   // when the last prompt arrived
	EndedAt    time.Time `json:"ended_at,omitempty"`    // when a turn last ended (Stop or StopFailure)
	// Request is what it waits on the user for, while it waits.
	Request *Request `json:"request,omitempty"`
}

// Request is what a waiting member asks the user, as the runtime recorded
// it, with the code cadrei gave it: an answer names the code, so it never
// lands on a request that changed.
type Request struct {
	Code     string   `json:"code"`
	Kind     string   `json:"kind"` // ForApproval or ForQuestion
	Tool     string   `json:"tool,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Question string   `json:"question,omitempty"`
	Options  []string `json:"options,omitempty"`
	Multi    bool     `json:"multi,omitempty"` // only its window can answer it
}

// Same reports whether two states say the same.
func Same(a, b *State) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, y := *a, *b
	x.Request, y.Request = nil, nil
	if x != y || (a.Request == nil) != (b.Request == nil) {
		return false
	}
	if a.Request == nil {
		return true
	}
	ra, rb := *a.Request, *b.Request
	return ra.Code == rb.Code && ra.Kind == rb.Kind && ra.Tool == rb.Tool && ra.Summary == rb.Summary &&
		ra.Question == rb.Question && ra.Multi == rb.Multi && slices.Equal(ra.Options, rb.Options)
}

// codeChars are what a request's code is made of: no 0, 1, l or o, which
// read alike.
const codeChars = "abcdefghijkmnpqrstuvwxyz23456789"

// CodeRule is a request code's shape.
var CodeRule = regexp.MustCompile("^[" + codeChars + "]{3}$")

// GiveCode gives a new request in s a code that no other state file in its
// folder has.
func GiveCode(path string, s *State) {
	if s == nil || s.Request == nil || s.Request.Code != "" {
		return
	}
	taken := map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.json"))
	for _, f := range files {
		if f == path {
			continue
		}
		if o := ReadState(f); o != nil && o.Request != nil {
			taken[o.Request.Code] = true
		}
	}
	for {
		var b [3]byte
		rand.Read(b[:])
		code := string([]byte{codeChars[int(b[0])%len(codeChars)], codeChars[int(b[1])%len(codeChars)], codeChars[int(b[2])%len(codeChars)]})
		if !taken[code] {
			s.Request.Code = code
			return
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
func Apply(prev *State, ev runtime.StateEvent, now time.Time) *State {
	if ev.Name == runtime.EvEnd {
		return nil
	}
	next := State{}
	if prev != nil {
		next = *prev
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
		next.WaitingFor = ""
		next.Error = false
		next.Request = nil
	}
	switch ev.Name {
	case runtime.EvPrompt:
		become(Working)
		next.PromptAt = now
	case runtime.EvToolUse:
		become(Working)
	case runtime.EvPermission:
		become(Waiting)
		next.WaitingFor = ForApproval
		if ev.Question {
			next.WaitingFor = ForQuestion
		}
		// A new request, so a new code (GiveCode).
		if r := ev.Request; r != nil {
			next.Request = &Request{Kind: next.WaitingFor, Tool: r.Tool, Summary: r.Summary, Question: r.Question,
				Options: slices.Clone(r.Options), Multi: r.Multi}
		}
	case runtime.EvStop:
		if prev != nil && prev.State == Working && !prev.PromptAt.IsZero() && now.Sub(prev.PromptAt) < queued {
			return &next // the turn the prompt starts is still going
		}
		become(Idle)
		next.EndedAt = now
	case runtime.EvStopFailure:
		become(Idle)
		next.Error, next.EndedAt = true, now
	case runtime.EvStart:
		become(Idle)
	case runtime.EvIdleReminder:
		if next.State != Idle {
			become(Idle)
		}
	default:
		if prev == nil {
			return nil
		}
		return prev
	}
	return &next
}

// StatePath is a member's state file in a cadrei's build folder, or "" for
// a name cadrei does not make.
func StatePath(build, name string) string {
	if !nameRule.MatchString(name) {
		return ""
	}
	return filepath.Join(build, "state", name+".json")
}

// ReadState reads a state file cadrei wrote, or nil.
func ReadState(path string) *State {
	raw, err := fsx.ReadOwn(path, 8192)
	if err != nil {
		return nil
	}
	var s State
	if json.Unmarshal(raw, &s) != nil || s.State == "" {
		return nil
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
