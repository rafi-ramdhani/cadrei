package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reply is a transcript line for a reply, as Claude Code writes one.
func reply(model string, in, writes, reads, out int, sidechain bool, at string) string {
	return fmt.Sprintf(`{"type":"assistant","isSidechain":%v,"timestamp":%q,"message":{"model":%q,"role":"assistant","usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d,"service_tier":"standard"}}}`,
		sidechain, at, model, in, writes, reads, out)
}

func transcript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const (
	user     = `{"type":"user","isSidechain":false,"timestamp":"2026-10-14T10:00:00Z","message":{"role":"user","content":"hello"}}`
	boundary = `{"type":"system","subtype":"compact_boundary","isSidechain":false,"timestamp":"2026-10-14T11:00:00Z","compactMetadata":{"trigger":"manual","preTokens":180000}}`
)

func TestContext(t *testing.T) {
	s := sessions{}
	at := "2026-10-14T10:30:00Z"
	when, _ := time.Parse(time.RFC3339, at)
	for _, tc := range []struct {
		name, setting string
		lines         []string
		want          int // tokens, or -1 for nothing to say
		window        int
		compacted     bool
	}{
		{"the last reply", "", []string{user, reply("claude-opus-5-5", 4, 1718, 140000, 704, false, at)}, 142426, 0, false},
		{"an earlier reply doesn't count", "", []string{reply("claude-opus-5-5", 1, 0, 1000, 1, false, at), user, reply("claude-opus-5-5", 2, 0, 5000, 3, false, at)}, 5005, 0, false},
		{"a subagent's reply after it doesn't count", "", []string{reply("claude-opus-5-5", 1, 0, 9000, 1, false, at), reply("claude-haiku-5-5", 5, 0, 100, 5, true, at)}, 9002, 0, false},
		{"an error's stand-in doesn't count", "", []string{reply("claude-opus-5-5", 1, 0, 9000, 1, false, at), reply("<synthetic>", 0, 0, 0, 0, false, at)}, 9002, 0, false},
		{"compacted, nothing said since", "", []string{reply("claude-opus-5-5", 1, 0, 180000, 1, false, at), boundary, user}, 0, 0, true},
		{"compacted, then a reply", "", []string{reply("claude-opus-5-5", 1, 0, 180000, 1, false, at), boundary, user, reply("claude-opus-5-5", 3, 2000, 18000, 400, false, at)}, 20403, 0, false},
		{"a 1M setting", "opus[1m]", []string{reply("claude-opus-5-5", 0, 0, 580000, 0, false, at)}, 580000, millionWindow, false},
		{"a 1M setting by its id", "claude-opus-5-5[1m]", []string{reply("claude-opus-5-5-20260901", 0, 0, 580000, 0, false, at)}, 580000, millionWindow, false},
		// Review M1: /model to another model, the setting says nothing.
		{"a 1M setting, then /model sonnet", "opus[1m]", []string{reply("claude-sonnet-5-5", 0, 0, 182000, 0, false, at)}, 182000, 0, false},
		{"a 1M id setting, then /model to another", "claude-opus-5-5[1m]", []string{reply("claude-opus-4-6", 0, 0, 182000, 0, false, at)}, 182000, 0, false},
		{"a 1M setting, then /model haiku", "sonnet[1m]", []string{reply("claude-haiku-4-5-20251001", 0, 0, 170000, 0, false, at)}, 170000, haikuWindow, false},
		{"a Haiku", "", []string{reply("claude-haiku-4-5-20251001", 0, 0, 170000, 0, false, at)}, 170000, haikuWindow, false},
		{"an older Haiku", "", []string{reply("claude-3-5-haiku-20241022", 0, 0, 170000, 0, false, at)}, 170000, haikuWindow, false},
		// Review L1: a Haiku cadrei doesn't know the window of.
		{"a later Haiku", "", []string{reply("claude-haiku-5-5", 0, 0, 170000, 0, false, at)}, 170000, 0, false},
		{"no reply yet", "", []string{user}, -1, 0, false},
		{"a line it can't read", "", []string{reply("claude-opus-5-5", 1, 0, 9000, 1, false, at), `{"type":"assistant", broken`}, 9002, 0, false},
	} {
		c, ok := s.Context(transcript(t, tc.lines...), tc.setting)
		switch {
		case tc.want < 0:
			if ok {
				t.Errorf("%s: %+v", tc.name, c)
			}
		case !ok || c.Tokens != tc.want || c.Window != tc.window || c.Compacted.IsZero() == tc.compacted:
			t.Errorf("%s: %+v %v", tc.name, c, ok)
		case !tc.compacted && !c.At.Equal(when):
			t.Errorf("%s: at %v", tc.name, c.At)
		}
	}
	if _, ok := s.Context("", ""); ok {
		t.Error("no transcript")
	}
	if _, ok := s.Context(filepath.Join(t.TempDir(), "gone.jsonl"), ""); ok {
		t.Error("a transcript that's gone")
	}
}

// A long chat: the reply sits behind megabytes of later lines, across
// chunk edges, and is still found; one further back than the limit isn't.
func TestContextInALongTranscript(t *testing.T) {
	filler := `{"type":"attachment","isSidechain":false,"timestamp":"2026-10-14T10:00:00Z","attachment":"` + strings.Repeat("x", 997) + `"}`
	lines := []string{reply("claude-opus-5-5", 1, 0, 50000, 1, false, "2026-10-14T10:30:00Z")}
	for range 3000 { // about 3 MB
		lines = append(lines, filler)
	}
	c, ok := sessions{}.Context(transcript(t, lines...), "")
	if !ok || c.Tokens != 50002 {
		t.Errorf("behind 3 MB: %+v %v", c, ok)
	}
	for range 15000 { // past the limit
		lines = append(lines, filler)
	}
	if c, ok := (sessions{}).Context(transcript(t, lines...), ""); ok {
		t.Errorf("past the limit: %+v", c)
	}
}

// A reply cut by a chunk's edge is read whole, never taken for an earlier
// one.
func TestContextAcrossAChunkEdge(t *testing.T) {
	long := strings.Replace(reply("claude-opus-5-5", 1, 0, 70000, 1, false, "2026-10-14T10:30:00Z"), `"type":"assistant"`, `"pad":"`+strings.Repeat("p", 4000)+`","type":"assistant"`, 1)
	// After the reply, lines that end 2000 bytes short of a chunk, so the
	// edge falls inside the reply.
	var after []string
	size := 0
	for size < contextChunk-2000-1100 {
		l := `{"type":"attachment","attachment":"` + strings.Repeat("x", 1000) + `"}`
		after = append(after, l)
		size += len(l) + 1
	}
	pad := contextChunk - 2000 - size - 1 - len(`{"type":"attachment","attachment":""}`)
	after = append(after, `{"type":"attachment","attachment":"`+strings.Repeat("y", pad)+`"}`)
	lines := append([]string{reply("claude-opus-5-5", 1, 0, 1, 1, false, "2026-10-14T10:00:00Z"), long}, after...)
	path := transcript(t, lines...)
	st, _ := os.Stat(path)
	if edge := st.Size() - contextChunk; edge <= int64(len(lines[0])+1) || edge >= int64(len(lines[0])+1+len(long)) {
		t.Fatalf("the edge at %d isn't inside the reply", edge)
	}
	if c, ok := (sessions{}).Context(path, ""); !ok || c.Tokens != 70002 {
		t.Errorf("a reply across the edge: %+v %v", c, ok)
	}
}

func TestContextPercent(t *testing.T) {
	c, _ := sessions{}.Context(transcript(t, reply("claude-haiku-4-5", 0, 0, 170000, 0, false, "2026-10-14T10:30:00Z")), "")
	if c.Percent() != 85 {
		t.Errorf("percent %d", c.Percent())
	}
	c.Window = 0
	if c.Percent() != -1 {
		t.Error("no window, no percent")
	}
}
