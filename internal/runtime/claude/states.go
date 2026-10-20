package claude

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// stateEvents are the hook events a member's settings copy runs
// cadrei hook state for, and what each means for its state.
var stateEvents = []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionRequest", "Stop", "StopFailure", "Notification", "SessionEnd"}

// callWord is a tool's name or a call's id as the hook got it, for
// matching one call's hooks to each other: any printable text up to
// callWordLimit bytes, or "". The trace keeps only what's one plain word
// of it (runtime.TraceWord).
func callWord(s string) string {
	if len(s) > callWordLimit || !utf8.ValidString(s) || strings.IndexFunc(s, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return ""
	}
	return s
}

const callWordLimit = 256

// StateHook is the command those hooks run.
func StateHook(binary string) string { return hookCommand(binary, "state") }

// StateEvent reads a hook's JSON input. Waiting comes from the hooks that
// say what is asked: a question's PreToolUse, which comes before its
// dialog, and PermissionRequest, which names no tool call (2.1.301), so it
// carries a digest of the tool's input to be matched to its call's start.
// Notification's permission_prompt comes 6 seconds after any prompt, and
// names nothing, so it only says to look at the screen (the catch-all).
func (sessions) StateEvent(input io.Reader) runtime.StateEvent {
	var in struct {
		Event      string          `json:"hook_event_name"`
		Session    string          `json:"session_id"`
		Transcript string          `json:"transcript_path"`
		Tool       string          `json:"tool_name"`
		ToolID     string          `json:"tool_use_id"`
		Input      json.RawMessage `json:"tool_input"`
		Type       string          `json:"notification_type"`
	}
	if json.NewDecoder(io.LimitReader(input, 1<<20)).Decode(&in) != nil {
		return runtime.StateEvent{}
	}
	ev := runtime.StateEvent{Session: in.Session, Transcript: in.Transcript, Hook: runtime.TraceWord(in.Event),
		Tool: callWord(in.Tool), ToolID: callWord(in.ToolID), Notice: runtime.TraceWord(in.Type)}
	if !uuidRule.MatchString(ev.Session) {
		ev.Session = ""
	}
	if !filepath.IsAbs(ev.Transcript) {
		ev.Transcript = ""
	}
	switch in.Event {
	case "UserPromptSubmit":
		ev.Name = runtime.EvPrompt
	case "PreToolUse":
		ev.Name, ev.InputKey = runtime.EvToolUse, inputKey(in.Input)
		if in.Tool == "AskUserQuestion" {
			ev.Question, ev.Request = true, request(in.Tool, in.Input)
		}
	case "PostToolUse", "PostToolUseFailure":
		ev.Name = runtime.EvToolDone
	case "PermissionRequest":
		ev.Name, ev.InputKey = runtime.EvPermission, inputKey(in.Input)
		ev.Question = in.Tool == "AskUserQuestion"
		ev.Request = request(in.Tool, in.Input)
	case "Stop":
		ev.Name = runtime.EvStop
	case "StopFailure":
		ev.Name = runtime.EvStopFailure
	case "SessionStart":
		ev.Name = runtime.EvStart
	case "Notification":
		switch in.Type {
		case "idle_prompt":
			ev.Name = runtime.EvIdleReminder
		case "permission_prompt":
			ev.Name = runtime.EvPromptShown
		}
	case "SessionEnd":
		ev.Name = runtime.EvEnd
	}
	return ev
}

// inputKey is a digest of a tool's input, the same for the same input
// however its JSON is spaced or ordered, or "" for none.
func inputKey(input json.RawMessage) string {
	var v any
	if len(input) == 0 || json.Unmarshal(input, &v) != nil || v == nil {
		return ""
	}
	canon, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:16])
}

// request reads what a permission request asks, from Claude Code's own
// record of the tool call: for Bash, the command; for a file tool, the
// path; for a web fetch, the URL; for AskUserQuestion, the first question
// and its options; for any other tool, its input, short.
func request(tool string, input json.RawMessage) *runtime.Request {
	r := &runtime.Request{Tool: runtime.RequestText(tool, runtime.OptionLimit)}
	if tool == "AskUserQuestion" {
		var in struct {
			Questions []struct {
				Question string `json:"question"`
				Options  []struct {
					Label string `json:"label"`
				} `json:"options"`
				MultiSelect bool `json:"multiSelect"`
			} `json:"questions"`
		}
		if json.Unmarshal(input, &in) != nil || len(in.Questions) == 0 {
			r.Multi = true
			return r
		}
		r.Multi = len(in.Questions) > runtime.MaxParts
		for n, q := range in.Questions {
			if n == runtime.MaxParts {
				break
			}
			p := runtime.Part{Question: runtime.RequestText(q.Question, runtime.SummaryLimit), Multi: q.MultiSelect}
			for i, o := range q.Options {
				if i == runtime.MaxOptions {
					break
				}
				p.Options = append(p.Options, runtime.RequestText(o.Label, runtime.OptionLimit))
			}
			r.Multi = r.Multi || len(q.Options) == 0 || len(q.Options) > runtime.MaxOptions
			r.Parts = append(r.Parts, p)
		}
		r.Question, r.Options = r.Parts[0].Question, r.Parts[0].Options
		// One question with one pick is the question itself.
		if len(r.Parts) == 1 && !r.Parts[0].Multi {
			r.Parts = nil
		}
		return r
	}
	var in map[string]json.RawMessage
	json.Unmarshal(input, &in)
	text := func(key string) string {
		var s string
		json.Unmarshal(in[key], &s)
		return s
	}
	summary := ""
	switch tool {
	case "Bash":
		summary = text("command")
	case "Edit", "MultiEdit", "Write", "Read":
		summary = text("file_path")
	case "NotebookEdit":
		summary = text("notebook_path")
	case "WebFetch":
		summary = text("url")
	case "WebSearch":
		summary = text("query")
	}
	if summary == "" && len(input) > 0 {
		var compact bytes.Buffer
		if json.Compact(&compact, input) == nil {
			summary = compact.String()
		}
		if strings.HasPrefix(tool, "mcp__") {
			summary = tool + " " + summary
		}
	}
	r.Summary = runtime.RequestText(summary, runtime.SummaryLimit)
	if strings.HasSuffix(r.Summary, "…") {
		r.Full = runtime.RequestText(summary, runtime.FullLimit)
	}
	return r
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
// the shortcuts hint never shows; on 2.1.296, default mode can show
// "manual mode on" in its place).
var readyMarks = []string{"? for shortcuts", "accept edits on", "plan mode on", "auto mode on", "manual mode on", "bypass permissions on", "to speak"}

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

// ShellCall: Claude Code's PreToolUse input. The Bash tool's command is
// the command line; any other tool runs none. permission_mode is the
// session's mode. Input that isn't Claude Code's, is cut short, or has a
// Bash call without its command is an error.
func (sessions) ShellCall(input io.Reader) (runtime.ShellCall, error) {
	raw, err := io.ReadAll(io.LimitReader(input, guardLimit+1))
	if err != nil {
		return runtime.ShellCall{}, err
	}
	if len(raw) > guardLimit {
		return runtime.ShellCall{}, errors.New("the tool call is too long to check")
	}
	var in struct {
		Tool  *string `json:"tool_name"`
		Mode  string  `json:"permission_mode"`
		Input struct {
			Command *string `json:"command"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return runtime.ShellCall{}, err
	}
	switch {
	case in.Tool == nil:
		return runtime.ShellCall{}, errors.New("the tool call names no tool")
	case *in.Tool != "Bash":
		return runtime.ShellCall{Mode: in.Mode}, nil
	case in.Input.Command == nil:
		return runtime.ShellCall{}, errors.New("the Bash call has no command")
	}
	return runtime.ShellCall{Command: *in.Input.Command, Shell: true, Mode: in.Mode}, nil
}

// SessionSettings: a Claude Code process runs claude (a native build, or
// node or a shell running the claude script), and --settings names its
// settings file, up to the next flag.
func (sessions) SessionSettings(args string) (string, bool) {
	fields := strings.Fields(args)
	base := func(i int) string {
		if i >= len(fields) {
			return ""
		}
		return filepath.Base(fields[i])
	}
	if base(0) != "claude" && base(1) != "claude" && !strings.Contains(args, "/@anthropic-ai/claude-code/") {
		return "", false
	}
	_, after, found := strings.Cut(args, " --settings ")
	if !found {
		if _, after, found = strings.Cut(args, " --settings="); !found {
			return "", true
		}
	}
	settings, _, _ := strings.Cut(after, " --")
	return strings.TrimSpace(settings), true
}

// InSession: Claude Code sets CLAUDECODE, and CLAUDE_CODE_CHILD_SESSION
// for its tools, hooks and ! commands.
func (sessions) InSession() bool {
	return os.Getenv("CLAUDECODE") != "" || os.Getenv("CLAUDE_CODE_CHILD_SESSION") != ""
}

// Ask: a PreToolUse answer that has Claude Code ask the user, with why as
// the reason its prompt shows. It never allows: the user's Yes does.
func (sessions) Ask(why string) []byte {
	type specific struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	}
	b, _ := json.Marshal(struct {
		HookSpecificOutput specific `json:"hookSpecificOutput"`
	}{specific{"PreToolUse", "ask", why}})
	return append(b, '\n')
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

// OrchestratorGuardHook is the command the orchestrator's guard runs
// before each Bash call. Unlike a member's, it lets the call go on when
// the program is gone, so an uninstalled cadrei doesn't lock the user's
// chat (and with it, cadrei answer is gone too); a run that ends with
// anything but 0 is blocked.
func OrchestratorGuardHook(binary string) string {
	q := quote(binary)
	return "if test -x " + q + "; then " + q + " hook guard || exit 2; else exit 0; fi"
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

// startScreens are Claude Code's start-up screens that wait on the user to
// pick an option, by the line each starts with (after the frame) and what
// each asks, from 2.1.304's own text (not seen live yet), and the trust
// screen's older heading. The servers screen's line names the server, so
// it's kept as the screen shows it (title ""); the API key screen shows
// part of the key, so only its fixed words are.
var startScreens = []struct{ kind, mark, title string }{
	{runtime.StartTrust, "Quick safety check: Is this a project you created or one you trust?", "Is this a project you created or one you trust?"},
	{runtime.StartTrust, "Do you trust the files in this folder?", "Do you trust the files in this folder?"},
	{runtime.StartLogin, "Select login method:", "Select login method"},
	{runtime.StartStyle, "Choose the text style that looks best with your terminal", "Choose the text style that looks best with your terminal"},
	{runtime.StartServer, "New MCP server found in this project:", ""},
	{runtime.StartServer, "new MCP servers found in this project", ""},
	{runtime.StartAPIKey, "Detected a custom API key in your environment", "Detected a custom API key in your environment. Do you want to use this API key?"},
	{runtime.StartBypass, "WARNING: Claude Code running in Bypass Permissions mode", "WARNING: Claude Code running in Bypass Permissions mode"},
	{runtime.StartImports, "Allow external CLAUDE.md file imports?", "Allow external CLAUDE.md file imports?"},
}

// Bounds on a start-up screen: how far up from the bottom its menu's
// pointer may be, how far above the menu its heading, how many options
// cadrei keeps, and how much of its heading.
const (
	menuReach    = 30
	headingReach = 40
	startOptions = 9
	titleLimit   = 1000
)

// StartScreen: one of Claude Code's start-up screens (startScreens), or
// another menu on a screen with no prompt footer under it (StartOther,
// asked in everything above it), read off the menu nearest the bottom.
// The imports screen's title names the files it would import. The
// several-servers screen takes ticks, so only its window can answer it,
// as can a menu cadrei can't read whole, and a screen whose question or
// list of files it can't read whole.
func (s sessions) StartScreen(screen string) (runtime.StartScreen, bool) {
	if s.Ready(screen) {
		return runtime.StartScreen{}, false
	}
	lines := strings.Split(strings.TrimRight(screen, " \n"), "\n")
	top, opts, at, whole := menuOf(lines)
	if len(opts) == 0 {
		return runtime.StartScreen{}, false
	}
	st := runtime.StartScreen{Kind: runtime.StartOther, At: at, Multi: !whole || len(opts) > startOptions}
	known, cut := false, false
	for i := top - 1; i >= 0 && i >= top-headingReach && !known; i-- {
		line := strings.TrimRight(strings.TrimLeft(lines[i], " \t│┃|╭╰•·*0123456789.)"), " │┃|")
		for _, m := range startScreens {
			if strings.HasPrefix(line, m.mark) {
				st.Kind, st.Title, known = m.kind, cmp.Or(m.title, strings.TrimSpace(lines[i])), true
				st.Multi = st.Multi || m.mark == "new MCP servers found in this project"
				if m.kind == runtime.StartImports {
					// A yes approves these files: they're part of what it asks.
					files, whole := importsOf(lines[i+1 : top])
					st.Title += "\nExternal imports: " + strings.Join(files, ", ")
					cut = !whole
				}
				break
			}
		}
	}
	if !known {
		var whole bool
		st.Title, whole = question(lines, top)
		cut = !whole
	}
	title := strings.Trim(st.Title, " │┃|")
	st.Multi = st.Multi || cut || utf8.RuneCountInString(title) > titleLimit
	if st.Title = runtime.RequestText(title, titleLimit); st.Title == "" {
		return runtime.StartScreen{}, false
	}
	for _, o := range opts[:min(len(opts), startOptions)] {
		st.Options = append(st.Options, runtime.RequestText(o, runtime.OptionLimit))
	}
	return st, true
}

// menuPointer is the line of a menu its pointer is at.
var menuPointer = regexp.MustCompile(`^[ │┃]*❯ +\S`)

// menuNumber and menuTick are what may come before a menu option's label:
// its number, and a ✔ that marks the current choice.
var (
	menuNumber = regexp.MustCompile(`^([1-9][0-9]?)\. +`)
	menuTick   = regexp.MustCompile(`^[✔✓] +`)
)

// menuOf reads the menu nearest a screen's bottom: the line its pointer
// (❯) is at, and the lines next to it, up to a blank line or one that
// isn't an option at the same place, each option's label without its
// number and its ✔. It gives the menu's first line, the labels top down
// and the one the pointer is at. whole is false when a scroll arrow is
// on one of its lines or right next to it, or their numbers don't run
// from 1.
func menuOf(lines []string) (top int, opts []string, at int, whole bool) {
	p := -1
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-menuReach; i-- {
		if menuPointer.MatchString(lines[i]) {
			p = i
			break
		}
	}
	if p < 0 {
		return 0, nil, 0, false
	}
	col := strings.Index(lines[p], "❯")
	prefix := lines[p][:col]
	// label is the option a line holds, or ok false for none: after the
	// same frame as the pointer's line, the pointer or a blank in its
	// place, then a few spaces, its number and its ✔.
	numbered := menuNumber.MatchString(strings.TrimLeft(lines[p][col+len("❯"):], " "))
	label := func(line string) (n int, text string, ok bool) {
		if !strings.HasPrefix(line, prefix) {
			return 0, "", false
		}
		rest, pointer := strings.CutPrefix(line[col:], "❯")
		if !pointer {
			if rest, ok = strings.CutPrefix(line[col:], " "); !ok {
				return 0, "", false
			}
		}
		text = strings.TrimLeft(rest, " ")
		if indent := len(rest) - len(text); indent < 1 || indent > 4 || text == "" {
			return 0, "", false
		}
		if m := menuNumber.FindStringSubmatch(text); m != nil {
			n, _ = strconv.Atoi(m[1])
			text = text[len(m[0]):]
		} else if numbered {
			return 0, "", false
		}
		text = menuTick.ReplaceAllString(text, "")
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimRight(text, " │┃"), " ✔"))
		return n, text, text != ""
	}
	top, end := p, p
	for top > 0 {
		if _, _, ok := label(lines[top-1]); !ok {
			break
		}
		top--
	}
	for end+1 < len(lines) {
		if _, _, ok := label(lines[end+1]); !ok {
			break
		}
		end++
	}
	whole = true
	for i := max(0, top-1); i <= min(len(lines)-1, end+1); i++ {
		if strings.ContainsAny(lines[i], "↑↓") {
			whole = false
		}
	}
	for i := top; i <= end; i++ {
		n, text, _ := label(lines[i])
		if numbered && n != i-top+1 {
			whole = false
		}
		opts = append(opts, text)
	}
	return top, opts, p - top, whole
}

// framed is a screen line without its frame and the spaces around it.
func framed(line string) string { return strings.TrimSpace(strings.Trim(line, " │┃|")) }

// rule reports whether a line, without its frame, is a rule or a frame's
// edge.
func rule(t string) bool { return t != "" && strings.Trim(t, "─━╌╭╮╰╯") == "" }

// question is what a menu that isn't a known screen asks: every line
// above it, up to a rule or the screen's top, a paragraph a line. whole
// is false when it runs on past headingReach lines.
func question(lines []string, top int) (text string, whole bool) {
	var paras []string
	var para []string
	end := func() {
		if len(para) > 0 {
			paras = append([]string{strings.Join(para, " ")}, paras...)
			para = nil
		}
	}
	whole = true
	for i := top - 1; i >= 0; i-- {
		t := framed(lines[i])
		if rule(t) {
			break
		}
		if t != "" && i < top-headingReach {
			whole = false
			break
		}
		if t == "" {
			end()
			continue
		}
		para = append([]string{t}, para...)
	}
	end()
	return strings.Join(paras, "\n"), whole
}

// importsOf are the files the imports screen lists, from the lines under
// its heading: the ones under "External imports:", up to a blank line.
// whole is false when there's no such list, the screen leaves some of it
// out (past 8 files, "… +3 imports not shown.", or a path cut short), or
// a line of it isn't a path (a note, or a long path wrapped).
func importsOf(lines []string) (files []string, whole bool) {
	in := false
	for _, l := range lines {
		t := framed(l)
		switch {
		case !in:
			in = t == "External imports:"
		case t == "":
			return files, len(files) > 0
		case strings.Contains(t, "…") || !isPath(t):
			return files, false
		default:
			files = append(files, t)
		}
	}
	return files, in && len(files) > 0
}

// isPath reports whether a line starts the way a file's path does.
func isPath(t string) bool {
	for _, p := range []string{"/", "~", "./", "../"} {
		if strings.HasPrefix(t, p) {
			return true
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
