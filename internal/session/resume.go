package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// Record is what cadrei keeps of a session's conversation, to resume it on
// the next start: the conversation id, the folder it ran in, and when.
type Record struct {
	ID    string    `json:"id"`
	Dir   string    `json:"dir"`
	Since time.Time `json:"since"`
	// The model and effort the start passed (7.11), and where each was
	// set, or empty when it passed none.
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	ModelFrom  string `json:"model_from,omitempty"`
	EffortFrom string `json:"effort_from,omitempty"`
}

// Choice is the model and effort a record's start passed.
func (r *Record) Choice() Choice {
	if r == nil {
		return Choice{}
	}
	return Choice{Model: Setting{r.Model, r.ModelFrom}, Effort: Setting{r.Effort, r.EffortFrom}}
}

// nameRule is a session name as cadrei makes them; anything else names no
// record (the hook gets its name from the environment).
var nameRule = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// RecordPath is a session's record in a cadrei's build folder, or "" for a
// name cadrei does not make.
func RecordPath(build, name string) string {
	if !nameRule.MatchString(name) {
		return ""
	}
	return filepath.Join(build, "sessions", name+".json")
}

// ReadRecord reads a record cadrei wrote, or nil.
func ReadRecord(path string) *Record {
	raw, err := fsx.ReadOwn(path, 4096)
	if err != nil {
		return nil
	}
	var r Record
	if json.Unmarshal(raw, &r) != nil || r.ID == "" {
		return nil
	}
	return &r
}

// WriteRecord records a session's conversation.
func WriteRecord(path string, r Record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return fsx.WriteFile(path, append(raw, '\n'), 0o600)
}

// Forget removes a session's record, so its next start is a new
// conversation.
func Forget(path string) { os.Remove(path) }

// Conversation is how a session starts: the spec fields to set and what to
// say about it.
type Conversation struct {
	SessionID string // a new conversation with this id
	Resume    string // or the conversation to continue
	Note      string // "resumed its conversation", or why it starts new
}

// Plan decides whether a session resumes its recorded conversation or
// starts a new one, and why: never resumed when fresh is asked for, when
// there is no record, when the session works in another folder now (its
// conversation was about that folder), or when the runtime no longer has
// the conversation. A runtime that cannot resume starts new, silently.
func Plan(rt runtime.Runtime, record, dir string, fresh bool) Conversation {
	caps := rt.Caps()
	if !caps.Resume || !caps.AssignSessionID || record == "" {
		return Conversation{}
	}
	ops := rt.Sessions()
	c := Conversation{SessionID: ops.NewID()}
	r := ReadRecord(record)
	switch {
	case fresh:
		c.Note = "a new conversation, as asked"
	case r == nil:
		c.Note = "a new conversation"
	case paths.Real(r.Dir) != paths.Real(dir):
		c.Note = "a new conversation: it works in another folder now"
	case !ops.Exists(r.ID, r.Dir):
		c.Note = "a new conversation: the last one is gone"
	default:
		return Conversation{Resume: r.ID, Note: "resumed its conversation"}
	}
	return c
}
