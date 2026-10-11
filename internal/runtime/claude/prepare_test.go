package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/runtime/claude/settings"
)

// A broken settings file (one shell write does it) must never leave a
// member without the deny rules: it gets a deny-only copy.
func TestPrepareWithABrokenFileStillDenies(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	cadrei := filepath.Join(home, ".cadrei", "work")
	os.MkdirAll(filepath.Join(cadrei, "members"), 0o755)
	places := runtime.Places{Root: home + "/.cadrei", Cadreis: []string{cadrei, "/Docs/outside"}}
	p := permissions{}
	if got := p.Prepare(cadrei, places); got.Grants == "" || len(got.Warnings) != 0 {
		t.Fatalf("a new file: %+v", got)
	}
	file := p.GrantsFile(cadrei)
	f, _ := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("}")
	f.Close()
	got := p.Prepare(cadrei, places)
	if got.Grants == "" {
		t.Fatal("a broken file gave no copy: the member would start without any deny rule")
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "nothing extra allowed, only cadrei's limits") {
		t.Errorf("warnings %q", got.Warnings)
	}
	raw, _ := os.ReadFile(got.Grants)
	text := string(raw)
	for _, d := range append(append([]string{}, settings.FixedDeny...), "Edit(/"+cadrei+"/members/**)", "Edit(//Docs/outside/cadrei.conf)", "Edit(/"+home+"/.cadrei/config/**)") {
		if !strings.Contains(text, `"`+d+`"`) {
			t.Errorf("the deny-only copy lacks %s", d)
		}
	}
	if !strings.Contains(text, settings.FixedSoft) || !strings.Contains(text, settings.CadreiSoft) {
		t.Error("the deny-only copy lacks a soft_deny line")
	}
	if !strings.Contains(text, `"allow": []`) {
		t.Errorf("the deny-only copy grants something:\n%s", text)
	}
}
