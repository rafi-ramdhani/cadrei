package backup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// T-S1: no tracked file holds a string the pre-push check would take for a
// credential. A test that needs one builds it while it runs. backup.go
// holds the patterns themselves.
func TestNoSecretShapedStringsInTheRepo(t *testing.T) {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not in a git checkout")
	}
	root := strings.TrimSpace(string(top))
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatal(err)
	}
	files := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	if len(files) < 10 {
		t.Fatalf("only %d tracked files", len(files))
	}
	for _, f := range files {
		if f == "internal/backup/backup.go" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue // a file deleted in the worktree
		}
		for _, tok := range tokens {
			if loc := tok.re.FindIndex(raw); loc != nil {
				line := 1 + strings.Count(string(raw[:loc[0]]), "\n")
				t.Errorf("%s:%d looks like %s", f, line, tok.what)
			}
		}
	}
}
