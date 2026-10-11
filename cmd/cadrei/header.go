//go:build !windows

package main

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	cadrei "github.com/rafi-ramdhani/cadrei"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
)

// The compact header (7.12): the mascot's tiny figure, then three short
// lines beside it: the name bold and the rest dim. Without the figure (16
// colors or fewer, plain, or too narrow) the lines stay at the margin.

// header prints the compact header with lines (two or three), the first
// starting with the name.
func (e *env) header(lines ...string) {
	first := bold("cadrei")
	if rest := strings.TrimPrefix(lines[0], "cadrei"); strings.TrimSpace(rest) != "" {
		// The space after the name stays plain, as the brand's sample has it.
		word := strings.TrimLeft(rest, " ")
		first += rest[:len(rest)-len(word)] + dim(word)
	}
	marked := []string{first}
	longest := visible(lines[0])
	for _, l := range lines[1:] {
		marked = append(marked, dim(l))
		longest = max(longest, visible(l))
	}
	fig := figure(e.stdout)
	// The figure fits when the margin, it, two spaces and the longest line
	// stay inside W with a column to spare (so under 70 columns for the
	// first run's tagline, 7.12.1).
	if fig == nil || len(lines) > len(fig) || 2+artWidth(fig[0])+2+longest >= streamWidth(e.stdout) {
		for _, l := range marked {
			e.screen(l)
		}
		return
	}
	var b strings.Builder
	for i, row := range fig {
		b.WriteString("  " + row)
		if i < len(marked) {
			b.WriteString(spaces(2) + marked[i])
		}
		b.WriteString("\n")
	}
	fmt.Fprint(e.stdout, render(b.String(), true))
}

// productLine is the header's first line: the version, or a dev build.
func productLine() string {
	if framework.Kind(version) == "dev" {
		return "cadrei, development build"
	}
	return "cadrei v" + version
}

// figure is the mascot's rows (7.12), as the escape codes that draw them,
// for w: the truecolor figure or the 256-color one, and none when w isn't
// styled or has 16 colors or fewer.
func figure(w io.Writer) []string {
	if !streamStyled(w) {
		return nil
	}
	name := "mascot/truecolor.txt"
	switch colorTier() {
	case colors256:
		name = "mascot/256.txt"
	case colors16:
		return nil
	}
	raw, err := cadrei.Assets.ReadFile(name)
	if err != nil {
		return nil
	}
	rows := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	// Every row is the figure's width, or the text beside it would shift.
	for _, l := range rows {
		if artWidth(l) != artWidth(rows[0]) {
			return nil
		}
	}
	return rows
}

// artWidth is how many columns a row of art takes: its characters, each
// one column (a half block is one, T-B1), less its escape codes.
func artWidth(l string) int {
	n := 0
	for i := 0; i < len(l); {
		if l[i] == '\x1b' {
			// A CSI code runs to its final letter.
			for i++; i < len(l) && (l[i] < '@' || l[i] > '~' || l[i] == '['); i++ {
			}
			i++
			continue
		}
		_, size := utf8.DecodeRuneInString(l[i:])
		n++
		i += size
	}
	return n
}
