package cadrei

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/rafi-ramdhani/cadrei/internal/registry"
)

// Acceptance 81: README and guide.md hold the spec's 7.9.6 texts, word for
// word (backticks aside, which keep GitHub from hiding <name>).
func TestTheDocsHoldTheEngineAndVolumeTexts(t *testing.T) {
	spec, err := os.ReadFile("docs/specs/0.2.1-onboarding-polish.md")
	if err != nil {
		t.Skip("no spec here")
	}
	s := string(spec)
	start, end := strings.Index(s, "#### 7.9.6"), strings.Index(s, "### 7.10")
	if start < 0 || end < start {
		t.Fatal("no 7.9.6 in the spec")
	}
	blocks := regexp.MustCompile("(?s)```text\n(.*?)```").FindAllStringSubmatch(s[start:end], -1)
	if len(blocks) != 2 {
		t.Fatalf("%d texts in 7.9.6", len(blocks))
	}
	norm := func(s string) string { return strings.Join(strings.Fields(strings.ReplaceAll(s, "`", "")), " ") }
	for _, c := range []struct {
		file  string
		texts []int
	}{{"docs/guide.md", []int{0, 1}}} {
		raw, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range c.texts {
			if !strings.Contains(norm(string(raw)), norm(blocks[i][1])) {
				t.Errorf("%s lacks the 7.9.6 text %d", c.file, i+1)
			}
		}
	}
}

// Acceptance 126: docs/tools.md is 7.13.6's text, word for word, and the
// README and guide.md link it.
func TestTheToolsPageIsTheSpecs(t *testing.T) {
	spec, err := os.ReadFile("docs/specs/0.2.1-onboarding-polish.md")
	if err != nil {
		t.Skip("no spec here")
	}
	s := string(spec)
	start := strings.Index(s, "#### 7.13.6")
	if start < 0 {
		t.Fatal("no 7.13.6 in the spec")
	}
	open := strings.Index(s[start:], "````markdown\n")
	if open < 0 {
		t.Fatal("no page in 7.13.6")
	}
	body := s[start+open+len("````markdown\n"):]
	body = body[:strings.Index(body, "````")]
	page, err := os.ReadFile("docs/tools.md")
	if err != nil {
		t.Fatal(err)
	}
	// 0.2.2 adds a waiting member's request (its spec, section 10): the
	// page is 7.13.6's text with that added, and nothing else.
	base := string(page)
	table, next := strings.Index(base, "`request`, what a waiting member asks you:\n"), strings.Index(base, "Each project in `projects`:")
	row := strings.Index(base, "| `request` | object, optional |")
	if table < 0 || next < table || row < 0 {
		t.Fatal("docs/tools.md lacks 0.2.2's request field")
	}
	base = base[:table] + base[next:]
	base = base[:row] + base[row+strings.Index(base[row:], "\n")+1:]
	// And a running member's context (0.2.2, context use): its row and
	// its table, which comes just before the request's.
	ctable, cnext := strings.Index(base, "`context`, how full a running member's context is"), strings.Index(base, "Each project in `projects`:")
	crow := strings.Index(base, "| `context` | object, optional |")
	if ctable < 0 || cnext < ctable || crow < 0 {
		t.Fatal("docs/tools.md lacks 0.2.2's context field")
	}
	base = base[:ctable] + base[cnext:]
	base = base[:crow] + base[crow+strings.Index(base[crow:], "\n")+1:]
	for added, was := range map[string]string{
		"a `waiting_for`, a request `kind`, a snapshot reason":                "a `waiting_for`, a snapshot reason",
		" an unknown request `kind` as one to answer in the member's window,": "",
		// 0.2.3: a prompt read off a member's screen (PR A), and a
		// start-up screen (PR C).
		"`approval` (it wants to run something), `question` (it asked you something), `prompt` (a prompt cadrei read off its screen, when Claude Code didn't say what it asks)" +
			" or `start` (a start-up screen, like its folder's trust or a login, read off its screen while it starts)": "`approval` (it wants to run something) or `question` (it asked you something)",
	} {
		if !strings.Contains(base, added) {
			t.Errorf("docs/tools.md lacks %q", added)
		}
		base = strings.Replace(base, added, was, 1)
	}
	if base != body {
		t.Error("docs/tools.md isn't 7.13.6's text, with 0.2.2's request field added")
	}
	// The promise as decided before 0.2.1 shipped (forward-compatibility
	// audit M1, S3 to S5): values can grow within version 1, with safe
	// meanings for unknown ones; names are keys; the old names stay.
	for _, want := range []string{
		"new values in the lists of values on this page",
		"Version 2 is only for removing or renaming a field, changing its type, or changing what an existing value means.",
		"an unknown member `state` like `unknown`", "an unknown snapshot `reason` like `periodic`",
		"compare them whole, and don't split them on `-`",
		"`legacy` and `cloned` stay in version 1",
		"`cadrei`, `cadreis` and `other_cadreis` mean your crew and your crews: they keep these names for all of version 1.",
		"pass `--json-version 1`",
		"Only `cadrei ls --json` and its stream promise their exit codes",
	} {
		if !strings.Contains(string(page), want) {
			t.Errorf("docs/tools.md lacks %q", want)
		}
	}
	for _, gone := range []string{"would come with version 2", "still say"} {
		if strings.Contains(string(page), gone) {
			t.Errorf("docs/tools.md still has %q", gone)
		}
	}
	for file, link := range map[string]string{
		"README.md":     "- [For tool builders](https://cadrei.dev/docs/tools/): read your crew's state from your own app",
		"docs/guide.md": "see [For tool builders](tools.md).",
	} {
		raw, _ := os.ReadFile(file)
		if !strings.Contains(string(raw), link) {
			t.Errorf("%s doesn't link the page", file)
		}
	}
}

// Audit qual-maint-1: the first-run screens in the README and guide.md
// are the ones cadrei draws (its goldens), with the styled marks and a
// release's header.
func TestTheDocsShowTheFirstRunAsItIs(t *testing.T) {
	golden := func(name string) string {
		raw, err := os.ReadFile("cmd/cadrei/testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	asDrawn := strings.NewReplacer("  ✓ ", "  + ", "  · ", "  - ", "cadrei v0.2.2\n", "cadrei, development build\n", "$ cadrei\n\n", "")
	section := func(file, heading string) []string {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		start := strings.Index(s, heading)
		if start < 0 {
			t.Fatalf("%s has no %q", file, heading)
		}
		s = s[start+len(heading):]
		if end := strings.Index(s, "\n## "); end >= 0 {
			s = s[:end]
		}
		var out []string
		for _, b := range regexp.MustCompile("(?s)```text\n(.*?)```").FindAllStringSubmatch(s, -1) {
			out = append(out, asDrawn.Replace(b[1]))
		}
		return out
	}
	// The README is short now, and links cadrei.dev's docs: the guide
	// holds the first run.
	guide := section("docs/guide.md", "## Your first run")
	if len(guide) < 3 {
		t.Fatalf("blocks: guide %d", len(guide))
	}
	first := golden("first-run.txt")
	for name, block := range map[string]string{"the guide's screen": guide[0], "the guide's steps": guide[1]} {
		if !strings.Contains(first, block) {
			t.Errorf("%s isn't the first run's:\n%s", name, block)
		}
	}
	if !strings.Contains(golden("plug-in.txt"), guide[len(guide)-1]) {
		t.Errorf("the guide's plug-in screen isn't cadrei's:\n%s", guide[len(guide)-1])
	}
	for _, file := range []string{"README.md", "docs/guide.md"} {
		raw, _ := os.ReadFile(file)
		if strings.Contains(string(raw), "tutorial") || strings.Contains(string(raw), "Checking your setup\n") {
			t.Errorf("%s still shows the old first run", file)
		}
	}
}

// The guide's sample greeting (the README's, before the README became a
// short page) is orchestrator.md's first-run greeting inside a project,
// filled in for main and my-app, with the tmux line it gets when it opens
// in tmux: word for word, so neither drifts from the other.
func TestTheGuidesGreetingIsTheOrchestratorsOwn(t *testing.T) {
	prompt, err := os.ReadFile("orchestrator.md")
	if err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile("docs/guide.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```text\n(.*?)```").FindAllStringSubmatch(string(prompt), -1)
	if len(blocks) == 0 || !strings.Contains(blocks[0][1], "set up for <project>.") {
		t.Fatal("orchestrator.md's first greeting isn't the one inside a project")
	}
	want := strings.NewReplacer("<crew>", "main", "<project>", "my-app").Replace(blocks[0][1]) + "New to tmux? Ask me how to get around.\n"
	if !strings.Contains(string(prompt), "`New to tmux? Ask me how to get around.`") || !strings.Contains(string(readme), "```text\n"+want+"```") {
		t.Errorf("the guide's greeting isn't:\n%s", want)
	}
}

// The brand art is all rights reserved (NOTICE): every art file in mascot/
// is listed there, the README says so, and release archives carry NOTICE
// beside the MIT LICENSE, which stays plain so GitHub reads it.
func TestTheNoticeCoversTheBrandArt(t *testing.T) {
	notice, err := os.ReadFile("NOTICE")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(notice), "Brand art: Copyright (c) 2026 Rafi Ramdhani Azis. All rights reserved.\nIt is not covered by the MIT license.") {
		t.Error("NOTICE doesn't reserve the brand art for LICENSE's holder")
	}
	if brand, _ := os.ReadDir("docs/brand"); len(brand) > 0 && !strings.Contains(string(notice), "  docs/brand/ ") {
		t.Error("NOTICE doesn't list docs/brand/")
	}
	entries, _ := os.ReadDir("mascot")
	for _, e := range entries {
		if !e.IsDir() && !strings.Contains(string(notice), "mascot/"+e.Name()) {
			t.Errorf("NOTICE doesn't list mascot/%s", e.Name())
		}
	}
	license, _ := os.ReadFile("LICENSE")
	readme, _ := os.ReadFile("README.md")
	release, _ := os.ReadFile(".goreleaser.yaml")
	if !strings.HasPrefix(string(license), "MIT License\n") || strings.Contains(string(license), "brand") {
		t.Error("LICENSE isn't plain MIT")
	}
	if !strings.Contains(string(readme), "all rights reserved, not covered by the MIT license. [NOTICE](NOTICE)") {
		t.Error("the README's license line doesn't name the brand art")
	}
	if !strings.Contains(string(release), "files: [LICENSE, NOTICE, README.md]") {
		t.Error("release archives don't carry NOTICE")
	}
}

// Review of fix round 6: what the docs say about uninstall --all is what
// it takes: its typed phrase, and only flags the uninstall commands have.
func TestTheUninstallDocsTakeItsOwnPhraseAndFlags(t *testing.T) {
	src, err := os.ReadFile("cmd/cadrei/uninstallall.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const deletePhrase = "([^"]+)"`).FindSubmatch(src)
	if m == nil {
		t.Fatal("no deletePhrase")
	}
	phrase := string(m[1])
	commands, _ := os.ReadFile("cmd/cadrei/commands.go")
	flags := map[string]bool{}
	for _, line := range strings.Split(string(commands), "\n") {
		if strings.Contains(line, `name: "uninstall`) {
			for _, f := range regexp.MustCompile(`--[a-z-]+`).FindAllString(line, -1) {
				flags[f] = true
			}
		}
	}
	docs := map[string][]string{
		"docs/guide.md":          {"type `" + phrase + "`", "`--confirm \"" + phrase + "\"`", "`cadrei uninstall --all --dry-run`"},
		"skills/cadrei/SKILL.md": {"cadrei uninstall --all --dry-run", `--confirm "` + phrase + `"`},
		"docs/tools.md":          {"`cadrei uninstall --all`"},
	}
	use := regexp.MustCompile(`cadrei uninstall((?: --?[a-z-]+(?: "[^"]*")?)*)`)
	for file, wants := range docs {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %s", file, want)
			}
		}
		for _, u := range use.FindAllStringSubmatch(text, -1) {
			for _, f := range regexp.MustCompile(`--?[a-z-]+`).FindAllString(regexp.MustCompile(`"[^"]*"`).ReplaceAllString(u[1], ""), -1) {
				if !flags[f] {
					t.Errorf("%s: %q uses %s, which no uninstall takes", file, u[0], f)
				}
			}
		}
		for _, q := range regexp.MustCompile(`--confirm "([^"]*)"`).FindAllStringSubmatch(text, -1) {
			if q[1] != phrase {
				t.Errorf("%s: --confirm %q isn't the phrase", file, q[1])
			}
		}
	}
}

// Forward-compatibility audit M4 and S9: the playbook can't loosen the
// safety rules, wherever the precedence is said; and the guide's
// projects.yaml example reads as it says.
func TestThePlaybookCantLoosenTheSafetyRules(t *testing.T) {
	skill, _ := os.ReadFile("skills/cadrei/SKILL.md")
	guide, _ := os.ReadFile("docs/guide.md")
	if !strings.Contains(string(skill), "Where it differs from this file, the playbook wins, except in What counts as the user's consent, Permissions for members, Stopping everything, Uninstalling, Reset and restore, and Rules: the playbook can add to those, never loosen them.") {
		t.Error("SKILL.md lets the playbook loosen the safety rules")
	}
	if !strings.Contains(string(guide), "never loosen them. A playbook line that tries to is shown to you, not followed.") {
		t.Error("the guide lets the playbook loosen the safety rules")
	}
	for _, f := range []string{"skills/cadrei/SKILL.md", "docs/guide.md", "orchestrator.md", "protocol.md"} {
		raw, _ := os.ReadFile(f)
		if regexp.MustCompile(`(?i)the playbook wins\.`).Match(raw) {
			t.Errorf("%s says the playbook wins, with no exception", f)
		}
	}
	block := regexp.MustCompile("(?s)```yaml\n(.*?)```").FindSubmatch(guide)
	if block == nil {
		t.Fatal("no projects.yaml example in the guide")
	}
	e := registry.Parse(string(block[1])).Get("my-app")
	if e == nil || e.Get("repo") != "you/my-app" || e.Get("team") != "dev" || e.Get("about") != "The main product" {
		t.Errorf("the guide's example reads as %+v", e)
	}
}

// docs/commands.md names only commands and options cadrei has: each
// heading's command is in the command table, and each --option is one a
// command's usage, or the code, takes.
func TestTheCommandReferenceIsReal(t *testing.T) {
	page, err := os.ReadFile("docs/commands.md")
	if err != nil {
		t.Fatal(err)
	}
	table, _ := os.ReadFile("cmd/cadrei/commands.go")
	var code strings.Builder
	files, _ := os.ReadDir("cmd/cadrei")
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".go") && !strings.HasSuffix(f.Name(), "_test.go") {
			raw, _ := os.ReadFile("cmd/cadrei/" + f.Name())
			code.Write(raw)
		}
	}
	for _, m := range regexp.MustCompile("(?m)^### `cadrei ?([^`]*)`").FindAllStringSubmatch(string(page), -1) {
		var words []string
		for _, w := range strings.Fields(m[1]) {
			if strings.ContainsAny(w, "[<|-") {
				break
			}
			words = append(words, w)
		}
		name := strings.Join(words, " ")
		if strings.HasPrefix(m[1], "--") {
			name = strings.Fields(m[1])[0]
		}
		if !strings.Contains(string(table), `{name: "`+name+`"`) {
			t.Errorf("docs/commands.md has a command cadrei doesn't: cadrei %s", m[1])
		}
	}
	for _, f := range regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(string(page), -1) {
		if !strings.Contains(code.String(), `"`+f+`"`) && !strings.Contains(string(table), f+" ") && !strings.Contains(string(table), f+"]") {
			t.Errorf("docs/commands.md has an option cadrei doesn't: %s", f)
		}
	}
}

// Every link and image the README names in this repository is there.
func TestTheReadmesLinksResolve(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	refs := regexp.MustCompile(`(?:\]\(|src="|srcset=")([^)"#\s]+)`).FindAllStringSubmatch(string(raw), -1)
	if len(refs) < 5 {
		t.Fatalf("links: %v", refs)
	}
	for _, r := range refs {
		if strings.Contains(r[1], "://") {
			continue
		}
		if _, err := os.Stat(r[1]); err != nil {
			t.Errorf("README.md links %s, which isn't there", r[1])
		}
	}
}

// A reply that was blocked reaches the orchestrator only as a status that
// names the rule: the member never sends the content another way, and the
// orchestrator never reads it from the member's window, transcript or
// files (the silent-members review, M1 and M3).
func TestABlockedReplyStaysBlocked(t *testing.T) {
	protocol, _ := os.ReadFile("protocol.md")
	skill, _ := os.ReadFile("skills/cadrei/SKILL.md")
	for _, want := range []string{
		"If your reply itself is denied or blocked, send one short status instead: that your reply was denied by auto mode (or by the rule named in square brackets, when the denial names one), and that the content is in your window. Nothing more: no explanation, no path or file, and no copy of the content, reworded, summarized or in part.",
		"When the blocked action is your reply itself, the Replying rule above applies instead: name only the rule, never the content.",
		"one line such as `Noted.` is enough",
		"Don't write the content to a file for anyone.",
		"If that status is blocked too, stop.",
	} {
		if !strings.Contains(string(protocol), want) {
			t.Errorf("protocol.md lacks %q", want)
		}
	}
	for _, want := range []string{
		"Never poll ListAgents or send \"are you done?\" messages; the one exception is the status line below.",
		"For an idle notice that comes without a reply, or a status saying a member's reply was blocked, never read that member's window, its transcript, or any file it names",
		"First check that member's `state` in `cadrei ls --json`.",
		"Otherwise you may send that member exactly one line, once for that task, with `notify_when_idle: true`: `No reply reached me. Send one short status.`",
		"If still no reply comes, tell the user the same way which member and window to check.",
		"Never ask that member, or any other, to send the content again in other words",
		"then send that member a pure subscription (`SendMessage` with the same `to`, `notify_when_idle: true`, and no message). That note comes from cadrei, not the user.",
	} {
		if !strings.Contains(string(skill), want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}
	guide, _ := os.ReadFile("docs/guide.md")
	for _, want := range []string{
		"When a member goes idle without replying at all, your orchestrator checks its state in `cadrei ls`, may ask it once for a short status, and otherwise tells you which window to open. It doesn't read that window itself either.",
		"its replies wait for your approval in your orchestrator's window, and its idle notices may not reach your orchestrator at all",
		"Its idle notices may not reach your orchestrator at all, so it may not hear that the member finished.",
	} {
		if !strings.Contains(string(guide), want) {
			t.Errorf("guide.md lacks %q", want)
		}
	}
	// Reading a silent member's window would bring blocked content back.
	if strings.Contains(string(skill), "capture-pane") {
		t.Error("SKILL.md tells the orchestrator to read a member's window")
	}
}

// The demo tape asks for the latest release on PATH: the newest version
// CHANGELOG.md has a heading for, so a release can't leave it behind.
func TestTheDemoTapeNamesTheLatestRelease(t *testing.T) {
	changelog, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	latest := regexp.MustCompile(`(?m)^## (\d+\.\d+\.\d+) - `).FindSubmatch(changelog)
	if latest == nil {
		t.Fatal("CHANGELOG.md has no release")
	}
	tape, err := os.ReadFile("docs/demo.tape")
	if err != nil {
		t.Fatal(err)
	}
	named := regexp.MustCompile(`cadrei (\d+\.\d+\.\d+) on PATH`).FindSubmatch(tape)
	if named == nil {
		t.Fatal("docs/demo.tape names no cadrei version")
	}
	if string(named[1]) != string(latest[1]) {
		t.Errorf("docs/demo.tape asks for cadrei %s, the latest release is %s", named[1], latest[1])
	}
}
