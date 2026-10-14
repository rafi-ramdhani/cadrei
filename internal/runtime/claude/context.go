package claude

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// contextChunk is how much of a transcript Context reads at a time, from
// its end, and contextLimit how much at most: a long chat's transcript is
// tens of megabytes, and the last reply is near its end.
const (
	contextChunk = 64 << 10
	contextLimit = 16 << 20
)

// Windows cadrei can be sure of: a model setting with Claude Code's
// [1m] suffix, and a Haiku. Any other model's window depends on the
// account and the version, so it isn't guessed.
const (
	millionWindow = 1_000_000
	haikuWindow   = 200_000
)

// Context reads the last main-chain reply's usage from the end of the
// transcript: input, cache writes, cache reads and output, the size of
// that request, which is the context in use. A compact boundary after the
// last reply says the conversation was compacted, and the new size isn't
// known until the next reply. Replies of subagents (isSidechain) and
// Claude Code's own stand-ins for errors (model "<synthetic>") don't
// count.
func (sessions) Context(transcript, model string) (runtime.ContextUse, bool) {
	if transcript == "" {
		return runtime.ContextUse{}, false
	}
	f, err := os.Open(transcript)
	if err != nil {
		return runtime.ContextUse{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return runtime.ContextUse{}, false
	}
	var tail []byte // the start of a line cut by the chunk before it
	for end := st.Size(); end > 0 && st.Size()-end < contextLimit; {
		start := max(end-contextChunk, 0)
		buf := make([]byte, end-start, int(end-start)+len(tail))
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return runtime.ContextUse{}, false
		}
		buf = append(buf, tail...)
		// Every line but the first, which may be cut, from the last one up.
		lines := bytes.Split(buf, []byte("\n"))
		first := 0
		if start > 0 {
			first = 1
		}
		for i := len(lines) - 1; i >= first; i-- {
			if c, ok := contextOf(lines[i], model); ok {
				return c, true
			}
		}
		if start > 0 {
			tail = lines[0]
		}
		end = start
	}
	return runtime.ContextUse{}, false
}

// contextOf reads one transcript line: a main-chain reply with usage, or
// a compact boundary.
func contextOf(line []byte, model string) (runtime.ContextUse, bool) {
	if !bytes.Contains(line, []byte(`"assistant"`)) && !bytes.Contains(line, []byte(`"compact_boundary"`)) {
		return runtime.ContextUse{}, false
	}
	var e struct {
		Type      string    `json:"type"`
		Subtype   string    `json:"subtype"`
		Sidechain bool      `json:"isSidechain"`
		Timestamp time.Time `json:"timestamp"`
		Message   struct {
			Model string `json:"model"`
			Usage *struct {
				Input       int `json:"input_tokens"`
				CacheWrites int `json:"cache_creation_input_tokens"`
				CacheReads  int `json:"cache_read_input_tokens"`
				Output      int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &e) != nil || e.Sidechain {
		return runtime.ContextUse{}, false
	}
	switch {
	case e.Type == "system" && e.Subtype == "compact_boundary":
		return runtime.ContextUse{Compacted: e.Timestamp}, true
	case e.Type == "assistant" && e.Message.Usage != nil && e.Message.Model != "<synthetic>":
		u := e.Message.Usage
		return runtime.ContextUse{Tokens: u.Input + u.CacheWrites + u.CacheReads + u.Output, Window: window(model, e.Message.Model), At: e.Timestamp}, true
	}
	return runtime.ContextUse{}, false
}

// window is a model's context window when it's certain, else 0: setting
// is cadrei's model setting for the session, used the model the reply
// names.
func window(setting, used string) int {
	switch {
	case strings.HasSuffix(strings.ToLower(setting), "[1m]"):
		return millionWindow
	case strings.Contains(strings.ToLower(used), "haiku"):
		return haikuWindow
	}
	return 0
}
