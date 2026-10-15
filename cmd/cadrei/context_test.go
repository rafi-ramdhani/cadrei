//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// contextReply is a transcript line for a reply of model, whose request
// held n tokens.
func contextReply(model string, n int) string {
	return fmt.Sprintf(`{"type":"assistant","isSidechain":false,"timestamp":"2026-10-14T10:30:00Z","message":{"model":%q,"usage":{"input_tokens":2,"cache_creation_input_tokens":0,"cache_read_input_tokens":%d,"output_tokens":0}}}`, model, n-2)
}

// cadrei ls and the snapshot say how full a member's context is, from its
// transcript; ls --json has it as an optional object; the team status
// line, on each prompt, doesn't.
func TestLsShowsContextUse(t *testing.T) {
	c := teamOf(t)
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	at := func(lines ...string) {
		t.Helper()
		os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	}
	at(contextReply("claude-opus-5-5", 142400))
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop","transcript_path":"`+transcript+`"}`)
	if r := row(t, "engineer"); !strings.HasSuffix(r, " · context 142k") {
		t.Errorf("tokens only: %q", r)
	}
	// A Haiku: a known window, its percent, and the hint past 80%.
	at(contextReply("claude-haiku-4-5-20251001", 170000))
	if r := row(t, "engineer"); !strings.HasSuffix(r, " · context 170k (85%), compact soon") {
		t.Errorf("a full Haiku: %q", r)
	}
	at(contextReply("claude-haiku-4-5-20251001", 100000))
	if r := row(t, "engineer"); !strings.HasSuffix(r, " · context 100k (50%)") {
		t.Errorf("a Haiku at half: %q", r)
	}
	var ls struct {
		Running []struct {
			Members []struct {
				Role    string
				Context *struct {
					Tokens, Window int
					Percent        *int
					At, Compacted  *string
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(must(t, "ls", "--json")), &ls); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range ls.Running {
		for _, m := range s.Members {
			if m.Role != "engineer" {
				continue
			}
			found = true
			if x := m.Context; x == nil || x.Tokens != 100000 || x.Window != 200000 || x.Percent == nil || *x.Percent != 50 || x.At == nil || x.Compacted != nil {
				t.Errorf("ls --json: %+v", x)
			}
		}
	}
	if !found {
		t.Fatal("no engineer in ls --json")
	}
	// Compacted, nothing said since.
	at(contextReply("claude-opus-5-5", 180000), `{"type":"system","subtype":"compact_boundary","isSidechain":false,"timestamp":"2026-10-14T11:00:00Z"}`)
	if r := row(t, "engineer"); !strings.Contains(r, " · compacted ") || strings.Contains(r, "context") {
		t.Errorf("compacted: %q", r)
	}
	if out := must(t, "ls", "--json"); !strings.Contains(out, `"compacted": "2026-10-14T11:00:00Z"`) || strings.Contains(out, `"tokens"`) {
		t.Errorf("ls --json, compacted:\n%s", out)
	}
	// No reply yet: nothing said, and no context object.
	at(`{"type":"user","message":{"content":"hi"}}`)
	if r := row(t, "engineer"); strings.Contains(r, "context") || strings.Contains(r, "compacted") {
		t.Errorf("no reply yet: %q", r)
	}
	if out := must(t, "ls", "--json"); strings.Contains(out, `"context"`) {
		t.Errorf("ls --json, no reply yet:\n%s", out)
	}
	// A member started with a 1M model setting: that window.
	at(contextReply("claude-opus-5-5", 142400))
	record := session.RecordPath(c+"/.claude/build", "work-dev-engineer")
	if rec := session.ReadRecord(record); rec != nil {
		rec.Model = "opus[1m]"
		session.WriteRecord(record, *rec)
	} else {
		t.Fatalf("no record at %s", record)
	}
	if r := row(t, "engineer"); !strings.Contains(r, " · context 142k (14%)") || strings.Contains(r, "compact soon") {
		t.Errorf("a 1M setting: %q", r)
	}
	// Review M1: after /model to Sonnet, the 1M setting says nothing.
	at(contextReply("claude-sonnet-5-5", 182000))
	if r := row(t, "engineer"); !strings.Contains(r, " · context 182k · ") || strings.Contains(r, "%") {
		t.Errorf("a 1M setting, then /model sonnet: %q", r)
	}
	at(contextReply("claude-opus-5-5", 142400))
	// The snapshot has it; the team status line doesn't.
	t.Setenv("CADREI_HOME", c)
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	if s := snapshotText(t, "startup"); !strings.Contains(s, "engineer idle since ") || !strings.Contains(s, " · context 142k (14%)") {
		t.Errorf("snapshot:\n%s", s)
	}
	if out := must(t, "hook", "team-status"); strings.Contains(out, "context") {
		t.Errorf("the team status line:\n%s", out)
	}
}

// Review L2: after /clear, the member's session start hook names the new
// conversation's transcript, so the old one's size doesn't show while
// nothing was said in the new one.
func TestContextAfterClear(t *testing.T) {
	c := teamOf(t)
	old := filepath.Join(t.TempDir(), "old.jsonl")
	os.WriteFile(old, []byte(contextReply("claude-opus-5-5", 182000)+"\n"), 0o600)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop","transcript_path":"`+old+`"}`)
	if r := row(t, "engineer"); !strings.HasSuffix(r, " · context 182k") {
		t.Fatalf("before /clear: %q", r)
	}
	fresh := filepath.Join(t.TempDir(), "new.jsonl")
	t.Setenv("CADREI_HOME", c)
	t.Setenv("CADREI_MEMBER", "work-dev-engineer")
	callIn(`{"hook_event_name":"SessionStart","source":"clear","session_id":"6f1c2b9e-4a5d-4e8f-9b3a-2c7d1e0f5a6b","transcript_path":"`+fresh+`"}`, "hook", "session")
	t.Setenv("CADREI_HOME", "")
	os.Unsetenv("CADREI_MEMBER")
	if r := row(t, "engineer"); !strings.Contains(r, " idle since ") || strings.Contains(r, "context") {
		t.Errorf("after /clear: %q", r)
	}
}

// Review M2: ls --watch leaves context out, its snapshots included, so a
// round that reads a member again never drops it: a new reply in an idle
// member's chat gives no line.
func TestTheStreamLeavesContextOut(t *testing.T) {
	c := teamOf(t)
	fastWatch(t)
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(transcript, []byte(contextReply("claude-opus-5-5", 142400)+"\n"), 0o600)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop","transcript_path":"`+transcript+`"}`)
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	if l := s.expect(t, lineSnapshot)[0]; strings.Contains(l.raw, `"context"`) {
		t.Errorf("the snapshot has context: %s", l.raw)
	}
	time.Sleep(200 * time.Millisecond)
	f, _ := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(contextReply("claude-opus-5-5", 150000) + "\n")
	f.Close()
	s.quiet(t, 500*time.Millisecond)
}
