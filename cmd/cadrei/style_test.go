//go:build !windows

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestStreamsArePlainUnlessTheyCanShowStyle(t *testing.T) {
	var buf bytes.Buffer
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")
	if streamStyled(&buf) {
		t.Error("a pipe got styled output")
	}
	t.Setenv("FORCE_COLOR", "1")
	if !streamStyled(&buf) {
		t.Error("FORCE_COLOR=1 did not force styled output")
	}
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "1")
	if !streamStyled(&buf) {
		t.Error("CLICOLOR_FORCE=1 did not force styled output")
	}
	t.Setenv("CLICOLOR_FORCE", "")
	// A terminal is styled, and plain with NO_COLOR or a dumb TERM (audit
	// qual-tests-01: this ran only where a test had /dev/tty).
	terminalForTests = true
	t.Cleanup(func() { terminalForTests = false })
	t.Setenv("TERM", "xterm-256color")
	if !streamStyled(&buf) {
		t.Error("a terminal isn't styled")
	}
	t.Setenv("NO_COLOR", "1")
	if streamStyled(&buf) {
		t.Error("NO_COLOR=1 left a terminal styled")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if streamStyled(&buf) {
		t.Error("TERM=dumb left a terminal styled")
	}
}

func TestRenderDropsStylesWhenPlain(t *testing.T) {
	text := "Run " + cmd("cadrei project add") + " in " + dim("(~/a b)") + " " + bold("now")
	if got := render(text, false); got != "Run cadrei project add in (~/a b) now" {
		t.Errorf("plain: %q", got)
	}
	got := render(text, true)
	for _, want := range []string{"\x1b[36mcadrei project add\x1b[0m", "\x1b[2m(~/a b)\x1b[0m", "\x1b[1mnow\x1b[0m"} {
		if !strings.Contains(got, want) {
			t.Errorf("styled %q has no %q", got, want)
		}
	}
	if m := markDone.text(false) + markFail.text(false) + markBullet.text(false); m != "+x-" {
		t.Errorf("plain marks %q", m)
	}
}

func TestWrapHangsAndKeepsCommandsWhole(t *testing.T) {
	text := "Install it with " + cmd("brew install --cask claude-code") + ", or see https://claude.com/claude-code"
	lines := wrap(text, 40, "  ! ", "    ")
	for _, l := range lines {
		if visible(l) > 40 && !strings.Contains(l, "https://") {
			t.Errorf("line wider than 40: %q", render(l, false))
		}
	}
	joined := render(strings.Join(lines, "\n"), false)
	if !strings.Contains(joined, "brew install --cask claude-code") {
		t.Errorf("the command was broken:\n%s", joined)
	}
	if !strings.HasPrefix(lines[1], "    ") {
		t.Errorf("no hanging indent:\n%s", joined)
	}
	// A word wider than the line overflows instead of breaking.
	long := strings.Repeat("x", 60)
	if got := wrap("a "+long, 20, "", ""); len(got) != 2 || got[1] != long {
		t.Errorf("%q", got)
	}
}

func TestErrorsLeadWithTheMark(t *testing.T) {
	var out, errOut bytes.Buffer
	e := &env{stdout: &out, stderr: &errOut}
	if code := e.failWith("There's no command foo.", "See what there is with "+cmd("cadrei help")); code != 1 {
		t.Errorf("exit %d", code)
	}
	if got := errOut.String(); got != "cadrei: there's no command foo.\n  See what there is with cadrei help\n" {
		t.Errorf("%q", got)
	}
	for in, want := range map[string]string{"Claude Code isn't installed.": "Claude Code isn't installed.", "Bash(x) is not granted": "Bash(x) is not granted",
		"JSON is broken": "JSON is broken", "Git has no name": "git has no name", "~/x is gone": "~/x is gone"} {
		if got := plainStart(in); got != want {
			t.Errorf("plainStart(%q) = %q, want %q", in, got, want)
		}
	}
}

// Acceptance 106: the clay accent by terminal, never an OSC 11 query.
func TestTheAccent(t *testing.T) {
	for _, c := range []struct{ colorterm, term, fgbg, want string }{
		{"truecolor", "xterm-256color", "15;0", "\x1b[1;38;2;226;125;88m"},
		{"truecolor", "xterm-256color", "0;15", "\x1b[1;38;2;169;80;47m"},
		{"24bit", "xterm", "", "\x1b[1;38;2;194;96;62m"},
		{"", "xterm-256color", "15;0", "\x1b[1;38;5;173m"},
		{"", "xterm-256color", "0;15", "\x1b[1;38;5;130m"},
		{"", "xterm-256color", "", "\x1b[1;38;5;166m"},
		{"", "xterm", "15;0", "\x1b[1m"},
	} {
		t.Setenv("COLORTERM", c.colorterm)
		t.Setenv("TERM", c.term)
		t.Setenv("COLORFGBG", c.fgbg)
		if got := render(accent("main"), true); got != c.want+"main\x1b[0m" {
			t.Errorf("%+v: %q", c, got)
		}
		if got := render(accent("main"), false); got != "main" {
			t.Errorf("plain: %q", got)
		}
	}
}

// Audit qual-bugs-4: a styled span that wrapping splits is closed at the
// end of each of its lines and opened again on the next, so no style
// reaches past it.
func TestAWrappedSpanStaysOnItsLines(t *testing.T) {
	text := dim("It keeps going if you close this window. Ctrl-b s switch, Ctrl-b d leave") + " after"
	lines := wrap(text, 30, "    ", "    ")
	if len(lines) < 3 {
		t.Fatalf("lines: %q", lines)
	}
	for i, l := range lines {
		got := render(l, true)
		if strings.Count(got, "\x1b[2m") != strings.Count(got, "\x1b[0m") {
			t.Errorf("line %d: %q", i, got)
		}
		if i > 0 && strings.Contains(l, "Ctrl") && !strings.HasPrefix(got, "    \x1b[2m") {
			t.Errorf("line %d isn't dim again: %q", i, got)
		}
		if strings.Contains(render(l, false), "\x1b") {
			t.Errorf("plain line %d has codes", i)
		}
	}
	// A line with a span left open is reset at its end anyway.
	if got := render(string(shiftOut)+"dopen", true); got != "\x1b[2mopen\x1b[0m" {
		t.Errorf("open span: %q", got)
	}
}

// The first-run plan on a narrow styled terminal: every line that turns a
// style on turns it off.
func TestANarrowStyledPlanResetsEachLine(t *testing.T) {
	home := firstMachine(t)
	inRepo(t, home)
	columnsForTests = 50
	t.Cleanup(func() { columnsForTests = 0 })
	t.Setenv("FORCE_COLOR", "1")
	_, out, _ := callIn("q\n")
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "\x1b[") && !strings.HasSuffix(l, "\x1b[0m") && !strings.HasSuffix(strings.TrimRight(l, " "), "\x1b[0m") {
			if opened := strings.LastIndex(l, "\x1b[2m"); opened > strings.LastIndex(l, "\x1b[0m") {
				t.Errorf("a style left on: %q", l)
			}
		}
	}
}
