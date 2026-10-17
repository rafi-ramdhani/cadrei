//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// answerFixtures holds the prompt screens, found before any test moves
// to its own folder.
var answerFixtures, _ = filepath.Abs("testdata/answer")

// screenFixture is a prompt screen from testdata/answer, with what a
// member's window shows above it: earlier conversation, with numbered
// lists of its own.
func screenFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(answerFixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	return "⏺ Here's the plan:\n  1. Yes\n  2. No\n\n" + "────────────────────────────────────────\n" + string(raw)
}

// tallCommand is the command in tall-live.txt, as the state hook records
// it: 34 lines.
var tallCommand = func() string {
	lines := []string{"cat > tall.txt <<'EOF'"}
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	return strings.Join(append(lines, "EOF", "wc -l tall.txt"), "⏎")
}()

// longPath is the Write in longpath-live.txt: a file outside the
// member's folder, with a path too long for the window.
func longPath() *waitingOn {
	w := approval("/Users/me/e2e/a-folder-with-a-rather-long-name-for-wrapping/and-a-second-long-folder-name/notes.txt")
	w.req.Tool, w.dir = "Write", "/Users/me/e2e/app"
	return w
}

// rawFixture is a prompt screen from testdata/answer as it is: the whole
// window.
func rawFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(answerFixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// wrappedDescription is question-live.txt with the first option's
// description wrapped at 80 columns (item 9, as seen live).
func wrappedDescription(t *testing.T) string {
	return strings.Replace(screenFixture(t, "question-live.txt"), "     Upload resume.pdf to the signing site\n",
		"     Upload resume.pdf to the signing site, which keeps a copy of it for thirty\n     days\n", 1)
}

// longLabel is a label too long for an 80-column window.
const longLabel = "Yes, upload resume.pdf to the signing site now and keep a copy for the records"

// wrappedLabel is question-live.txt with longLabel for the first option,
// wrapped at 80 columns above its description.
func wrappedLabel(t *testing.T) string {
	return strings.Replace(screenFixture(t, "question-live.txt"), "❯ 1. Yes\n",
		"❯ 1. Yes, upload resume.pdf to the signing site now and keep a copy for the\n     records\n", 1)
}

// tool is a permission request for tool, with what it would touch.
func tool(name, summary string) *waitingOn {
	w := approval(summary)
	w.req.Tool = name
	return w
}

// withOptions is w with its prompt's options, as the state hook reads
// them off the screen.
func withOptions(w *waitingOn, options ...string) *waitingOn {
	w.req.Options = options
	return w
}

// planApproval is the plan approval in plan-live.txt, as the state hook
// would record it (its input isn't seen live), with its options read off
// the screen.
func planApproval() *waitingOn {
	return withOptions(tool("ExitPlanMode", `{"plan":"1. Read README.md to check how it currently ends (trailing newline).\n2. Append the line \"Plan test\" at the end of README.md, then confirm with a final read."}`),
		// Its hint goes on at its label's column, so it's read as the rest
		// of the label.
		"Yes, and use auto mode", "Yes, manually approve edits", "Tell Claude what to change shift+tab to approve with this feedback")
}

// bashChoices are bash-prompt-live.txt's options, as read off the screen.
var bashChoices = []string{"Yes", "Yes, and always allow access to <work> from this project", "Yes, and switch to auto mode · auto mode handles these prompts for you", "No"}

// W6: an approval's other choices, read off the screen, are answered by
// number when the screen shows the same ones, and the guard's ask says
// the choice can last. A plan approval, which has no plain Yes, takes its
// choices and no; not yes, nor its choice that asks for text.
func TestAnApprovalsOtherChoices(t *testing.T) {
	bash := screenFixture(t, "bash-prompt-live.txt")
	w := withOptions(approval("mkdir e2e-yes"), bashChoices...)
	for choice, key := range map[string]string{"yes": "1", "no": "4", "2": "2", "3": "3", "4": "4", "1": "1"} {
		if got, why := keyFor(bash, w, choice); got != key || why != "" {
			t.Errorf("%s: %q %q", choice, got, why)
		}
	}
	if !saysNo(w, "4") || saysNo(w, "2") {
		t.Error("only the option labeled No says no")
	}
	other := strings.Replace(bash, "   2. Yes, and always allow access to\n      <work> from this project\n", "   2. Yes, and always allow access to\n      / from this project\n", 1)
	if key, why := keyFor(other, w, "2"); key != "" || !strings.Contains(why, "I couldn't find that prompt") {
		t.Errorf("other options: %q %q", key, why)
	}
	for choice, want := range map[string]string{
		"2": `Cadrei: answer the engineer in dev with "2. Yes, and always allow access to <work> from this project"? That choice can last past this one prompt, as its words say. It wants to run: mkdir e2e-yes`,
		"1": "Cadrei: allow once? The engineer in dev wants to run: mkdir e2e-yes",
		"4": "Cadrei: tell the engineer in dev no? It wants to run: mkdir e2e-yes",
	} {
		if got := askReason(w, choice); got != want {
			t.Errorf("ask %s: %q", choice, got)
		}
	}
	if note := w.note(); !strings.Contains(note, "Its choices, word for word, which can last past this prompt: 2. Yes, and always allow access to <work> from this project, 3. Yes, and switch to auto mode") {
		t.Errorf("note: %q", note)
	}

	plan := screenFixture(t, "plan-live.txt")
	p := planApproval()
	for choice, key := range map[string]string{"1": "1", "2": "2", "no": "Escape"} {
		if got, why := keyFor(plan, p, choice); got != key || why != "" {
			t.Errorf("plan %s: %q %q", choice, got, why)
		}
	}
	if _, why := keyFor(plan, p, "yes"); flat(render(why, false)) != `This prompt has no plain "Yes". Pick one of its choices with cadrei answer 7f3 <n> (1. Yes, and use auto mode, 2. Yes, manually approve edits), or answer it in its window: cadrei attach dev` {
		t.Errorf("plan yes: %q", why)
	}
	if why := checkChoice(p, "7f3", "3", plain); why != "7f3 takes yes, no, or one of its choices: 1, 2. Use cadrei answer 7f3 no, for example." {
		t.Errorf("plan 3: %q", why)
	}
	if verb, object, _ := act(p.req); verb != "carry out this plan" || !strings.HasSuffix(object, "then confirm with a final read.") {
		t.Errorf("plan act: %q %q", verb, object)
	}
	if note := p.note(); !strings.Contains(note, "It has no plain Yes: answer with cadrei answer 7f3 <n> for one of its choices, or no.") {
		t.Errorf("plan note: %q", note)
	}
	// Before its choices are read off the screen, a plan approval has only
	// no; yes says there's no plain Yes.
	p.req.Options = nil
	if _, why := keyFor(plan, p, "yes"); !strings.Contains(why, `no plain "Yes", only choices that would allow it for good`) {
		t.Errorf("plan yes, no choices: %q", why)
	}
}

// W7: the review is sent only when it shows exactly the picks, for each
// question in order: not one with a pick missing, one more, another
// answer, the questions the other way round, or no option to send it.
// A pick is read as one field per question.
func TestTheReviewMustShowExactlyTheAnswers(t *testing.T) {
	req := &session.Request{Code: "k2m", Kind: session.ForQuestion, Parts: []session.Part{
		{Question: "Which files go to the signing site?", Options: []string{"resume.pdf", "cover.pdf"}, Multi: true},
		{Question: "Send them now?", Options: []string{"Yes", "No"}}}}
	review := filesAndSendScreens()[4]
	if key, ok := reviewShows(review, req, [][]int{{1, 2}, {1}}); key != "1" || !ok {
		t.Errorf("the review: %q %v", key, ok)
	}
	for name, picks := range map[string][][]int{"one pick less": {{1}, {1}}, "the other answer": {{1, 2}, {2}}} {
		if _, ok := reviewShows(review, req, picks); ok {
			t.Errorf("%s: sent", name)
		}
	}
	for name, screen := range map[string]string{
		"a pick more":     strings.Replace(review, "→ Yes", "→ Yes, No", 1),
		"the other order": strings.Replace(strings.Replace(review, "Which files go to the signing site?", "Q1", 1), "Send them now?", "Which files go to the signing site?", 1),
		"no way to send":  strings.Replace(review, "1. Submit answers", "1. Go back", 1),
		"no review":       strings.Replace(review, "Ready to submit your answers?", "", 1),
	} {
		if _, ok := reviewShows(screen, req, [][]int{{1, 2}, {1}}); ok {
			t.Errorf("%s: sent", name)
		}
	}
	for choice, ok := range map[string]bool{"1,2 1": true, "2 2": true, "1": false, "1 1,2": false, "3 1": false, "1,1 1": false, "1 1 1": false, "01 1": false} {
		if _, got := partPicks(req, choice); got != ok {
			t.Errorf("%q: %v", choice, got)
		}
	}
}

// inApp is w with its member started in /Users/me/app.
func inApp(w *waitingOn) *waitingOn {
	w.dir = "/Users/me/app"
	return w
}

// approval and question are records as the state hook writes them.
func approval(summary string) *waitingOn {
	return &waitingOn{team: "dev", role: "engineer", req: &session.Request{Code: "7f3", Kind: session.ForApproval, Tool: "Bash", Summary: summary}}
}

func question(text string, options ...string) *waitingOn {
	return &waitingOn{team: "docs", role: "writer", req: &session.Request{Code: "k2m", Kind: session.ForQuestion, Tool: "AskUserQuestion", Question: text, Options: options}}
}

// 0.2.2 (6.3, 6.4): the key for each answer, from the recorded screens:
// yes is the option labeled exactly Yes (1, never 3, which switches to auto
// mode), no is the one labeled No, and a question's pick is its number.
// The earlier conversation's own numbered list isn't taken for the prompt.
func TestTheKeyForEachAnswer(t *testing.T) {
	live := screenFixture(t, "bash-prompt-live.txt")
	liveAsk := screenFixture(t, "question-live.txt")
	write := screenFixture(t, "write-live.txt")
	file := approval("/Users/me/app/notes.txt")
	file.req.Tool, file.dir = "Write", "/Users/me/app"
	ask := screenFixture(t, "question.txt")
	for _, tc := range []struct {
		screen string
		w      *waitingOn
		choice string
		key    string
	}{
		// A member's own prompt: dashed rules around the command, and a
		// long option that wraps.
		{live, approval("mkdir e2e-yes"), "yes", "1"},
		{live, approval("mkdir e2e-yes"), "no", "4"},
		{liveAsk, question("Upload resume.pdf to the signing site?", "Yes", "No"), "2", "2"},
		// A file prompt names the file from the member's folder; yes is
		// 1, never 2 (accept edits).
		{write, file, "yes", "1"},
		{write, file, "no", "3"},
		// Recorded in the second run: an Edit prompt names the file from
		// the member's folder; a command with a blank line in it is
		// framed, line by line.
		{screenFixture(t, "edit-live.txt"), &waitingOn{team: "dev", role: "engineer", dir: "/Users/me/app",
			req: &session.Request{Code: "xqw", Kind: session.ForApproval, Tool: "Edit", Summary: "/Users/me/app/README.md"}}, "yes", "1"},
		{screenFixture(t, "edit-live.txt"), &waitingOn{team: "dev", role: "engineer", dir: "/Users/me/app",
			req: &session.Request{Code: "xqw", Kind: session.ForApproval, Tool: "Edit", Summary: "/Users/me/app/README.md"}}, "no", "3"},
		{screenFixture(t, "para-live.txt"), approval("cat > para.txt <<'EOF'⏎line one⏎⏎line two⏎EOF⏎cat para.txt"), "yes", "1"},
		// A long command wraps onto framed lines.
		{screenFixture(t, "wrapped-live.txt"), approval("rm <work>/notes.txt && ls -la <work>/notes.txt 2>&1"), "no", "4"},
		// Re-review round 2 (M7): a prompt taller than 24 lines, with its
		// top on the screen (a taller window).
		{drawn(t, approval(tallCommand).req), approval(tallCommand), "yes", "1"},
		{ask, question("Upload the test file to the signing site?", "Yes", "No"), "1", "1"},
		{ask, question("Upload the test file to the signing site?", "Yes", "No"), "2", "2"},
		// A long label recorded cut short still matches.
		{strings.Replace(ask, "❯ 1. Yes", "❯ 1. Yes, upload it now to the signing site", 1), question("Upload the test file to the signing site?", "Yes, upload it…", "No"), "1", "1"},
		// Item 9: at 80 columns a description wraps onto a second line at
		// its own column, and so does a long label, before its description.
		{wrappedDescription(t), question("Upload resume.pdf to the signing site?", "Yes", "No"), "1", "1"},
		{wrappedLabel(t), question("Upload resume.pdf to the signing site?", longLabel, "No"), "1", "1"},
		{wrappedLabel(t), question("Upload resume.pdf to the signing site?", "Yes, upload resume.pdf to the signing site now…", "No"), "2", "2"},
		// W5, recorded live: a page fetch (no "Esc to cancel" line, the URL
		// with a slash after its host, no plain No, so no is Escape) and an
		// MCP tool (its arguments as name: JSON).
		{screenFixture(t, "fetch-live.txt"), tool("WebFetch", "https://example.com"), "yes", "1"},
		{screenFixture(t, "fetch-live.txt"), tool("WebFetch", "https://example.com"), "no", "Escape"},
		{screenFixture(t, "fetch-live.txt"), tool("WebFetch", "https://example.com/"), "yes", "1"},
		{screenFixture(t, "mcp-live.txt"), tool("mcp__files__upload", `mcp__files__upload {"path":"resume.pdf"}`), "yes", "1"},
		{screenFixture(t, "mcp-live.txt"), tool("mcp__files__upload", `mcp__files__upload {"path":"resume.pdf"}`), "no", "3"},
		// W5, NOT seen live (the -unconfirmed screens are built from Claude
		// Code 2.1.302's own text and the recorded Edit prompt; PR E's live
		// run checks them): a web search, a file to read, a notebook, an
		// edit in several places.
		{screenFixture(t, "search-unconfirmed.txt"), tool("WebSearch", "tmux default-size detached session"), "yes", "1"},
		{screenFixture(t, "read-unconfirmed.txt"), tool("Read", "/Users/me/shared/notes.md"), "yes", "1"},
		{screenFixture(t, "read-unconfirmed.txt"), tool("Read", "/Users/me/shared/notes.md"), "no", "3"},
		{screenFixture(t, "notebook-unconfirmed.txt"), inApp(tool("NotebookEdit", "/Users/me/app/analysis.ipynb")), "yes", "1"},
		{screenFixture(t, "edit-live.txt"), inApp(tool("MultiEdit", "/Users/me/app/README.md")), "yes", "1"},
	} {
		key, why := keyFor(tc.screen, tc.w, tc.choice)
		if key != tc.key || why != "" {
			t.Errorf("%s %s: key %q, %q", tc.w.req.Summary+tc.w.req.Question, tc.choice, key, why)
		}
	}
}

// A screen that doesn't match the record gets no key: another command,
// another question, other options, a question where a command was asked,
// no prompt at all. With no plain Yes, yes refuses; with no No, no is
// Escape.
func TestNoKeyForAScreenThatDoesntMatch(t *testing.T) {
	bash := screenFixture(t, "bash-prompt-live.txt")
	ask := screenFixture(t, "question.txt")
	lost := "I couldn't find that prompt on the engineer's screen, so I didn't press anything. Answer it in its window: cadrei attach dev"
	lostWriter := "I couldn't find that prompt on the writer's screen, so I didn't press anything. Answer it in its window: cadrei attach docs"
	for name, tc := range map[string]struct {
		screen string
		w      *waitingOn
		choice string
		why    string
	}{
		"another command":    {bash, approval("rm -rf e2e-yes"), "yes", lost},
		"a question instead": {ask, approval("mkdir e2e-yes"), "yes", lost},
		"another question":   {ask, question("Delete the test file?", "Yes", "No"), "1", lostWriter},
		"other options":      {ask, question("Upload the test file to the signing site?", "No", "Yes"), "1", lostWriter},
		// Item 9: a wrapped label must go on exactly as recorded, and whole.
		"a label going on otherwise": {strings.Replace(wrappedLabel(t), "     records\n", "     archive\n", 1),
			question("Upload resume.pdf to the signing site?", longLabel, "No"), "1", lostWriter},
		"a label without its end": {strings.Replace(wrappedLabel(t), "     records\n", "", 1),
			question("Upload resume.pdf to the signing site?", longLabel, "No"), "1", lostWriter},
		"no prompt":            {"⏺ Done.\n\n❯ \n? for shortcuts\n", approval("mkdir e2e-yes"), "yes", lost},
		"options gone":         {strings.Replace(bash, " Esc to cancel · Tab to amend\n", "", 1), approval("mkdir e2e-yes"), "yes", lost},
		"a gap in the numbers": {strings.Replace(bash, "   2. Yes, and always", "   5. Yes, and always", 1), approval("mkdir e2e-yes"), "yes", lost},
		// Re-review round 2 (M7): a prompt taller than the window, as an
		// 80x24 one shows it (its top is gone, with nothing kept above),
		// and a path the window shows cut short at its start.
		"taller than its window": {rawFixture(t, "tall-live.txt"), approval(tallCommand), "yes",
			"That prompt is taller than the engineer's window, so cadrei can't see it whole. Answer it in its window: cadrei attach dev"},
		"a path cut short": {screenFixture(t, "longpath-live.txt"), longPath(), "yes",
			"The engineer's window shows that file's path cut short, so cadrei can't check it. Answer it in its window: cadrei attach dev"},
		"no plain Yes": {strings.Replace(bash, " ❯ 1. Yes\n", " ❯ 1. Yes, and don't ask again for mkdir commands\n", 1), approval("mkdir e2e-yes"), "yes",
			"This prompt has no plain \"Yes\", only choices that would allow it for good, so answer it in its window: cadrei attach dev"},
	} {
		key, why := keyFor(tc.screen, tc.w, tc.choice)
		if key != "" || flat(render(why, false)) != tc.why {
			t.Errorf("%s: key %q, %q", name, key, why)
		}
	}
	// Review of 0.2.2 (M4): the screen must show exactly what the record
	// says. A command that holds the recorded one, or one the short record
	// is only the start of; another file of the same name; a file whose
	// text names the recorded one.
	write := screenFixture(t, "write-live.txt")
	inApp := func(path string) *waitingOn {
		w := approval(path)
		w.req.Tool, w.dir = "Write", "/Users/me/app"
		return w
	}
	for name, tc := range map[string]struct {
		screen string
		w      *waitingOn
	}{
		"a command holding it":       {strings.Replace(bash, "╌\n mkdir e2e-yes\n", "╌\n mkdir e2e-yes && rm -rf <work>\n", 1), approval("mkdir e2e-yes")},
		"a longer command":           {strings.Replace(bash, "╌\n mkdir e2e-yes\n", "╌\n git push --force origin main\n", 1), approval("git push")},
		"a short record":             {strings.Replace(bash, "╌\n mkdir e2e-yes\n", "╌\n ls -la\n", 1), approval("ls")},
		"another file":               {write, inApp("/Users/me/app/other.txt")},
		"the same name elsewhere":    {strings.Replace(write, " notes.txt\n", " sub/notes.txt\n", 1), inApp("/Users/me/app/notes.txt")},
		"its name in another's text": {strings.Replace(strings.Replace(write, " notes.txt\n", " README.md\n", 1), "  1 hello", "  1 notes.txt", 1), inApp("/Users/me/app/notes.txt")},
		"its name alone in the text": {strings.Replace(strings.Replace(write, " notes.txt\n", " README.md\n", 1), "  1 hello", " notes.txt", 1), inApp("/Users/me/app/notes.txt")},
		// Re-review of 0.2.2 (L10): the command between the dashed rules,
		// whole. Not one that goes on after the record on the next framed
		// line, not the description line above them, and not a screen
		// with no dashed rules (the S4 spike's, as written down).
		"a command going on past it": {screenFixture(t, "wrapped-live.txt"), approval("rm <work>/notes.txt && ls -la")},
		"only the description line":  {bash, approval("Create directory e2e-yes in the current working directory")},
		"no dashed rules":            {screenFixture(t, "bash-prompt.txt"), approval("mkdir <work>/s4a")},
		// Re-review of 0.2.2 (L11): file text can't stand in for the
		// header: a long preview, text that holds a solid rule, with or
		// without a title under it, or more lines in the header.
		"its name in a long text":  {strings.Replace(strings.Replace(write, " notes.txt\n", " README.md\n", 1), "  1 hello", strings.Repeat("  1 hello\n", 70)+" notes.txt", 1), inApp("/Users/me/app/notes.txt")},
		"a solid rule in the text": {strings.Replace(strings.Replace(write, " notes.txt\n", " README.md\n", 1), "  1 hello", "─────────────────\n notes.txt", 1), inApp("/Users/me/app/notes.txt")},
		"a header in the text":     {strings.Replace(strings.Replace(write, " notes.txt\n", " README.md\n", 1), "  1 hello", "─────────────────\n Create file\n notes.txt", 1), inApp("/Users/me/app/notes.txt")},
		"more in the header":       {strings.Replace(write, " notes.txt\n╌", " notes.txt\n README.md\n╌", 1), inApp("/Users/me/app/notes.txt")},
		// No solid rule anywhere: the box's top isn't on the screen.
		"no solid rule": {strings.ReplaceAll(bash, "────────────────────────────────────────", ""), approval("mkdir e2e-yes")},
	} {
		if key, why := keyFor(tc.screen, tc.w, "yes"); key != "" || why == "" {
			t.Errorf("%s: %q %q", name, key, why)
		}
	}
	// W5: a page fetch, an MCP tool, a web search and a file to read
	// show exactly what the record says, or get no key: another URL, one
	// the record goes on past, a fetch without its footer where a Bash
	// prompt's is needed; other arguments, more of them, another server;
	// another query, one with a list of sites; another file.
	fetch, mcp := screenFixture(t, "fetch-live.txt"), screenFixture(t, "mcp-live.txt")
	search, read := screenFixture(t, "search-unconfirmed.txt"), screenFixture(t, "read-unconfirmed.txt")
	upload := `mcp__files__upload {"path":"resume.pdf"}`
	for name, tc := range map[string]struct {
		screen string
		w      *waitingOn
	}{
		"another URL":        {fetch, tool("WebFetch", "https://example.org")},
		"a longer URL":       {fetch, tool("WebFetch", "https://example.com/admin")},
		"a URL with a query": {strings.Replace(fetch, "url: https://example.com/", "url: https://example.com/?a=1", 1), tool("WebFetch", "https://example.com")},
		"a fetch as Bash":    {fetch, approval("https://example.com")},
		"other arguments":    {mcp, tool("mcp__files__upload", `mcp__files__upload {"path":"cover.pdf"}`)},
		"more arguments":     {strings.Replace(mcp, ` path: "resume.pdf"`, ` path: "resume.pdf"`+"\n"+` to: "anyone"`, 1), tool("mcp__files__upload", upload)},
		"fewer arguments":    {mcp, tool("mcp__files__upload", `mcp__files__upload {"path":"resume.pdf","to":"anyone"}`)},
		"another server":     {mcp, tool("mcp__drive__upload", `mcp__drive__upload {"path":"resume.pdf"}`)},
		"input that's cut":   {mcp, tool("mcp__files__upload", `mcp__files__upload {"path":"resume.pdf"`)},
		"another query":      {search, tool("WebSearch", "tmux default-size")},
		"only some sites":    {strings.Replace(search, "detached session\n", "detached session, only allowing domains: example.com\n", 1), tool("WebSearch", "tmux default-size detached session")},
		"another file":       {read, tool("Read", "/Users/me/shared/other.md")},
		"a read as an edit":  {read, tool("Edit", "/Users/me/shared/notes.md")},
	} {
		if key, why := keyFor(tc.screen, tc.w, "yes"); key != "" || why == "" {
			t.Errorf("%s: %q %q", name, key, why)
		}
	}
	// W6: a plan approval shows a plan other than the recorded one, or
	// only part of it; and its record names no plan (the hook's input for
	// it isn't seen live), so cadrei can't check it.
	planScreen := screenFixture(t, "plan-live.txt")
	for name, w := range map[string]*waitingOn{
		"another plan":     tool("ExitPlanMode", `{"plan":"1. Read README.md to check how it currently ends (trailing newline).\n2. Delete README.md."}`),
		"part of the plan": tool("ExitPlanMode", `{"plan":"1. Read README.md to check how it currently ends (trailing newline)."}`),
	} {
		if key, why := keyFor(planScreen, w, "no"); key != "" || why == "" {
			t.Errorf("%s: %q %q", name, key, why)
		}
	}
	if why := checkChoice(tool("ExitPlanMode", `{"planFile":"x.md"}`), "7f3", "no", plain); why != "Cadrei can't check this kind of prompt on the screen, so answer it in its window: cadrei attach dev" {
		t.Errorf("no plan: %q", why)
	}
	// The full path shown matches, with the member's folder not known.
	full := inApp("/Users/me/app/notes.txt")
	full.dir = ""
	if key, _ := keyFor(strings.Replace(write, " notes.txt\n", " /Users/me/app/notes.txt\n", 1), full, "yes"); key != "1" {
		t.Errorf("the full path: %q", key)
	}
	// A multi-select question isn't taken for the recorded single one.
	multi := question("Which files go to the signing site?", "resume.pdf", "cover.pdf")
	if key, _ := keyFor(screenFixture(t, "multi-live.txt"), multi, "1"); key != "" {
		t.Errorf("multi-select: %q", key)
	}
	noNo := strings.Replace(bash, "   4. No\n", "", 1)
	if key, why := keyFor(noNo, approval("mkdir e2e-yes"), "no"); key != "Escape" || why != "" {
		t.Errorf("no No option: %q %q", key, why)
	}
}

// 0.2.2 (6.3): a choice that can't answer the request is refused before
// the screen is looked at: a pick for a yes-or-no prompt, yes for a
// question, an option past the member's own (Type something. and Chat
// about this come after them), and any pick of a multi-select question.
func TestAChoiceThatCantAnswer(t *testing.T) {
	ask := question("Upload the test file to the signing site?", "Yes", "No")
	multi := question("Which files?", "a.pdf", "b.pdf")
	multi.req.Multi = true
	for _, tc := range []struct {
		w      *waitingOn
		choice string
		want   string
	}{
		{approval("x"), "1", "7f3 is a yes-or-no prompt. Use cadrei answer 7f3 yes or cadrei answer 7f3 no."},
		{approval("x"), "Yes", "7f3 is a yes-or-no prompt. Use cadrei answer 7f3 yes or cadrei answer 7f3 no."},
		{ask, "yes", "k2m has options 1 to 2. Use cadrei answer k2m 1, for example."},
		{ask, "3", "k2m has options 1 to 2. Use cadrei answer k2m 1, for example."},
		{ask, "4", "k2m has options 1 to 2. Use cadrei answer k2m 1, for example."},
		{ask, "0", "k2m has options 1 to 2. Use cadrei answer k2m 1, for example."},
		{ask, "01", "k2m has options 1 to 2. Use cadrei answer k2m 1, for example."},
		{multi, "1", "That question has more options or questions than cadrei keeps, so answer it in its window: cadrei attach docs"},
		// Review of 0.2.2 (M3): a prompt cadrei can't check on the screen.
		{&waitingOn{team: "dev", role: "engineer", req: &session.Request{Code: "7f3", Kind: session.ForApproval, Tool: "Glob", Summary: `Glob {"pattern":"**/*.pdf"}`}}, "yes",
			"Cadrei can't check this kind of prompt on the screen, so answer it in its window: cadrei attach dev"},
		{&waitingOn{team: "dev", role: "engineer", req: &session.Request{Code: "7f3", Kind: session.ForApproval, Tool: "Bash"}}, "yes",
			"Cadrei can't check this kind of prompt on the screen, so answer it in its window: cadrei attach dev"},
		{&waitingOn{team: "dev", role: "engineer", req: &session.Request{Code: "7f3", Kind: session.ForApproval, Tool: "ExitPlanMode", Summary: `{}`}}, "yes",
			"Cadrei can't check this kind of prompt on the screen, so answer it in its window: cadrei attach dev"},
		// W6: with its choices read off the screen, an approval takes one
		// of them by number, but not one that asks for text.
		{withOptions(approval("x"), "Yes", "Yes, and don't ask again for x commands", "No"), "4", "7f3 takes yes, no, or one of its choices: 1, 2, 3. Use cadrei answer 7f3 no, for example."},
		{withOptions(approval("x"), "Yes", "No, and tell Claude what to do differently (esc)"), "2", "7f3 takes yes, no, or one of its choices: 1. Use cadrei answer 7f3 no, for example."},
		// Re-review of 0.2.2 (L12): a command recorded cut short.
		{approval("rm -rf build && " + strings.Repeat("x", 290) + "…"), "yes", "That's too long for cadrei to check whole, so answer it in its window: cadrei attach dev"},
	} {
		if got := checkChoice(tc.w, tc.w.req.Code, tc.choice, plain); got != tc.want {
			t.Errorf("%s: %q", tc.choice, got)
		}
	}
	for _, ok := range []string{"yes", "no"} {
		if got := checkChoice(approval("x"), "7f3", ok, plain); got != "" {
			t.Errorf("%s: %q", ok, got)
		}
	}
	if got := checkChoice(ask, "k2m", "2", plain); got != "" {
		t.Errorf("2: %q", got)
	}
}

// 0.2.2 (6.3): the guard's text in Claude Code's prompt, from the record
// only.
func TestTheAskReason(t *testing.T) {
	w := approval("gh pr merge 42 --squash")
	w.project = "my-app"
	for choice, want := range map[string]string{
		"yes": "Cadrei: allow once? The engineer on my-app wants to run: gh pr merge 42 --squash",
		"no":  "Cadrei: tell the engineer on my-app no? It wants to run: gh pr merge 42 --squash",
	} {
		if got := askReason(w, choice); got != want {
			t.Errorf("%s: %q", choice, got)
		}
	}
	q := question("Upload resume.pdf to the signing site?", "Yes", "No")
	if got := askReason(q, "1"); got != `Cadrei: answer the writer in docs with "1. Yes"? It asks: Upload resume.pdf to the signing site?` {
		t.Errorf("question: %q", got)
	}
	edit := approval("/w/a.go")
	edit.req.Tool = "Edit"
	if got := askReason(edit, "yes"); got != "Cadrei: allow once? The engineer in dev wants to edit: /w/a.go" {
		t.Errorf("edit: %q", got)
	}
}
