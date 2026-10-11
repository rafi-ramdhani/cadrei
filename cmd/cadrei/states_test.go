//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// fixtures is the transcript fixtures' folder, found before a test moves
// into its sandbox.
var fixtures, _ = filepath.Abs("testdata/transcripts")

// teamOf starts the dev team (engineer, reviewer) of a cadrei "work" and
// returns the cadrei's folder.
func teamOf(t *testing.T) string {
	t.Helper()
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	must(t, "up", "dev")
	return home + "/.cadrei/work"
}

// feed runs cadrei hook state for a member with one hook payload.
func feed(t *testing.T, c, member, payload string) {
	t.Helper()
	t.Setenv("CADREI_HOME", c)
	t.Setenv("CADREI_MEMBER", member)
	callIn(payload, "hook", "state")
	t.Setenv("CADREI_HOME", "")
	os.Unsetenv("CADREI_MEMBER")
}

// row is the engineer's row of cadrei ls.
func row(t *testing.T, role string) string {
	t.Helper()
	for _, l := range strings.Split(must(t, "ls"), "\n") {
		if strings.Contains(l, " "+role+" ") {
			return l
		}
	}
	return ""
}

// Acceptance 28, 29 and 33: what a member is doing, as its hooks report it.
func TestLsShowsWhatMembersDo(t *testing.T) {
	c := teamOf(t)
	if r := row(t, "engineer"); r != "    . engineer   starting" {
		t.Errorf("before any hook: %q", r)
	}
	// <now> is the minute of the hook, which a slow run can see roll over.
	for _, step := range []struct{ payload, want string }{
		{`{"hook_event_name":"UserPromptSubmit"}`, "    * engineer   working just now"},
		{`{"hook_event_name":"PreToolUse","tool_name":"Read"}`, "    * engineer   working just now"},
		{`{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`, "    ! engineer   waiting on you · approve it in its window"},
		{`{"hook_event_name":"Stop"}`, "    o engineer   idle since <now>"},
		{`{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion"}`, "    ! engineer   waiting on you · it asked you something in its window"},
		{`{"hook_event_name":"StopFailure","error":"rate_limit"}`, "    o engineer   idle since <now> · its last turn hit an error"},
		{`{"hook_event_name":"UserPromptSubmit"}`, "    * engineer   working just now"},
		{`{"hook_event_name":"Notification","notification_type":"idle_prompt"}`, "    o engineer   idle since <now>"},
	} {
		before := time.Now().Format("15:04")
		feed(t, c, "work-dev-engineer", step.payload)
		r := row(t, "engineer")
		after := time.Now().Format("15:04")
		if r != strings.ReplaceAll(step.want, "<now>", before) && r != strings.ReplaceAll(step.want, "<now>", after) {
			t.Errorf("after %s: %q, want %q", step.payload, r, step.want)
		}
	}
	// Styled, with the Unicode marks.
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`)
	t.Setenv("FORCE_COLOR", "1")
	out := must(t, "ls")
	t.Setenv("FORCE_COLOR", "")
	if !strings.Contains(out, "\x1b[33m!\x1b[0m engineer   waiting on you") || !strings.Contains(out, "◌\x1b[0m reviewer   starting") {
		t.Errorf("styled:\n%q", out)
	}
	// The snapshot gives its place and command ready-made.
	t.Setenv("CADREI_HOME", c)
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	if s := snapshotText(t, "resume"); !strings.Contains(s, "- Waiting on the user: the engineer in dev, for an approval, opened with cadrei attach dev\n") {
		t.Errorf("snapshot:\n%s", s)
	}
	t.Setenv("CADREI_ORCHESTRATOR", "")
	t.Setenv("CADREI_HOME", "")
	// A working member whose transcript hasn't changed for 12 minutes.
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(transcript, []byte("{}\n"), 0o600)
	old := time.Now().Add(-12 * time.Minute)
	os.Chtimes(transcript, old, old)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit","transcript_path":"`+transcript+`"}`)
	if r := row(t, "engineer"); r != "    * engineer   working just now · no activity for 12m" {
		t.Errorf("quiet: %q", r)
	}
	// A state for another conversation, and no state at all: unknown.
	state := c + "/.claude/build/state/work-dev-engineer.json"
	session.WriteState(state, &session.State{State: session.Working, Since: time.Now(), Session: "00000000-0000-4000-8000-00000000dead"})
	if r := row(t, "engineer"); r != "    ? engineer   running · can't tell what it's doing" {
		t.Errorf("another session's state: %q", r)
	}
	os.Remove(state)
	if r := row(t, "engineer"); r != "    ? engineer   running · can't tell what it's doing" {
		t.Errorf("no state: %q", r)
	}
}

// placeTranscript puts a fixture where Claude Code keeps a member's
// conversation, and returns its path.
func placeTranscript(t *testing.T, c, member, fixture string, edit func([]byte) []byte) string {
	t.Helper()
	rec := session.ReadRecord(c + "/.claude/build/sessions/" + member + ".json")
	if rec == nil {
		t.Fatalf("no record for %s", member)
	}
	dir := filepath.Join(os.Getenv("HOME"), ".claude", "projects", regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(rec.Dir, "-"))
	os.MkdirAll(dir, 0o755)
	raw, err := os.ReadFile(filepath.Join(fixtures, fixture))
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		raw = edit(raw)
	}
	path := filepath.Join(dir, rec.ID+".jsonl")
	os.WriteFile(path, raw, 0o600)
	return path
}

// Acceptance 38 to 41: a member stopped by safeguards, in ls, its JSON,
// the snapshot and the start check.
func TestSafeguardStopsShow(t *testing.T) {
	c := teamOf(t)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	// A stop seconds ago is the runtime's to retry: not yet in ls.
	recent := placeTranscript(t, c, "work-dev-engineer", "safeguard-recent.jsonl", func(b []byte) []byte {
		return []byte(strings.ReplaceAll(string(b), "2026-01-01T10:00:05.100Z", time.Now().UTC().Format(time.RFC3339Nano)))
	})
	if r := row(t, "engineer"); !strings.Contains(r, "working") {
		t.Errorf("a recent stop: %q", r)
	}
	os.Remove(recent)
	placeTranscript(t, c, "work-dev-engineer", "safeguard-stuck.jsonl", nil)
	for _, payload := range []string{`{"hook_event_name":"Stop"}`, `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`} {
		feed(t, c, "work-dev-engineer", payload)
		out := must(t, "ls")
		if !strings.Contains(out, "\n    x engineer   stopped by safeguards\n                 Pick the model again in its window, or start it fresh.\n") {
			t.Errorf("ls after %s:\n%s", payload, out)
		}
	}
	t.Setenv("FORCE_COLOR", "1")
	if out := must(t, "ls"); !strings.Contains(out, "\x1b[31m✗\x1b[0m engineer   stopped by safeguards") {
		t.Errorf("styled:\n%q", out)
	}
	t.Setenv("FORCE_COLOR", "")
	var st cadreiStatus
	json.Unmarshal([]byte(must(t, "ls", "--json")), &st)
	for _, m := range st.Running[0].Members {
		if m.Role == "engineer" && (m.State != "stopped_by_safeguards" || m.Since == nil || !m.Since.Equal(time.Date(2026, 1, 1, 10, 0, 5, 100e6, time.UTC))) {
			t.Errorf("ls --json: %+v", m)
		}
	}
	t.Setenv("CADREI_HOME", c)
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	if s := snapshotText(t, "resume"); !strings.Contains(s, "- Stopped by safeguards: the engineer in dev (work-dev-engineer), since Jan 1, opened with cadrei attach dev\n") {
		t.Errorf("snapshot:\n%s", s)
	}
	t.Setenv("CADREI_ORCHESTRATOR", "")
	t.Setenv("CADREI_HOME", "")
	// Resuming it says so, after the start lines, and still exits 0.
	must(t, "stop", "dev/engineer")
	code, out, _ := call("up", "dev/engineer")
	block := "! Claude's safeguards stopped the engineer's last reply, so it won't answer.\n  Open its window and pick the model again: cadrei attach dev\n  Or start it fresh: cadrei stop dev/engineer --fresh\n  then cadrei up dev/engineer\n"
	if code != 0 || !strings.HasSuffix(out, block) {
		t.Errorf("up: %d %q", code, out)
	}
	// The start check doesn't wait: a stop seconds ago counts.
	must(t, "stop", "dev/engineer")
	placeTranscript(t, c, "work-dev-engineer", "safeguard-recent.jsonl", func(b []byte) []byte {
		return []byte(strings.ReplaceAll(string(b), "2026-01-01T10:00:05.100Z", time.Now().UTC().Format(time.RFC3339Nano)))
	})
	if _, out, _ := call("up", "dev/engineer"); !strings.HasSuffix(out, block) {
		t.Errorf("up with a recent stop: %q", out)
	}
	// A fresh chat has no transcript, and no block.
	must(t, "stop", "dev/engineer", "--fresh")
	if _, out, _ := call("up", "dev/engineer"); strings.Contains(out, "safeguards") {
		t.Errorf("fresh up: %q", out)
	}
}

// Audit qual-tests-15, acceptance 38 and 40: a safeguard stop wins over a
// working state, over the no-activity note (a transcript last written 12
// minutes ago), and over an unknown state once the 60-second wait is over;
// a notice seconds old leaves an unknown member unknown.
func TestASafeguardStopWinsOverEveryState(t *testing.T) {
	c := teamOf(t)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	stuck := placeTranscript(t, c, "work-dev-engineer", "safeguard-stuck.jsonl", nil)
	old := time.Now().Add(-12 * time.Minute)
	os.Chtimes(stuck, old, old)
	r := row(t, "engineer")
	if !strings.Contains(r, "stopped by safeguards") || strings.Contains(r, "working") || strings.Contains(r, "no activity") {
		t.Errorf("working, quiet: %q", r)
	}
	var st cadreiStatus
	json.Unmarshal([]byte(must(t, "ls", "--json")), &st)
	for _, m := range st.Running[0].Members {
		if m.Role == "engineer" && (m.State != "stopped_by_safeguards" || m.NoActivitySince != nil) {
			t.Errorf("ls --json: %+v", m)
		}
	}
	// Unknown (no state file), with a notice seconds old: still unknown;
	// once it's 61 seconds old, stopped.
	state := c + "/.claude/build/state/work-dev-engineer.json"
	notice := func(ago time.Duration) {
		os.Remove(state)
		os.Remove(c + "/.claude/build/state/work-dev-engineer.safeguard")
		placeTranscript(t, c, "work-dev-engineer", "safeguard-recent.jsonl", func(b []byte) []byte {
			return []byte(strings.ReplaceAll(string(b), "2026-01-01T10:00:05.100Z", time.Now().Add(-ago).UTC().Format(time.RFC3339Nano)))
		})
	}
	notice(30 * time.Second)
	if r := row(t, "engineer"); strings.Contains(r, "safeguards") || !strings.Contains(r, "?") {
		t.Errorf("unknown, a notice 30 s old: %q", r)
	}
	notice(61 * time.Second)
	if r := row(t, "engineer"); !strings.Contains(r, "stopped by safeguards") {
		t.Errorf("unknown, a notice 61 s old: %q", r)
	}
}

// Acceptance 87 (88 and 89 need real Claude Code): a member that went
// idle from a Stop after the stop record is stopped by safeguards at once,
// with no 60-second wait; while it's still working the wait holds.
func TestASafeguardStopThenIdleShowsAtOnce(t *testing.T) {
	for _, fixture := range []string{"safeguard-idle.jsonl", "refusal-no-notice.jsonl"} {
		c := teamOf(t)
		feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
		notice := time.Now().UTC()
		placeTranscript(t, c, "work-dev-engineer", fixture, func(b []byte) []byte {
			s := strings.ReplaceAll(string(b), "NOTICE_TIME", notice.Format(time.RFC3339Nano))
			return []byte(strings.ReplaceAll(s, "2026-01-01T10:00:05.000Z", notice.Format(time.RFC3339Nano)))
		})
		if r := row(t, "engineer"); !strings.Contains(r, "working") {
			t.Errorf("%s, still working: %q", fixture, r)
		}
		rec := session.ReadRecord(c + "/.claude/build/sessions/work-dev-engineer.json")
		raw, _ := os.ReadFile(filepath.Join(fixtures, "safeguard-idle.state.json"))
		state := strings.ReplaceAll(string(raw), "STOP_TIME", notice.Add(2*time.Second).Format(time.RFC3339Nano))
		os.WriteFile(c+"/.claude/build/state/work-dev-engineer.json", []byte(strings.ReplaceAll(state, "SESSION_ID", rec.ID)), 0o600)
		if r := row(t, "engineer"); !strings.Contains(r, "stopped by safeguards") {
			t.Errorf("%s, idle after the stop: %q", fixture, r)
		}
		if out := must(t, "ls", "--json"); !strings.Contains(out, `"state": "stopped_by_safeguards"`) {
			t.Errorf("%s, ls --json:\n%s", fixture, out)
		}
		t.Setenv("CADREI_HOME", c)
		t.Setenv("CADREI_ORCHESTRATOR", "1")
		if out := must(t, "hook", "team-status"); !strings.Contains(out, "**✗ engineer stopped by safeguards**") {
			t.Errorf("%s, team status:\n%s", fixture, out)
		}
		t.Setenv("CADREI_ORCHESTRATOR", "")
		t.Setenv("CADREI_HOME", "")
		// An idle from before the stop doesn't count as the retry's end.
		state = strings.ReplaceAll(string(raw), "STOP_TIME", notice.Add(-time.Minute).Format(time.RFC3339Nano))
		os.WriteFile(c+"/.claude/build/state/work-dev-engineer.json", []byte(strings.ReplaceAll(state, "SESSION_ID", rec.ID)), 0o600)
		os.Remove(c + "/.claude/build/state/work-dev-engineer.safeguard")
		if r := row(t, "engineer"); strings.Contains(r, "safeguards") {
			t.Errorf("%s, idle before the stop: %q", fixture, r)
		}
	}
}

// golden compares got with testdata/<name>, or writes it with
// UPDATE_GOLDEN=1.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(filepath.Dir(fixtures), name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		os.WriteFile(path, []byte(got), 0o644)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil || string(want) != got {
		t.Errorf("%s differs (run with UPDATE_GOLDEN=1 to update):\n--- got\n%s--- want\n%s", name, got, want)
	}
}

// Acceptance 27: the ls screens, plain.
func TestLsScreens(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	golden(t, "ls-empty.txt", must(t, "ls"))
	register(t, home, "work", "blog:\n  repo: you/blog\n  team: dev\n")
	must(t, "up", "dev")
	golden(t, "ls-team.txt", must(t, "ls"))
	must(t, "init", "life")
	golden(t, "ls-all.txt", must(t, "ls", "--all"))
}

// Review 9 (spec review M1): a member of a project with a dot in its name
// keeps its name in ls, so its state shows.
func TestADottedProjectsMembersShowTheirState(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	exec.Command("git", "init", "-q", home+"/src/my.app").Run()
	must(t, "project", "add", "my.app", "--path", home+"/src/my.app", "--no-trust")
	must(t, "up", "dev/engineer", "my.app")
	feed(t, home+"/.cadrei/work", "work-dev-my.app-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	if r := row(t, "engineer"); !strings.Contains(r, "working") {
		t.Errorf("row %q", r)
	}
	if out := must(t, "ls", "--json"); !strings.Contains(out, `"name": "work-dev-my.app-engineer"`) {
		t.Errorf("ls --json:\n%s", out)
	}
}

// Review 11 (L1) and acceptance 104: "working just now", never "working
// for just now".
func TestBriefSaysJustNow(t *testing.T) {
	now := time.Now()
	for st, want := range map[string]string{session.Working: "working just now", session.Waiting: "waiting on you just now"} {
		if got := (memberStatus{State: st, Since: now}).brief(now); got != want {
			t.Errorf("%s: %q", st, got)
		}
	}
	if got := (memberStatus{State: session.Working, Since: now.Add(-4 * time.Minute)}).brief(now); got != "working for 4m" {
		t.Errorf("4m: %q", got)
	}
}
