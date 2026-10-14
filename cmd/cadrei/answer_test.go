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
		"another command":      {bash, approval("rm -rf e2e-yes"), "yes", lost},
		"a question instead":   {ask, approval("mkdir e2e-yes"), "yes", lost},
		"another question":     {ask, question("Delete the test file?", "Yes", "No"), "1", lostWriter},
		"other options":        {ask, question("Upload the test file to the signing site?", "No", "Yes"), "1", lostWriter},
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
	// Review of 0.2.2 (M3): prompts recorded live that cadrei can't check
	// get no key, whatever the record: a page fetch (no "Esc to cancel",
	// the URL its own way), an MCP tool (its input not as recorded), a plan
	// approval (no plain Yes).
	for name, tc := range map[string]*session.Request{
		"fetch-live.txt": {Code: "jsw", Kind: session.ForApproval, Tool: "WebFetch", Summary: "https://example.com"},
		"mcp-live.txt":   {Code: "rxn", Kind: session.ForApproval, Tool: "mcp__files__upload", Summary: `mcp__files__upload {"path":"resume.pdf"}`},
		"plan-live.txt":  {Code: "226", Kind: session.ForApproval, Tool: "ExitPlanMode", Summary: `{"plan":"1. Read README.md"}`},
	} {
		w := &waitingOn{team: "dev", role: "engineer", req: tc}
		if key, why := keyFor(screenFixture(t, name), w, "yes"); key != "" || why == "" {
			t.Errorf("%s: %q %q", name, key, why)
		}
		if why := checkChoice(w, tc.Code, "yes", plain); why != "Cadrei can't check this kind of prompt on the screen, so answer it in its window: cadrei attach dev" {
			t.Errorf("%s: %q", name, why)
		}
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
		{multi, "1", "This question needs more than one pick, so answer it in its window: cadrei attach docs"},
		// Review of 0.2.2 (M3): a prompt cadrei can't check on the screen.
		{&waitingOn{team: "dev", role: "engineer", req: &session.Request{Code: "7f3", Kind: session.ForApproval, Tool: "mcp__files__upload", Summary: `mcp__files__upload {"path":"/w/a.pdf"}`}}, "yes",
			"Cadrei can't check this kind of prompt on the screen, so answer it in its window: cadrei attach dev"},
		{&waitingOn{team: "dev", role: "engineer", req: &session.Request{Code: "7f3", Kind: session.ForApproval, Tool: "Bash"}}, "yes",
			"Cadrei can't check this kind of prompt on the screen, so answer it in its window: cadrei attach dev"},
		{&waitingOn{team: "dev", role: "engineer", req: &session.Request{Code: "7f3", Kind: session.ForApproval, Tool: "ExitPlanMode", Summary: `{"plan":"x"}`}}, "yes",
			"Cadrei can't check this kind of prompt on the screen, so answer it in its window: cadrei attach dev"},
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
