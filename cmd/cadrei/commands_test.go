//go:build !windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafi-ramdhani/cadrei/internal/testguard"
)

func call(args ...string) (code int, out, errOut string) { return callIn("", args...) }

// callIn runs cadrei with stdin as its input.
func callIn(stdin string, args ...string) (code int, out, errOut string) {
	testguard.MustBeSafe()
	var o, e bytes.Buffer
	code = run(args, strings.NewReader(stdin), &o, &e)
	return code, o.String(), e.String()
}

// flat joins wrapped lines, so a check reads a message whole however the
// width broke it.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestHelpListsExactlyTheVisibleCommands(t *testing.T) {
	code, out, _ := call("help")
	if code != 0 {
		t.Fatalf("help exited %d", code)
	}
	var got []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "  cadrei") {
			got = append(got, strings.Fields(l)[1])
		}
	}
	// Plain cadrei's line has no name, so its second field is its summary.
	want := []string{"open", "ls", "attach", "stop", "help", "--version", "uninstall"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("help lists %v, want %v", got, want)
	}
	if !strings.Contains(out, "\nOptions for cadrei: --tmux, --no-tmux, --detach, --fresh\n") {
		t.Error("help lacks plain cadrei's options")
	}
	if !strings.Contains(out, "cadrei help advanced") {
		t.Error("help does not point to cadrei help advanced")
	}
}

func TestHelpAdvancedListsEveryCommand(t *testing.T) {
	_, out, _ := call("help", "advanced")
	// Every command, the everyday ones with their options too.
	for _, c := range commands {
		if c.hidden {
			continue
		}
		words := strings.TrimSpace(c.name + " " + c.usage)
		if c.shown != "" {
			words = c.shown
		}
		if !strings.Contains(out, "cadrei "+words) {
			t.Errorf("help advanced lacks cadrei %s", words)
		}
	}
	// Acceptance 127.
	if !strings.Contains(out, "\n  cadrei ls [--all] [--json [--watch] [--json-version 1]]") {
		t.Errorf("help advanced's ls line:\n%s", out)
	}
	for _, o := range oldNames {
		if strings.Contains(out, "cadrei "+o.name+" ") {
			t.Errorf("help advanced lists the old name cadrei %s", o.name)
		}
	}
	for _, c := range commands {
		if c.hidden && strings.Contains(out, "cadrei "+c.name) {
			t.Errorf("help advanced shows the hidden cadrei %s", c.name)
		}
	}
}

// Old 0.1.x names are not aliases: each says what to do now and exits 1.
func TestOldNamesPointToTheNewWay(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"down", "dev"}, "cadrei: cadrei down is gone since 0.2.0. Use cadrei stop"},
		{[]string{"down", "--all"}, "cadrei down is gone since 0.2.0. Use cadrei stop"},
		{[]string{"projects"}, "Use cadrei ls"},
		{[]string{"path", "app"}, "Use cadrei project path"},
		{[]string{"sync"}, "Use cadrei project sync"},
		{[]string{"add", "project", "app", "me/app"}, "Ask your orchestrator, or use cadrei project add"},
		{[]string{"add", "team", "ops"}, "Ask your orchestrator"},
		{[]string{"add", "persona", "ops/sre"}, "Ask your orchestrator"},
		{[]string{"version"}, "Use cadrei --version"},
		{[]string{"trust", "app"}, "Use cadrei project trust"},
		{[]string{"update", "--check"}, "brew upgrade cadrei"},
	} {
		code, out, errOut := call(tc.args...)
		if code != 1 || out != "" || !strings.Contains(flat(errOut), tc.want) {
			t.Errorf("%v: exit %d, %q %q; want one line naming %q", tc.args, code, out, errOut, tc.want)
		}
	}
	for _, o := range oldNames {
		if _, _, err := lookup(strings.Fields(o.name)); err == nil {
			t.Errorf("old name cadrei %s runs a command", o.name)
		}
	}
}

func TestCommandsStillNamedAsBefore(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
		rest string
	}{
		{[]string{"up", "dev"}, "up", "dev"},
		{[]string{"init", "x"}, "init", "x"},
		{[]string{"use", "x"}, "use", "x"},
		{[]string{"attach", "dev"}, "attach", "dev"},
		{[]string{"ls", "--json"}, "ls --json", ""},
		{[]string{"ls", "--all", "--json"}, "ls", "--all --json"},
		{[]string{"-h"}, "help", ""},
		{[]string{"--help", "advanced"}, "help", "advanced"},
		{[]string{"-v"}, "--version", ""},
	} {
		c, rest, err := lookup(tc.args)
		if err != nil {
			t.Errorf("%v: %v", tc.args, err)
			continue
		}
		if c.name != tc.want || strings.Join(rest, " ") != tc.rest {
			t.Errorf("%v runs %q with %q, want %q with %q", tc.args, c.name, rest, tc.want, tc.rest)
		}
	}
}

// Commands of features that were cut are gone, not stubs.
func TestCutCommandsAreUnknown(t *testing.T) {
	for _, args := range [][]string{
		{"project", "export", "a", "/x"}, {"project", "move", "a", "/x"},
		{"project", "restore", "a"}, {"project", "relink", "a", "/x"}, {"cadreis", "add", "/x"}, {"cadreis", "remove", "x"},
		{"team", "add", "ops"}, {"member", "add", "ops/sre"},
		{"which"}, {"ctx"}, {"start"},
	} {
		if code, _, errOut := call(args...); code != 1 || !strings.Contains(errOut, "there's no command") {
			t.Errorf("%v: exit %d, %q", args, code, errOut)
		}
	}
	if code, _, errOut := call("stop", "--with-orchestrator"); code != 1 || !strings.Contains(errOut, "use cadrei stop [team[/role]]") {
		t.Errorf("stop --with-orchestrator: exit %d, %q", code, errOut)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errOut := call("bogus")
	if code != 1 || errOut != "cadrei: there's no command bogus.\n  See what there is with cadrei help\n" {
		t.Errorf("exit %d, %q", code, errOut)
	}
}

// 7.2: a near miss gets the command it was likely meant to be.
func TestDidYouMean(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"sl"}, "cadrei: there's no command sl. Did you mean cadrei ls?\n"},
		{[]string{"atach", "dev"}, "cadrei: there's no command atach. Did you mean cadrei attach?\n"},
		{[]string{"foo"}, "cadrei: there's no command foo.\n  See what there is with cadrei help\n"},
		{[]string{"project", "ad", "x"}, "cadrei: there's no command project ad. Did you mean cadrei project add?\n"},
		{[]string{"hook", "stat"}, ""},
	} {
		code, _, errOut := call(tc.args...)
		if code != 1 || (tc.want != "" && errOut != tc.want) || strings.Contains(errOut, "hook state") {
			t.Errorf("%v: exit %d, %q", tc.args, code, errOut)
		}
	}
	// cadrei project alone: the Projects group of help advanced.
	code, out, _ := call("project")
	if code != 0 || !strings.HasPrefix(out, "Projects\n  cadrei project add ") || strings.Contains(out, "cadrei allow") {
		t.Errorf("project: %d %q", code, out)
	}
	// 7.2's other errors.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"up"}, "cadrei: which team? For example: cadrei up dev my-app\n  Or ask your orchestrator to start one.\n"},
		{[]string{"project", "add"}, "cadrei: add which project? For example:\n  cadrei project add blog you/blog\n  cadrei project add app --path ~/code/app\n"},
	} {
		if code, _, errOut := call(tc.args...); code != 1 || errOut != tc.want {
			t.Errorf("%v: exit %d, %q", tc.args, code, errOut)
		}
	}
}

func TestVersion(t *testing.T) {
	for _, a := range []string{"--version", "-v"} {
		code, out, _ := call(a)
		if code != 0 || out != "cadrei, a development build\n" {
			t.Errorf("%s: exit %d, %q", a, code, out)
		}
	}
}

func TestEveryCommandHasARunner(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range commands {
		if c.run == nil {
			t.Errorf("cadrei %s has no runner", c.name)
		}
		if seen[c.name] {
			t.Errorf("cadrei %s is listed twice", c.name)
		}
		seen[c.name] = true
		if !c.hidden && c.summary == "" {
			t.Errorf("cadrei %s has no summary", c.name)
		}
	}
}

func TestUnknownSubcommandIsNamed(t *testing.T) {
	_, _, err := lookup([]string{"project", "bogus", "x"})
	if err == nil || err.Error() != "There's no command project bogus. See what there is with cadrei help" {
		t.Errorf("project bogus: %v", err)
	}
}

func TestVersionTakesNoArguments(t *testing.T) {
	if code, _, _ := call("--version", "x"); code != 1 {
		t.Errorf("--version x exited %d", code)
	}
}

// Acceptance 72: --version for each way cadrei is installed.
func TestVersionLines(t *testing.T) {
	for _, c := range []struct{ kind, v, want string }{
		{"homebrew", "0.2.1", "cadrei 0.2.1, installed with Homebrew"},
		{"release", "0.2.1", "cadrei 0.2.1"},
		{"dev", "dev", "cadrei, a development build"},
		{"dev", "0.2.1-3-gabc", "cadrei 0.2.1-3-gabc, a development build"},
	} {
		if got := versionLine(c.kind, c.v); got != c.want {
			t.Errorf("%s %s: %q", c.kind, c.v, got)
		}
	}
}

// 8.3.3: help's descriptions wrap at W under their own column, even in a
// wide terminal.
func TestHelpWrapsUnderItsColumn(t *testing.T) {
	columnsForTests = 100
	t.Cleanup(func() { columnsForTests = 0 })
	for _, args := range [][]string{{"help", "advanced"}, {"project"}} {
		_, out, _ := call(args...)
		for _, l := range strings.Split(out, "\n") {
			usage := strings.HasPrefix(l, "  cadrei ") && !strings.Contains(strings.TrimSpace(l[2:]), "   ")
			if len([]rune(l)) > 80 && !usage {
				t.Errorf("%v: %d wide: %q", args, len([]rune(l)), l)
			}
			if strings.HasPrefix(l, "   ") && !strings.HasPrefix(l, spaces(0)+strings.Repeat(" ", 47)) {
				t.Errorf("%v: not under its column: %q", args, l)
			}
		}
	}
}

// Audit qual-tests-13, acceptance 71: cadrei help is 7.4's screen, word
// for word.
func TestHelpIsTheSpecs(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join(repoRoot, "docs", "specs", "0.2.1-onboarding-polish.md"))
	if err != nil {
		t.Skip("no spec here")
	}
	s := string(spec)
	start := strings.Index(s, "### 7.4 Help")
	open := strings.Index(s[start:], "```text\n")
	if start < 0 || open < 0 {
		t.Fatal("no 7.4 screen in the spec")
	}
	want := s[start+open+len("```text\n"):]
	want = want[:strings.Index(want, "```")]
	sandbox(t)
	if got := must(t, "help"); got != want {
		t.Errorf("cadrei help:\n%s\nwant:\n%s", got, want)
	}
}
