package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// fixtures are the synthetic transcripts the spec describes (7.7).
const fixtures = "../../../cmd/cadrei/testdata/transcripts"

// Acceptance 37 and 42: the matcher flags a stop with no normal reply
// after it, whatever model the notice names, and reads only the end of a
// long transcript, past a bad last line.
func TestSafeguardStop(t *testing.T) {
	for name, want := range map[string]bool{"safeguard-stuck.jsonl": true, "safeguard-refused-twice.jsonl": true, "partial.jsonl": true, "refusal-no-notice.jsonl": true,
		"safeguard-recovered.jsonl": false, "quoted.jsonl": false} {
		raw, err := os.ReadFile(filepath.Join(fixtures, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, model := range []string{"Test Model's", "Other Model's"} {
			path := filepath.Join(t.TempDir(), name)
			os.WriteFile(path, bytes.ReplaceAll(raw, []byte("Test Model's"), []byte(model)), 0o600)
			at, got := sessions{}.SafeguardStop(path)
			if got != want {
				t.Errorf("%s with %s: stopped %v, want %v", name, model, got, want)
			}
			if got && at.Format("15:04:05") != "10:00:05" {
				t.Errorf("%s: stopped at %v", name, at)
			}
		}
	}
	// A stop further back than the last 256 KB is not read.
	raw, _ := os.ReadFile(filepath.Join(fixtures, "safeguard-stuck.jsonl"))
	pad := strings.Repeat(`{"type":"progress","data":"`+strings.Repeat("x", 400)+`"}`+"\n", 700)
	path := filepath.Join(t.TempDir(), "old-stop.jsonl")
	os.WriteFile(path, append(raw, pad...), 0o600)
	if _, got := (sessions{}).SafeguardStop(path); got {
		t.Error("a stop before the last 256 KB was read")
	}
	if _, got := (sessions{}).SafeguardStop(filepath.Join(t.TempDir(), "none.jsonl")); got {
		t.Error("a missing transcript was flagged")
	}
}

func TestStateEvent(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	for in, want := range map[string]runtime.StateEvent{
		`{"hook_event_name":"UserPromptSubmit","session_id":"` + id + `","transcript_path":"/t/x.jsonl"}`: {Name: runtime.EvPrompt, Session: id, Transcript: "/t/x.jsonl"},
		`{"hook_event_name":"PreToolUse","tool_name":"Bash"}`:                                             {Name: runtime.EvToolUse},
		`{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`:                                      {Name: runtime.EvPermission, Request: &runtime.Request{Tool: "Bash"}},
		`{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion"}`:                           {Name: runtime.EvPermission, Question: true, Request: &runtime.Request{Tool: "AskUserQuestion", Multi: true}},
		`{"hook_event_name":"Notification","notification_type":"permission_prompt"}`:                      {Name: runtime.EvPromptShown},
		`{"hook_event_name":"Notification","notification_type":"agent_completed"}`:                        {},
		`{"hook_event_name":"PostToolUse","tool_name":"Bash"}`:                                            {Name: runtime.EvToolDone},
		`{"hook_event_name":"PostToolUseFailure","tool_name":"Bash"}`:                                     {Name: runtime.EvToolDone},
		`{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Ship?","options":[{"label":"Yes"},{"label":"No"}]}]}}`: {Name: runtime.EvToolUse, Question: true,
			Request: &runtime.Request{Tool: "AskUserQuestion", Question: "Ship?", Options: []string{"Yes", "No"}}},
		`{"hook_event_name":"Notification","notification_type":"idle_prompt"}`:                   {Name: runtime.EvIdleReminder},
		`{"hook_event_name":"StopFailure","error":"rate_limit"}`:                                 {Name: runtime.EvStopFailure},
		`{"hook_event_name":"SessionEnd","session_id":"not-an-id","transcript_path":"relative"}`: {Name: runtime.EvEnd},
		`not json`: {},
	} {
		got := (sessions{}).StateEvent(strings.NewReader(in))
		got.Hook, got.Tool, got.ToolID, got.Notice, got.InputKey = "", "", "", "", "" // TestStateEventForTheTrace, TestAnInputsKey
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v, want %+v", in, got, want)
		}
	}
}

// 0.2.3: what a member's trace notes of an event is one plain word each:
// the hook's name, the tool, the call's id and a notification's type.
// Never what the tool got, and nothing else a hook could slip in. The
// tool and the id are kept as they came, printable and short, for
// matching (an MCP tool's name or an id outside the trace's words still
// matches its call); the trace filters them itself (runtime.TraceWord).
func TestStateEventForTheTrace(t *testing.T) {
	ev := sessions{}.StateEvent(strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"toolu_01AbC","tool_input":{"command":"echo TEST_TOKEN_PLACEHOLDER"}}`))
	if ev.Hook != "PreToolUse" || ev.Tool != "Bash" || ev.ToolID != "toolu_01AbC" || ev.Notice != "" {
		t.Errorf("tool event: %+v", ev)
	}
	ev = sessions{}.StateEvent(strings.NewReader(`{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission"}`))
	if ev.Hook != "Notification" || ev.Notice != "permission_prompt" || ev.Tool != "" {
		t.Errorf("notification: %+v", ev)
	}
	ev = sessions{}.StateEvent(strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"mcp__my.server__upload","tool_use_id":"id with spaces","notification_type":"` + strings.Repeat("x", 65) + `"}`))
	if ev.Tool != "mcp__my.server__upload" || ev.ToolID != "id with spaces" || ev.Notice != "" {
		t.Errorf("for matching: %+v", ev)
	}
	if runtime.TraceWord(ev.Tool) != "" || runtime.TraceWord(ev.ToolID) != "" {
		t.Errorf("the trace would keep %q %q", runtime.TraceWord(ev.Tool), runtime.TraceWord(ev.ToolID))
	}
	ev = sessions{}.StateEvent(strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash\u001b[2J","tool_use_id":"` + strings.Repeat("x", 257) + `"}`))
	if ev.Tool != "" || ev.ToolID != "" {
		t.Errorf("unprintable or long kept: %+v", ev)
	}
}

// 0.2.2 (6.4): a permission request records what Claude Code passed the
// hook: the command, path or URL, or the first question and its options
// (spike S5's input), each one printable line, cut to its limit.
func TestARequestIsWhatClaudeCodePassed(t *testing.T) {
	ev := func(tool, input string) *runtime.Request {
		t.Helper()
		in := `{"hook_event_name":"PermissionRequest","tool_name":"` + tool + `","tool_input":` + input + `}`
		return (sessions{}).StateEvent(strings.NewReader(in)).Request
	}
	for _, tc := range []struct {
		tool, input string
		want        runtime.Request
	}{
		{"Bash", `{"command":"gh pr merge 42 --squash","description":"Merge it"}`, runtime.Request{Tool: "Bash", Summary: "gh pr merge 42 --squash"}},
		{"Bash", `{"command":"echo a\nrm -rf x\u200b\u001b[2J"}`, runtime.Request{Tool: "Bash", Summary: "echo a⏎rm -rf x??[2J"}},
		{"Edit", `{"file_path":"/w/a.go","old_string":"x","new_string":"y"}`, runtime.Request{Tool: "Edit", Summary: "/w/a.go"}},
		{"Write", `{"file_path":"/w/b.md","content":"hi"}`, runtime.Request{Tool: "Write", Summary: "/w/b.md"}},
		{"WebFetch", `{"url":"https://example.com/x","prompt":"read"}`, runtime.Request{Tool: "WebFetch", Summary: "https://example.com/x"}},
		{"mcp__files__upload", `{"path": "/w/resume.pdf"}`, runtime.Request{Tool: "mcp__files__upload", Summary: `mcp__files__upload {"path":"/w/resume.pdf"}`}},
		{"AskUserQuestion", `{"questions": [{"question": "Upload the test file to the signing site?", "header": "Upload", "options": [{"label": "Yes", "description": "Upload the test file to the signing site."}, {"label": "No", "description": "Do not upload the test file."}], "multiSelect": false}]}`,
			runtime.Request{Tool: "AskUserQuestion", Question: "Upload the test file to the signing site?", Options: []string{"Yes", "No"}}},
		// W7 (PR B): a multi-select question, or several questions, keep
		// each question as a part, answered from the orchestrator.
		{"AskUserQuestion", `{"questions": [{"question": "Which?", "options": [{"label": "A"}, {"label": "B"}], "multiSelect": true}]}`,
			runtime.Request{Tool: "AskUserQuestion", Question: "Which?", Options: []string{"A", "B"}, Parts: []runtime.Part{{Question: "Which?", Options: []string{"A", "B"}, Multi: true}}}},
		{"AskUserQuestion", `{"questions": [{"question": "One?", "options": [{"label": "A"}]}, {"question": "Two?", "options": [{"label": "B"}]}]}`,
			runtime.Request{Tool: "AskUserQuestion", Question: "One?", Options: []string{"A"}, Parts: []runtime.Part{{Question: "One?", Options: []string{"A"}}, {Question: "Two?", Options: []string{"B"}}}}},
		// More questions than it keeps, or a question with no options, is
		// left to the window.
		{"AskUserQuestion", `{"questions": [{"question": "1?", "options": [{"label": "A"}]}, {"question": "2?", "options": [{"label": "A"}]}, {"question": "3?", "options": [{"label": "A"}]}, {"question": "4?", "options": [{"label": "A"}]}, {"question": "5?", "options": [{"label": "A"}]}]}`,
			runtime.Request{Tool: "AskUserQuestion", Question: "1?", Options: []string{"A"}, Multi: true, Parts: []runtime.Part{{Question: "1?", Options: []string{"A"}}, {Question: "2?", Options: []string{"A"}}, {Question: "3?", Options: []string{"A"}}, {Question: "4?", Options: []string{"A"}}}}},
		{"AskUserQuestion", `{"questions": [{"question": "One?", "options": [{"label": "A"}]}, {"question": "Two?", "options": []}]}`,
			runtime.Request{Tool: "AskUserQuestion", Question: "One?", Options: []string{"A"}, Multi: true, Parts: []runtime.Part{{Question: "One?", Options: []string{"A"}}, {Question: "Two?"}}}},
		{"AskUserQuestion", `{"questions": [{"question": "Pick", "options": [{"label": "1"}, {"label": "2"}, {"label": "3"}, {"label": "4"}, {"label": "5"}, {"label": "6"}, {"label": "7"}]}]}`,
			runtime.Request{Tool: "AskUserQuestion", Question: "Pick", Options: []string{"1", "2", "3", "4", "5", "6"}, Multi: true}},
	} {
		if got := ev(tc.tool, tc.input); got == nil || !reflect.DeepEqual(*got, tc.want) {
			t.Errorf("%s %s: %+v", tc.tool, tc.input, got)
		}
	}
	cut := ev("Bash", `{"command":"`+strings.Repeat("a", 400)+`\nb"}`)
	if long := cut.Summary; len([]rune(long)) != runtime.SummaryLimit+1 || !strings.HasSuffix(long, "…") {
		t.Errorf("a long command: %d %q", len([]rune(long)), long[len(long)-10:])
	}
	// W3 (PR B): one cut short keeps its whole text too, cut only past
	// FullLimit; one that isn't cut has none.
	if cut.Full != strings.Repeat("a", 400)+"⏎b" {
		t.Errorf("the whole command: %q", cut.Full)
	}
	if huge := ev("Bash", `{"command":"`+strings.Repeat("a", runtime.FullLimit+5)+`"}`).Full; len([]rune(huge)) != runtime.FullLimit+1 || !strings.HasSuffix(huge, "…") {
		t.Errorf("a huge command: %d", len([]rune(huge)))
	}
	label := ev("AskUserQuestion", `{"questions": [{"question": "Q", "options": [{"label": "`+strings.Repeat("b", 100)+`"}, {"label": "No"}]}]}`).Options[0]
	if n := len([]rune(label)); n != runtime.OptionLimit+1 {
		t.Errorf("a long label: %d", n)
	}
}

// Audit perf-start-4: the footer's shortcuts hint gives way to a mode or
// the voice hint; each counts as ready, and a screen still starting
// doesn't.
func TestReadyKnowsEveryFooter(t *testing.T) {
	for _, screen := range []string{"> \n  ? for shortcuts", "> \n  ⏵⏵ accept edits on (shift+tab to cycle)", "> \n  ⏸ plan mode on", "> \n  hold space to speak",
		"❯ \n  app | model Haiku 5.5 | effort low\n  ⏸ manual mode on · ← for agents"} {
		if !(sessions{}).Ready(screen) {
			t.Errorf("not ready: %q", screen)
		}
	}
	if (sessions{}).Ready("Welcome to Claude Code\n  Loading...") {
		t.Error("a starting screen is ready")
	}
}

// Review of fix round 5 (S3 regression): the screens before the prompt
// that wait on the user count as going; any other line doesn't, so a
// command that prints and ends isn't taken for one.
func TestWaitingKnowsTheFirstScreens(t *testing.T) {
	for _, screen := range []string{
		" Let's get started.\n Choose the text style that looks best with your terminal\n ❯ ✔ Dark mode",
		" Select login method:\n ❯ 1. Claude account with subscription",
		" Accessing workspace:\n Quick safety check: Is this a project you created or one you trust?\n ❯ 1. Yes, I trust this folder",
		" Do you trust the files in this folder?",
		// Each mark alone (review of fix round 6), the trust screen's
		// question wrapped in a narrow pane.
		" Accessing workspace:\n Quick safety check: Is this a project you created or one you trust?\n (Like your own code)",
		" Accessing workspace:\n Quick safety check:\n Is this a project you\n ❯ 1. Yes, I trust this folder",
		"╭────╮\n│ Do you trust the files in this folder? │",
	} {
		if !(sessions{}).Waiting(screen) {
			t.Errorf("not waiting: %q", screen)
		}
	}
	for _, screen := range []string{"Welcome to Claude Code\n  Loading...", "No conversation found with session ID: x", "Error: something went wrong", "",
		// A mark inside other text isn't the screen.
		"Error: Do you trust the files in this folder? check failed", "> Your last message: Select login method: SSO"} {
		if (sessions{}).Waiting(screen) {
			t.Errorf("waiting: %q", screen)
		}
	}
}

// The guard's hook command fails closed: with the program gone, or one
// that fails or refuses, the call is blocked (Claude Code blocks on 2).
func TestTheGuardsCommandFailsClosed(t *testing.T) {
	dir := t.TempDir()
	for name, script := range map[string]string{"ok": "exit 0", "fails": "exit 1", "refuses": "exit 2"} {
		cmd := exec.Command("/bin/sh", "-c", `printf '%s' "$2" > "$1" && chmod 755 "$1"`, "sh", dir+"/"+name, "#!/bin/sh\n"+script+"\n")
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
	}
	for bin, want := range map[string]int{dir + "/ok": 0, dir + "/fails": 2, dir + "/refuses": 2, dir + "/gone": 2} {
		err := exec.Command("/bin/sh", "-c", GuardHook(bin)).Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		if code != want {
			t.Errorf("%s: exit %d", bin, code)
		}
	}
}

// ShellCall reads Claude Code's PreToolUse input: Bash's command and the
// permission mode, no command for another tool, and an error for anything
// it can't read.
func TestShellCallReadsTheBashCall(t *testing.T) {
	ops := sessions{}
	if c, err := ops.ShellCall(strings.NewReader(`{"tool_name":"Bash","permission_mode":"acceptEdits","tool_input":{"command":"ls -la"}}`)); c != (runtime.ShellCall{Command: "ls -la", Shell: true, Mode: "acceptEdits"}) || err != nil {
		t.Errorf("Bash: %+v %v", c, err)
	}
	if c, err := ops.ShellCall(strings.NewReader(`{"tool_name":"Read","tool_input":{"file_path":"/x"}}`)); c.Shell || err != nil {
		t.Errorf("Read: %+v %v", c, err)
	}
	for _, in := range []string{"", "[]", `{"tool_input":{"command":"ls"}}`, `{"tool_name":"Bash"}`, `{"tool_name":"Bash","tool_input":{"command":3}}`, strings.Repeat(" ", guardLimit+1)} {
		if _, err := ops.ShellCall(strings.NewReader(in)); err == nil {
			t.Errorf("read %.40q", in)
		}
	}
}

// 0.2.2 (6.6): the guard's ask is exactly one object, with only the event
// name, ask and cadrei's text, and never allows.
func TestTheGuardsAskIsOnlyAnAsk(t *testing.T) {
	out := (sessions{}).Ask(`Cadrei: allow once? The engineer on my-app wants to run: gh pr merge 42 --squash`)
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"Cadrei: allow once? The engineer on my-app wants to run: gh pr merge 42 --squash"}}` + "\n"
	if string(out) != want {
		t.Errorf("ask: %s", out)
	}
}

// The orchestrator's settings run cadrei hook notify after each SendMessage
// call, guarded, with a short time limit; with a program that's gone or
// doesn't know the word, the call ends with exit 0 and nothing on stdout.
func TestTheNotifyHookIsTheOrchestratorsAndStaysQuiet(t *testing.T) {
	dir := t.TempDir()
	path, err := (hooks{}).SnapshotSettings(dir, "/opt/cadrei/bin/cadrei", "")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Hooks map[string][]struct {
			Matcher string
			Hooks   []struct {
				Command string
				Timeout int
			}
		}
	}
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	post := s.Hooks["PostToolUse"]
	if len(post) != 1 || post[0].Matcher != "SendMessage" || len(post[0].Hooks) != 1 || post[0].Hooks[0].Command != NotifyHook("/opt/cadrei/bin/cadrei") ||
		post[0].Hooks[0].Timeout != 5 || !strings.HasSuffix(NotifyHook("x"), " hook notify 2>/dev/null; exit 0") {
		t.Errorf("PostToolUse: %+v", post)
	}
	// An older cadrei says it doesn't know the word on stderr and fails.
	old := filepath.Join(dir, "cadrei")
	cmd := exec.Command("/bin/sh", "-c", `printf '%s' "$2" > "$1" && chmod 755 "$1"`, "sh", old, "#!/bin/sh\necho \"cadrei: unknown command 'hook notify'\" >&2\nexit 1\n")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	for _, bin := range []string{old, filepath.Join(dir, "gone")} {
		out, err := exec.Command("/bin/sh", "-c", NotifyHook(bin)).Output()
		if err != nil || len(out) != 0 {
			t.Errorf("%s: %v, stdout %q", bin, err, out)
		}
	}
}

// 0.2.2 (6.6): the orchestrator's settings run the guard before each Bash
// call. It lets the call go on when the program is gone (so an
// uninstalled cadrei doesn't lock the user's chat), passes the guard's
// ask on, and blocks when the guard fails.
func TestTheOrchestratorsGuardFailsOpenOnlyWhenGone(t *testing.T) {
	dir := t.TempDir()
	path, err := (hooks{}).SnapshotSettings(dir, "/opt/cadrei/bin/cadrei", "")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Hooks map[string][]struct {
			Matcher string
			Hooks   []struct {
				Command   string
				Timeout   int
				OnFailure string
			}
		}
	}
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	// A guard that times out blocks the call (review of 0.2.2, H1).
	pre := s.Hooks["PreToolUse"]
	if len(pre) != 1 || pre[0].Matcher != "Bash" || len(pre[0].Hooks) != 1 || pre[0].Hooks[0].Command != OrchestratorGuardHook("/opt/cadrei/bin/cadrei") || pre[0].Hooks[0].Timeout != 10 || pre[0].Hooks[0].OnFailure != "block" {
		t.Errorf("PreToolUse: %+v", pre)
	}
	stub := func(name, script string) string {
		p := filepath.Join(dir, name)
		if err := exec.Command("/bin/sh", "-c", `printf '%s' "$2" > "$1" && chmod 755 "$1"`, "sh", p, script).Run(); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for bin, want := range map[string]struct {
		code int
		out  string
	}{
		filepath.Join(dir, "gone"):                                       {0, ""},
		stub("asks", "#!/bin/sh\necho '{\"ask\":1}'\n"):                  {0, "{\"ask\":1}\n"},
		stub("blocks", "#!/bin/sh\necho 'no' >&2\nexit 2\n"):             {2, ""},
		stub("fails", "#!/bin/sh\necho 'unknown command' >&2\nexit 1\n"): {2, ""},
	} {
		out, err := exec.Command("/bin/sh", "-c", OrchestratorGuardHook(bin)).Output()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
		if code != want.code || string(out) != want.out {
			t.Errorf("%s: exit %d, %q", filepath.Base(bin), code, out)
		}
	}
}

// Sent reads Claude Code's PostToolUse input for SendMessage: its target,
// its notify_when_idle, and a subagent's agent_id.
func TestSentReadsASendMessageCall(t *testing.T) {
	ops := sessions{}
	for in, want := range map[string]runtime.Sent{
		`{"tool_name":"SendMessage","tool_input":{"to":"work-dev-engineer","message":"x"}}`:                 {To: "work-dev-engineer"},
		`{"tool_name":"SendMessage","tool_input":{"to":"work-dev-engineer","notify_when_idle":true}}`:       {To: "work-dev-engineer", Notify: true},
		`{"tool_name":"SendMessage","agent_id":"a1","tool_input":{"to":"work-dev-engineer","message":"x"}}`: {To: "work-dev-engineer", Subagent: true},
	} {
		if got, ok := ops.Sent(strings.NewReader(in)); !ok || got != want {
			t.Errorf("%s: %+v %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "{x", `{"tool_name":"Bash","tool_input":{"command":"ls"}}`, `{"tool_name":"SendMessage","tool_input":{"message":"x"}}`} {
		if _, ok := ops.Sent(strings.NewReader(in)); ok {
			t.Errorf("read %q", in)
		}
	}
	if got := string(ops.SentContext("x")); got != `{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"x"}}`+"\n" {
		t.Errorf("SentContext: %s", got)
	}
}

// 0.2.3: a permission request is matched to its call by a digest of the
// tool's input, which PreToolUse and PermissionRequest both carry: the
// same however the JSON is spaced or its keys ordered, and different for
// another input. The input itself isn't kept.
func TestAnInputsKey(t *testing.T) {
	key := func(hook, input string) string {
		return sessions{}.StateEvent(strings.NewReader(`{"hook_event_name":"` + hook + `","tool_name":"Bash","tool_input":` + input + `}`)).InputKey
	}
	a := key("PreToolUse", `{"command":"make deploy","description":"Deploy"}`)
	if a == "" || len(a) != 32 || strings.Contains(a, "make") {
		t.Fatalf("key %q", a)
	}
	if b := key("PermissionRequest", `{ "description" : "Deploy", "command" : "make deploy" }`); b != a {
		t.Errorf("same input, keys %q %q", a, b)
	}
	if c := key("PermissionRequest", `{"command":"make deploy-prod","description":"Deploy"}`); c == a {
		t.Error("another input, same key")
	}
	if key("PreToolUse", `null`) != "" || key("PostToolUse", `{"command":"x"}`) != "" {
		t.Error("a key without an input, or for an event that doesn't match")
	}
}
