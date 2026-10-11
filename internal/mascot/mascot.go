// Package mascot draws the brand's lead.pixels as the half-block art the
// header shows: two pixel rows per terminal row, the upper one in ▀'s
// color and the lower one behind it, transparent pixels left to the
// terminal's background.
package mascot

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// Tiers are the color tiers the art is drawn for.
const (
	TrueColor = "truecolor"
	Colors256 = "256"
)

// color is one palette letter: its truecolor and 256-color codes.
type color struct{ rgb, xterm string }

// paletteLine is "# C = #C2603E  256:173 ...".
var paletteLine = regexp.MustCompile(`^#\s*(\S)\s*=\s*#([0-9A-Fa-f]{6})\s+256:(\d+)`)

// Render draws pixels (the lead.pixels format: a palette in comments, then
// one character per pixel, "." transparent) for a tier. Every row comes
// out the full width, ending in a reset.
func Render(pixels []byte, tier string) (string, error) {
	palette := map[byte]color{}
	var rows []string
	sc := bufio.NewScanner(bytes.NewReader(pixels))
	for sc.Scan() {
		line := sc.Text()
		if m := paletteLine.FindStringSubmatch(line); m != nil {
			var r, g, b int
			fmt.Sscanf(m[2], "%02x%02x%02x", &r, &g, &b)
			palette[m[1][0]] = color{rgb: fmt.Sprintf("2;%d;%d;%d", r, g, b), xterm: "5;" + m[3]}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rows = append(rows, line)
	}
	if len(rows) == 0 || len(rows)%2 != 0 {
		return "", fmt.Errorf("lead.pixels needs an even number of pixel rows, not %d", len(rows))
	}
	code := func(c byte) (string, error) {
		p, ok := palette[c]
		if !ok {
			return "", fmt.Errorf("lead.pixels uses %q, which its palette doesn't name", c)
		}
		if tier == Colors256 {
			return p.xterm, nil
		}
		return p.rgb, nil
	}
	var b strings.Builder
	for i := 0; i < len(rows); i += 2 {
		top, bottom := rows[i], rows[i+1]
		if len(top) != len(bottom) {
			return "", fmt.Errorf("lead.pixels rows %d and %d differ in width", i+1, i+2)
		}
		for x := 0; x < len(top); x++ {
			t, u := top[x], bottom[x]
			switch {
			case t == '.' && u == '.':
				b.WriteString("\x1b[0m ")
			case u == '.':
				fg, err := code(t)
				if err != nil {
					return "", err
				}
				b.WriteString("\x1b[49;38;" + fg + "m▀")
			case t == '.':
				fg, err := code(u)
				if err != nil {
					return "", err
				}
				b.WriteString("\x1b[49;38;" + fg + "m▄")
			default:
				fg, err := code(t)
				if err != nil {
					return "", err
				}
				bg, err := code(u)
				if err != nil {
					return "", err
				}
				b.WriteString("\x1b[38;" + fg + ";48;" + bg + "m▀")
			}
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String(), nil
}
