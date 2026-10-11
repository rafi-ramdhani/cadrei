//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

// Output style: each stream is styled (colors, Unicode marks) when it is a
// terminal that can show them, and plain (ASCII, no escape codes) for
// NO_COLOR, pipes and dumb terminals. Text is built once with markers for
// its styles and its unbreakable spaces, then rendered for the stream.

// Style markers inside text: shiftOut and a style letter start a styled
// span, shiftIn ends it. keep is a space that wrapping never breaks at,
// for commands and paths.
const (
	shiftOut = '\x0e'
	shiftIn  = '\x0f'
	keep     = '\x1f'
)

// ANSI codes of the five styles, from the terminal's 16 base colors.
var ansi = map[rune]string{'b': "\x1b[1m", 'd': "\x1b[2m", 'g': "\x1b[32m", 'y': "\x1b[33m", 'r': "\x1b[31m", 'c': "\x1b[36m"}

func span(style rune, s string) string {
	if s == "" {
		return ""
	}
	return string(shiftOut) + string(style) + s + string(shiftIn)
}

func bold(s string) string { return span('b', s) }

// accent is the crew's name where it's "yours" (2.3.1): bold clay, or
// bold alone on 16 colors.
func accent(s string) string { return span('a', s) }

// accentCode is the escape code of the clay accent for this terminal: by
// COLORTERM (truecolor), TERM (256 colors), else bold only; dark or light
// from COLORFGBG alone. Cadrei never asks the terminal (no OSC 11).
func accentCode() string {
	bg := background()
	switch colorTier() {
	case trueColor:
		rgb := map[string]string{"dark": "226;125;88", "light": "169;80;47"}[bg]
		if rgb == "" {
			rgb = "194;96;62"
		}
		return "\x1b[1;38;2;" + rgb + "m"
	case colors256:
		n := map[string]string{"dark": "173", "light": "130"}[bg]
		if n == "" {
			n = "166"
		}
		return "\x1b[1;38;5;" + n + "m"
	}
	return "\x1b[1m"
}

// The color tiers of 2.3.1: what the terminal says it can draw.
const (
	colors16 = iota
	colors256
	trueColor
)

// colorTier is the terminal's tier: COLORTERM says truecolor, TERM says
// 256 colors, else 16. Whether output is styled at all is streamStyled's.
func colorTier() int {
	if ct := os.Getenv("COLORTERM"); ct == "truecolor" || ct == "24bit" {
		return trueColor
	}
	if strings.Contains(os.Getenv("TERM"), "256color") {
		return colors256
	}
	return colors16
}

// background is "dark" or "light" from COLORFGBG's last field, or "".
func background() string {
	v := os.Getenv("COLORFGBG")
	if v == "" {
		return ""
	}
	f := v[strings.LastIndex(v, ";")+1:]
	switch f {
	case "0", "1", "2", "3", "4", "5", "6", "8":
		return "dark"
	case "7", "9", "10", "11", "12", "13", "14", "15":
		return "light"
	}
	return ""
}
func dim(s string) string { return span('d', s) }

// cmd is a command the user can type: cyan, and never broken by wrapping.
func cmd(s string) string { return span('c', unbroken(s)) }

// unbroken keeps s on one line when text wraps.
func unbroken(s string) string { return strings.ReplaceAll(s, " ", string(keep)) }

// spaces is n spaces that wrapping keeps as they are, for columns.
func spaces(n int) string { return strings.Repeat(string(keep), n) }

// Marks, each with its plain form and style.
type mark struct {
	styled, plain string
	style         rune // 0 for the terminal's own color
}

var (
	markDone     = mark{"✓", "+", 'g'}
	markWarn     = mark{"!", "!", 'y'}
	markFail     = mark{"✗", "x", 'r'}
	markBullet   = mark{"·", "-", 'd'}
	markWorking  = mark{"●", "*", 'g'}
	markIdle     = mark{"○", "o", 0}
	markWaiting  = mark{"!", "!", 'y'}
	markStarting = mark{"◌", ".", 'd'}
	markUnknown  = mark{"?", "?", 'd'}
	markStopped  = mark{"-", "-", 'd'}
)

func (m mark) text(styled bool) string {
	if !styled {
		return m.plain
	}
	if m.style == 0 {
		return m.styled
	}
	return span(m.style, m.styled)
}

// columnsForTests, when set, is the terminal width tests ask for.
var columnsForTests int

// streamStyled reports whether w gets styled output: FORCE_COLOR or
// CLICOLOR_FORCE force it; else w must be a terminal, TERM set and not
// dumb, and NO_COLOR unset or empty.
func streamStyled(w io.Writer) bool {
	if os.Getenv("FORCE_COLOR") == "1" || os.Getenv("CLICOLOR_FORCE") == "1" {
		return true
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if t := os.Getenv("TERM"); t == "" || t == "dumb" {
		return false
	}
	return isTerminal(w)
}

// terminalForTests, when set, makes tests' output writers count as a
// terminal: for the styled and redrawn screens only a terminal gets.
var terminalForTests bool

// isTerminal reports whether w is a terminal.
func isTerminal(w io.Writer) bool {
	if terminalForTests {
		return true
	}
	f, ok := unwrapWriter(w).(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// streamWidth is the width text wraps at: the terminal's, at most 80, and
// 80 when it is not known.
func streamWidth(w io.Writer) int {
	n := 80
	if columnsForTests > 0 {
		n = columnsForTests
	} else if f, ok := unwrapWriter(w).(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if c, _, err := term.GetSize(int(f.Fd())); err == nil && c > 0 {
			n = c
		}
	}
	if n > 80 {
		n = 80
	}
	return n
}

// render turns marked text into what a stream shows.
func render(s string, styled bool) string {
	if !strings.ContainsAny(s, string([]rune{shiftOut, shiftIn, keep})) {
		return s
	}
	var b strings.Builder
	open := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case shiftOut:
			if i+1 < len(s) {
				if styled && s[i+1] == 'a' {
					b.WriteString(accentCode())
				} else if styled {
					b.WriteString(ansi[rune(s[i+1])])
				}
				open = true
				i++
			}
		case shiftIn:
			if styled && open {
				b.WriteString("\x1b[0m")
			}
			open = false
		case keep:
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	// A span left open at the end ends here, so its style never reaches
	// the next line.
	if styled && open {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// visible is how many columns marked text takes.
func visible(s string) int {
	n := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == shiftOut:
			size++ // the style letter
		case r == shiftIn:
		default:
			n++
		}
		i += size
	}
	return n
}

// wrap breaks marked text into lines of at most width columns, at spaces
// only: the first line starts with first, the others with rest (a hanging
// indent). A word wider than the line stays whole and overflows. A styled
// span that a break splits is closed at the end of its line and opened
// again after the next line's prefix, so each line renders on its own.
func wrap(text string, width int, first, rest string) []string {
	var lines []string
	var style byte // the span open where the line ends, or 0
	end := func(line string) {
		if style != 0 {
			line += string(shiftIn)
		}
		lines = append(lines, line)
	}
	start := func(prefix string) string {
		if style != 0 {
			return prefix + string([]byte{shiftOut, style})
		}
		return prefix
	}
	for _, para := range strings.Split(text, "\n") {
		prefix := first
		if len(lines) > 0 {
			prefix = rest
		}
		line, n := start(prefix), visible(prefix)
		empty := true
		for _, w := range strings.Fields(para) {
			wn := visible(w)
			if !empty && n+1+wn > width {
				end(line)
				line, n, empty = start(rest), visible(rest), true
			}
			if !empty {
				line += " "
				n++
			}
			line += w
			n += wn
			empty = false
			style = openAfter(w, style)
		}
		end(strings.TrimRight(line, " "))
	}
	return lines
}

// openAfter is the span open after marked text w, given the one open
// before it (0 for none).
func openAfter(w string, style byte) byte {
	for i := 0; i < len(w); i++ {
		switch w[i] {
		case shiftOut:
			if i+1 < len(w) {
				style = w[i+1]
				i++
			}
		case shiftIn:
			style = 0
		}
	}
	return style
}

// write prints marked text on w, wrapped with first and rest as prefixes.
func write(w io.Writer, text, first, rest string) {
	styled := streamStyled(w)
	for _, l := range wrap(text, streamWidth(w), first, rest) {
		fmt.Fprintln(w, render(l, styled))
	}
}

// show prints a line on stdout: marked text, wrapped, with indent before
// every line.
func (e *env) show(indent, text string) { write(e.stdout, text, indent, indent) }

// note prints a marked line on stdout: the mark, then the text with a
// hanging indent under it.
func (e *env) note(indent string, m mark, text string) {
	styled := streamStyled(e.stdout)
	write(e.stdout, text, indent+m.text(styled)+" ", indent+strings.Repeat(" ", visible(m.plain)+1))
}

// warn prints a heads-up on stderr: "! <what>", then each fix line
// indented under it.
func (e *env) warn(indent, what string, fix ...string) {
	styled := streamStyled(e.stderr)
	write(e.stderr, what, indent+markWarn.text(styled)+" ", indent+"  ")
	for _, f := range fix {
		write(e.stderr, f, indent+"  ", indent+"  ")
	}
}

// failWith prints an error on stderr, "✗ <what>" (plain: "cadrei: <what>")
// at column 0, then each fix line indented 2 spaces, and returns exit
// code 1.
func (e *env) failWith(what string, fix ...string) int {
	styled := streamStyled(e.stderr)
	first, what := markFail.text(styled)+" ", styledStart(what)
	if !styled {
		first, what = "cadrei: ", plainStart(what)
	}
	write(e.stderr, what, first, "  ")
	for _, f := range fix {
		write(e.stderr, f, "  ", "  ")
	}
	return 1
}

// lowerNames are first words that stay lowercase at the start of a
// sentence: program names.
var lowerNames = []string{"cadrei", "tmux", "git", "brew", "cadrei's"}

// styledStart starts an error with a capital, as a sentence does after
// the ✗ mark, unless its first word is a program's name or anything but a
// plain lowercase word (a path, a command, a rule).
func styledStart(s string) string {
	word, _, _ := strings.Cut(s, " ")
	for _, n := range lowerNames {
		if word == n {
			return s
		}
	}
	for _, r := range word {
		if !unicode.IsLower(r) && r != '\'' {
			return s
		}
	}
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// properStarts are first words that keep their capital after "cadrei: ".
// A name of two capitalized words, such as a product's, keeps it too.
var properStarts = []string{"Homebrew", "I"}

// plainStart writes a sentence's first letter in lowercase, as it reads
// after "cadrei: ", unless the first word is a name.
func plainStart(s string) string {
	word, after, _ := strings.Cut(s, " ")
	if r, _ := utf8.DecodeRuneInString(after); unicode.IsUpper(r) {
		return s
	}
	for _, p := range properStarts {
		if word == p || strings.HasPrefix(word, p+"'") {
			return s
		}
	}
	// Only a plain word: a capital, then lowercase letters (an
	// apostrophe allowed), so Bash(...), JSON and paths keep theirs.
	for i, r := range word {
		switch {
		case i == 0 && !unicode.IsUpper(r):
			return s
		case i > 0 && !unicode.IsLower(r) && r != '\'':
			return s
		}
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[size:]
}

// notes prints heads-up lines from cadrei's parts, each with the ! mark.
func (e *env) notes(list []string) {
	for _, n := range list {
		e.note("", markWarn, sentence(n))
	}
}

// printable is s with every character that could break a line or take
// over a screen shown as "?": control characters (newlines and the
// terminal's escape among them, and the style markers above), the marks
// that turn text around, and the line and paragraph separators. Names
// come from file and folder names and files anyone can write (a cloned
// crew, a hand edit), and land on the screen and in the orchestrator's
// context, where a line break could pass for cadrei's own.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r), r == '\u2028', r == '\u2029', r == '\u061c', r == '\u200e', r == '\u200f',
			r >= '\u202a' && r <= '\u202e', r >= '\u2066' && r <= '\u2069':
			return '?'
		}
		return r
	}, s)
}
