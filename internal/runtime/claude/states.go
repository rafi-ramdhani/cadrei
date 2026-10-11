package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// stateEvents are the hook events a member's settings copy runs
// cadrei hook state for, and what each means for its state.
var stateEvents = []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Stop", "StopFailure", "Notification", "SessionEnd"}

// StateHook is the command those hooks run.
func StateHook(binary string) string { return hookCommand(binary, "state") }

// StateEvent reads a hook's JSON input. Waiting comes from
// PermissionRequest, not Notification: a question and an approval give the
// same notification, 6 seconds later, and only the request names the tool.
func (sessions) StateEvent(input io.Reader) runtime.StateEvent {
	var in struct {
		Event      string `json:"hook_event_name"`
		Session    string `json:"session_id"`
		Transcript string `json:"transcript_path"`
		Tool       string `json:"tool_name"`
		Type       string `json:"notification_type"`
	}
	if json.NewDecoder(io.LimitReader(input, 1<<20)).Decode(&in) != nil {
		return runtime.StateEvent{}
	}
	ev := runtime.StateEvent{Session: in.Session, Transcript: in.Transcript}
	if !uuidRule.MatchString(ev.Session) {
		ev.Session = ""
	}
	if !filepath.IsAbs(ev.Transcript) {
		ev.Transcript = ""
	}
	switch in.Event {
	case "UserPromptSubmit":
		ev.Name = runtime.EvPrompt
	case "PreToolUse", "PostToolUse":
		ev.Name = runtime.EvToolUse
	case "PermissionRequest":
		ev.Name = runtime.EvPermission
		ev.Question = in.Tool == "AskUserQuestion"
	case "Stop":
		ev.Name = runtime.EvStop
	case "StopFailure":
		ev.Name = runtime.EvStopFailure
	case "SessionStart":
		ev.Name = runtime.EvStart
	case "Notification":
		if in.Type == "idle_prompt" {
			ev.Name = runtime.EvIdleReminder
		}
	case "SessionEnd":
		ev.Name = runtime.EvEnd
	}
	return ev
}

// TranscriptFile is where Claude Code keeps the conversation for dir.
func (sessions) TranscriptFile(id, dir string) string {
	if !uuidRule.MatchString(id) || dir == "" {
		return ""
	}
	for _, d := range []string{dir, paths.Real(dir)} {
		f := filepath.Join(ConfigDir(), "projects", projectFolder(d), id+".jsonl")
		if st, err := os.Lstat(f); err == nil && st.Mode().IsRegular() {
			return f
		}
	}
	return ""
}

// tailSize is how much of a transcript the safeguard check reads.
const tailSize = 256 << 10

// readyMarks are what Claude Code's footer shows under an empty prompt,
// on 2.1.268 and later: the shortcuts hint, or in its place a permission
// mode or the voice hint (audit perf-start-4: with acceptEdits or plan,
// the shortcuts hint never shows).
var readyMarks = []string{"? for shortcuts", "accept edits on", "plan mode on", "auto mode on", "bypass permissions on", "to speak"}

// Ready: one of Claude Code's footer hints is on the screen.
func (sessions) Ready(screen string) bool {
	for _, m := range readyMarks {
		if strings.Contains(screen, m) {
			return true
		}
	}
	return false
}

// waitMarks are what Claude Code's screens before its prompt show, which
// wait on the user: the first-run text style and login screens (seen on
// 2.1.296), and the folder trust screen, old and new.
var waitMarks = []string{"Choose the text style", "Select login method", "Do you trust the files in this folder", "Is this a project you created or one you trust", "Yes, I trust this folder"}

// guardLimit is the most input the guard reads: more than it can check
// is a command it can't let run.
const guardLimit = 8 << 20

// ShellCommand: Claude Code's PreToolUse input. The Bash tool's command is
// the command line; any other tool runs none. Input that isn't Claude
// Code's, is cut short, or has a Bash call without its command is an error.
func (sessions) ShellCommand(input io.Reader) (string, bool, error) {
	raw, err := io.ReadAll(io.LimitReader(input, guardLimit+1))
	if err != nil {
		return "", false, err
	}
	if len(raw) > guardLimit {
		return "", false, errors.New("the tool call is too long to check")
	}
	var in struct {
		Tool  *string `json:"tool_name"`
		Input struct {
			Command *string `json:"command"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", false, err
	}
	switch {
	case in.Tool == nil:
		return "", false, errors.New("the tool call names no tool")
	case *in.Tool != "Bash":
		return "", false, nil
	case in.Input.Command == nil:
		return "", false, errors.New("the Bash call has no command")
	}
	return *in.Input.Command, true, nil
}

// Sent: Claude Code's PostToolUse input for its SendMessage tool. A
// subagent's call carries agent_id. Any other tool, or input that isn't
// Claude Code's, is not one sent message.
func (sessions) Sent(input io.Reader) (runtime.Sent, bool) {
	var in struct {
		Tool  string  `json:"tool_name"`
		Agent *string `json:"agent_id"`
		Input struct {
			To     *string `json:"to"`
			Notify bool    `json:"notify_when_idle"`
		} `json:"tool_input"`
	}
	if json.NewDecoder(io.LimitReader(input, 1<<20)).Decode(&in) != nil || in.Tool != "SendMessage" || in.Input.To == nil {
		return runtime.Sent{}, false
	}
	return runtime.Sent{To: *in.Input.To, Notify: in.Input.Notify, Subagent: in.Agent != nil && *in.Agent != ""}, true
}

// SentContext: a PostToolUse answer, text Claude Code adds to the
// orchestrator's context, with no decision of any kind.
func (sessions) SentContext(text string) []byte {
	type specific struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	}
	b, _ := json.Marshal(struct {
		HookSpecificOutput specific `json:"hookSpecificOutput"`
	}{specific{"PostToolUse", text}})
	return append(b, '\n')
}

// NotifyHook is the command the orchestrator's notify hook runs after each
// SendMessage call (cadrei hook notify).
func NotifyHook(binary string) string { return hookCommand(binary, "notify") }

// GuardHook is the command a member's guard runs before each Bash call.
// Unlike the other hooks it fails closed: when the program isn't there,
// or ends with anything but 0, the call is blocked (Claude Code blocks
// on exit 2, and shows what the hook wrote on stderr).
func GuardHook(binary string) string {
	q := quote(binary)
	return "if test -x " + q + "; then " + q + " hook guard || exit 2; else echo 'cadrei, which checks commands for team members, is missing, so this one is blocked.' >&2; exit 2; fi"
}

// Waiting: one of Claude Code's screens before its prompt is on the screen:
// a line that starts with a mark, after the frame, pointer and number an
// option or a heading has. A mark inside other text (an error that quotes
// it, say) isn't one.
func (sessions) Waiting(screen string) bool {
	for _, line := range strings.Split(screen, "\n") {
		line = strings.TrimLeft(line, " \t│┃|╭╰❯›>•·*0123456789.)")
		// The new trust screen's heading starts with words of its own.
		line = strings.TrimPrefix(line, "Quick safety check: ")
		for _, m := range waitMarks {
			if strings.HasPrefix(line, m) {
				return true
			}
		}
	}
	return false
}

// SafeguardStop looks at the end of a transcript for Claude's safeguards
// stopping a reply: a system record, subtype informational, whose content
// says "safeguards stopped the response" (never the model's name, which
// changes), with no assistant record after it that ended any way but a
// refusal. A last assistant record that ended in a refusal, with no
// normal reply after it, counts too, notice or not. It reads only the last
// 256 KB, drops the first partial line and skips lines that aren't JSON.
func (sessions) SafeguardStop(transcript string) (time.Time, bool) {
	buf, ok := tail(transcript)
	if !ok {
		return time.Time{}, false
	}
	var notice, refusal time.Time
	noticed, refused := false, false
	for _, line := range bytes.Split(buf, []byte("\n")) {
		var r struct {
			Type      string          `json:"type"`
			Subtype   string          `json:"subtype"`
			Content   json.RawMessage `json:"content"`
			Timestamp string          `json:"timestamp"`
			Message   struct {
				StopReason string `json:"stop_reason"`
			} `json:"message"`
		}
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &r) != nil {
			continue
		}
		at, _ := time.Parse(time.RFC3339Nano, r.Timestamp)
		switch {
		case r.Type == "system" && r.Subtype == "informational" && safeguardText(r.Content):
			noticed, notice = true, at
		case r.Type == "assistant" && r.Message.StopReason == "refusal":
			refused, refusal = true, at
		case r.Type == "assistant":
			noticed, refused = false, false
		}
	}
	switch {
	case noticed:
		return notice, true
	case refused:
		return refusal, true
	}
	return time.Time{}, false
}

// safeguardText reports whether a record's content says the safeguards
// stopped the response.
func safeguardText(raw json.RawMessage) bool {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		s = string(raw)
	}
	return strings.Contains(strings.ToLower(s), "safeguards stopped the response")
}

// UnansweredPrompt reads the end of a transcript: its last user record
// (not one Claude Code adds itself) says exactly text, and no assistant
// record follows it.
func (sessions) UnansweredPrompt(transcript, text string) bool {
	buf, ok := tail(transcript)
	if !ok {
		return false
	}
	unanswered := false
	for _, line := range bytes.Split(buf, []byte("\n")) {
		var r struct {
			Type    string `json:"type"`
			IsMeta  bool   `json:"isMeta"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &r) != nil {
			continue
		}
		switch {
		case r.Type == "user" && !r.IsMeta:
			unanswered = promptText(r.Message.Content) == text
		case r.Type == "assistant":
			unanswered = false
		}
	}
	return unanswered
}

// promptText is a user record's text: a string, or its text blocks.
func promptText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// tail reads the last tailSize bytes of a transcript, from its first
// whole line.
func tail(transcript string) ([]byte, bool) {
	f, err := os.Open(transcript)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, false
	}
	offset := st.Size() - tailSize
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, st.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		return nil, false
	}
	if offset > 0 {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			return nil, false
		}
		buf = buf[i+1:]
	}
	return buf, true
}
