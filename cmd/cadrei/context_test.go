//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	at(contextReply("claude-haiku-5-5", 170000))
	if r := row(t, "engineer"); !strings.HasSuffix(r, " · context 170k (85%), compact soon") {
		t.Errorf("a full Haiku: %q", r)
	}
	at(contextReply("claude-haiku-5-5", 100000))
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
