package cadreis

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/rafi-ramdhani/cadrei/internal/conf"
)

// Format is the crew layout this cadrei reads and writes. A later cadrei
// that changes how a crew is laid out (a tracked file moved, a new key a
// file must have) writes the new number as FORMAT in the crew's tracked
// cadrei.conf, in the same commit as its migration. A crew travels
// through git between machines on different versions, so an older
// cadrei that finds a newer format changes nothing in it and says to
// upgrade. Format 1 writes no FORMAT: it's what a missing one means.
const Format = 1

// FormatOf is a crew's format: FORMAT in its tracked cadrei.conf, never
// the machine's own .claude/local/cadrei.conf. Missing means 1; a value
// that isn't a whole number from 1 up is one this cadrei doesn't know,
// returned as 0 with the text as it is.
func FormatOf(dir string) (int, string) {
	raw, err := os.ReadFile(filepath.Join(dir, "cadrei.conf"))
	if err != nil {
		return 1, ""
	}
	values, _ := conf.Parse(string(raw))
	v, set := values["FORMAT"]
	if !set {
		return 1, ""
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, v
	}
	return n, v
}

// NewerFormat says why this cadrei must not change the crew at dir, or
// start its members: a newer cadrei changed its layout. "" when it may.
func NewerFormat(dir string) string {
	n, text := FormatOf(dir)
	if n == Format || (n > 0 && n < Format) {
		return ""
	}
	return "The crew " + filepath.Base(dir) + " was changed by a newer cadrei (format " + text + "), so this one won't change it or start its members."
}

// UpgradeFix is how to get a cadrei that knows a newer format.
const UpgradeFix = "Upgrade cadrei: brew upgrade cadrei, or run install.sh again. Until then, ls, attach, stop, --check and uninstall still work."
