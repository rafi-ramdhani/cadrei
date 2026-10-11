// Command gen draws mascot/lead.pixels into mascot/truecolor.txt and
// mascot/256.txt, the art cadrei embeds. Run it with go generate from the
// repository's top folder after the brand changes lead.pixels.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rafi-ramdhani/cadrei/internal/mascot"
)

func main() {
	dir := "mascot"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	pixels, err := os.ReadFile(filepath.Join(dir, "lead.pixels"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for tier, name := range map[string]string{mascot.TrueColor: "truecolor.txt", mascot.Colors256: "256.txt"} {
		art, err := mascot.Render(pixels, tier)
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, name), []byte(art), 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
