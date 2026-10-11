package mascot

import (
	"os"
	"strings"
	"testing"
)

// The art cadrei embeds is lead.pixels drawn by Render (audit
// qual-maint-9): after the brand changes lead.pixels, go generate draws it
// again, and this test says when that was forgotten.
func TestTheArtIsLeadPixels(t *testing.T) {
	pixels, err := os.ReadFile("../../mascot/lead.pixels")
	if err != nil {
		t.Fatal(err)
	}
	for tier, file := range map[string]string{TrueColor: "truecolor.txt", Colors256: "256.txt"} {
		want, err := Render(pixels, tier)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile("../../mascot/" + file)
		if string(got) != want {
			t.Errorf("mascot/%s isn't lead.pixels drawn for %s: run go generate", file, tier)
		}
	}
}

func TestRenderRefusesWhatItCantDraw(t *testing.T) {
	for name, pixels := range map[string]string{
		"odd rows":       "# C = #C2603E  256:173\nCC\n",
		"unknown letter": "# C = #C2603E  256:173\nCX\nCC\n",
		"uneven rows":    "# C = #C2603E  256:173\nCC\nC\n",
	} {
		if _, err := Render([]byte(pixels), TrueColor); err == nil {
			t.Errorf("%s: drawn", name)
		}
	}
	art, _ := Render([]byte("# C = #C2603E  256:173\nC.\n.C\n"), Colors256)
	if !strings.HasPrefix(art, "\x1b[49;38;5;173m▀\x1b[49;38;5;173m▄\x1b[0m\n") {
		t.Errorf("%q", art)
	}
}
