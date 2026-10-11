package settings

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/jsonx"
)

func newFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "member-settings.json")
	if err := Create(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCreateMakesAValidFileWithNoGrants(t *testing.T) {
	p := newFile(t)
	if _, err := Load(p); err != nil {
		t.Fatalf("a new file does not validate: %v", err)
	}
	g, err := OpenGrants(p)
	if err != nil || len(g.List()) != 0 {
		t.Errorf("a new file has grants: %v %v", g.List(), err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "Write(") {
		t.Error("a new file holds a Write rule, which Claude Code ignores")
	}
}

const valid = `"permissions": {"allow": [], "deny": ["Edit(//**/.claude/member-settings.json)", "Bash(cadrei allow:*)"]},
"autoMode": {"allow": ["$defaults"], "soft_deny": ["$defaults", "` + FixedSoft + `"]}`

func TestLoadRefusals(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`[]`, "it is not a JSON object"},
		{`{`, "it is not valid JSON"},
		{`{` + valid + `, "hooks": {}}`, "it has the key hooks; only permissions and autoMode are allowed"},
		{`{` + valid + `, "env": {"X": "1"}}`, "it has the key env"},
		{`{"permissions": [], "autoMode": {}}`, "permissions is not an object"},
		{`{"permissions": {"allow": [], "defaultMode": "bypassPermissions"}}`, "it has permissions.defaultMode; only allow and deny are allowed"},
		{`{"autoMode": {"environment": []}}`, "it has autoMode.environment; only allow and soft_deny are allowed"},
		{`{"permissions": {"allow": [1]}}`, "permissions.allow is not a list of strings"},
		{`{"autoMode": {"allow": ["x"]}}`, `autoMode.allow lacks "$defaults", which would replace the built-in rules`},
		{`{"permissions": {"allow": [], "deny": []}, "autoMode": {"allow": ["$defaults"], "soft_deny": ["$defaults"]}}`, "permissions.deny lacks the entries that protect the file"},
		{`{"permissions": {"allow": [], "deny": ["Edit(//**/.claude/member-settings.json)", "Bash(cadrei allow:*)"]}, "autoMode": {"allow": ["$defaults"], "soft_deny": ["$defaults"]}}`, "autoMode.soft_deny lacks the entry that protects the file"},
		{`{"permissions": {"defaultMode": "bypassPermissions"}, ` + valid + `}`, "it has the key permissions twice"},
		{`{"hooks": {}, "hooks": {}}`, "it has the key hooks twice"},
	} {
		p := filepath.Join(t.TempDir(), "s.json")
		os.WriteFile(p, []byte(tc.body), 0o644)
		_, err := Load(p)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.body, err, tc.want)
		}
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil || err.Error() != "it cannot be read" {
		t.Errorf("missing file: %v", err)
	}
}

func TestExport(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(p, []byte(`{"permissions": {"allow": ["Bash(npm test)"], "deny": ["Edit(//**/.claude/member-settings.json)", "Bash(cadrei allow:*)", "Write(//**/.claude/member-settings.json)"]},
"autoMode": {"allow": ["$defaults", "Running tests is expected"], "soft_deny": ["$defaults", "`+FixedSoft+`"]}}`), 0o644)
	dir := filepath.Join(t.TempDir(), "build")
	out, err := Export(p, dir, Places{Root: "/h/.cadrei", Cadreis: []string{"/h/.cadrei/work", "/Docs/old cadre [1]"}})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(out) != dir || !strings.HasPrefix(filepath.Base(out), "member-settings.") {
		t.Errorf("copy at %s", out)
	}
	if st, _ := os.Stat(out); st.Mode().Perm() != 0o400 {
		t.Errorf("copy mode %v", st.Mode().Perm())
	}
	raw, _ := os.ReadFile(out)
	root, _ := jsonx.Parse(raw)
	deny, _ := texts(root.Get("permissions").Get("deny"))
	for _, d := range FixedDeny {
		if !contains(deny, d) {
			t.Errorf("the copy lacks %s", d)
		}
	}
	for _, d := range []string{"Edit(//**/.cadrei/config/**)", "Edit(//**/.cadrei/*/members/**)", "Edit(//**/.cadrei/*/.git/**)"} {
		if !contains(deny, d) {
			t.Errorf("the copy lacks the N.7 entry %s", d)
		}
	}
	// Review of fix round 6 (must-fix 3): no member moves, deletes or
	// brings back every crew.
	for _, d := range []string{"Bash(cadrei uninstall:*)", "Bash(cadrei restore:*)"} {
		if !contains(deny, d) {
			t.Errorf("the copy lacks %s", d)
		}
	}
	if soft, _ := texts(root.Get("autoMode").Get("soft_deny")); !contains(soft, CadreiSoft) || !contains(soft, FixedSoft) {
		t.Errorf("the copy's soft_deny: %q", soft)
	}
	if strings.Contains(string(raw), "teams") {
		t.Error("a deny entry covers team folders, where members work")
	}
	// The same rules with physical paths, for outside cadreis and a ~/.cadrei
	// reached through a symlink, with glob characters escaped.
	for _, d := range []string{"Edit(//h/.cadrei/config/**)", "Edit(//h/.cadrei/framework/**)", "Edit(//h/.cadrei/work/members/**)",
		`Edit(//Docs/old cadre \[1\]/cadrei.conf)`, `Edit(//Docs/old cadre \[1\]/.git/**)`} {
		if !contains(deny, d) {
			t.Errorf("the copy lacks %s", d)
		}
	}
	if contains(deny, legacy[0]) {
		t.Error("the copy keeps the legacy Write rule")
	}
	if allow, _ := texts(root.Get("permissions").Get("allow")); !contains(allow, "Bash(npm test)") {
		t.Error("the copy lost a grant")
	}
	// A tampered copy is rewritten at the next start.
	os.Chmod(out, 0o600)
	os.WriteFile(out, []byte(`{"permissions": {"defaultMode": "bypassPermissions"}}`), 0o600)
	out2, err := Export(p, dir, Places{Root: "/h/.cadrei", Cadreis: []string{"/h/.cadrei/work", "/Docs/old cadre [1]"}})
	if err != nil || out2 != out {
		t.Fatalf("second export: %s %v", out2, err)
	}
	if again, _ := os.ReadFile(out2); string(again) != string(raw) {
		t.Error("a tampered copy was not rewritten")
	}
	// An unusable file gives no copy, and the reason.
	os.WriteFile(p, []byte(`{"hooks": {}}`), 0o644)
	if _, err := Export(p, dir, Places{}); err == nil {
		t.Error("an unusable file was exported")
	}
}

func TestFingerprints(t *testing.T) {
	p := newFile(t)
	hashes := filepath.Join(t.TempDir(), "config", "member-settings.sha256")
	if s, _ := Verify(p, hashes); s != Unknown {
		t.Errorf("before recording: %d", s)
	}
	if err := Record(p, hashes); err != nil {
		t.Fatal(err)
	}
	if s, _ := Verify(p, hashes); s != Same {
		t.Errorf("after recording: %d", s)
	}
	os.WriteFile(p, []byte("{}"), 0o644)
	if s, _ := Verify(p, hashes); s != Differs {
		t.Errorf("after a change: %d", s)
	}
	if st, _ := os.Stat(hashes); st.Mode().Perm() != 0o600 {
		t.Errorf("hash file mode %v", st.Mode().Perm())
	}
	// Keyed by physical path: a symlinked spelling finds the same line.
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(filepath.Dir(p), link)
	Record(p, hashes)
	if s, _ := Verify(filepath.Join(link, filepath.Base(p)), hashes); s != Same {
		t.Errorf("through a symlink: %d", s)
	}
}

// Twenty concurrent records for two files keep both lines.
func TestRecordIsSafeAcrossCadreis(t *testing.T) {
	a, b := newFile(t), newFile(t)
	hashes := filepath.Join(t.TempDir(), "h")
	done := make(chan error)
	for i := 0; i < 20; i++ {
		go func() { done <- Record(a, hashes) }()
		go func() { done <- Record(b, hashes) }()
	}
	for i := 0; i < 40; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{a, b} {
		if s, _ := Verify(p, hashes); s != Same {
			t.Errorf("%s lost its line", p)
		}
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestChangedOutside(t *testing.T) {
	cadrei := t.TempDir()
	git(t, cadrei, "init", "-q", "-b", "main")
	p := filepath.Join(cadrei, Rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	Create(p)
	git(t, cadrei, "add", "-A")
	git(t, cadrei, "commit", "-qm", "Add the member settings file")
	hashes := filepath.Join(t.TempDir(), "h")

	// Unknown hash, but the file is what cadrei committed: accepted and recorded.
	if ChangedOutside(cadrei, p, hashes) {
		t.Error("a file cadrei committed counts as changed outside")
	}
	if s, _ := Verify(p, hashes); s != Same {
		t.Error("it was not recorded")
	}
	// A hand edit, uncommitted.
	g, _ := OpenGrants(p)
	g.Add(Rule, "Bash(curl *)", false, time.Now())
	if !ChangedOutside(cadrei, p, hashes) {
		t.Error("an uncommitted hand edit was accepted")
	}
	// Committed by hand with a message cadrei does not write.
	git(t, cadrei, "commit", "-qam", "my own edit")
	if !ChangedOutside(cadrei, p, hashes) {
		t.Error("a hand-made commit was accepted")
	}
	// Committed by cadrei (pulled from another machine): accepted.
	git(t, cadrei, "commit", "-q", "--amend", "-m", "Allow for members: Bash(curl *)")
	if ChangedOutside(cadrei, p, hashes) {
		t.Error("a grant cadrei committed was not accepted")
	}
}

// T-E3: with no record here and nothing committed (no commits yet, or a
// file never committed), the file is taken as it is, as on first use; a
// later edit is still caught. A detached HEAD still holds the commit.
func TestFirstUseWithoutACommit(t *testing.T) {
	for _, setup := range []string{"no commits", "never committed", "detached"} {
		cadrei := t.TempDir()
		git(t, cadrei, "init", "-q", "-b", "main")
		p := filepath.Join(cadrei, Rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		switch setup {
		case "never committed":
			os.WriteFile(filepath.Join(cadrei, "a"), []byte("a"), 0o644)
			git(t, cadrei, "add", "a")
			git(t, cadrei, "commit", "-qm", "a")
			Create(p)
		case "detached":
			Create(p)
			git(t, cadrei, "add", "-A")
			git(t, cadrei, "commit", "-qm", "Add the member settings file")
			os.WriteFile(filepath.Join(cadrei, "b"), []byte("b"), 0o644)
			git(t, cadrei, "add", "b")
			git(t, cadrei, "commit", "-qm", "b")
			git(t, cadrei, "checkout", "-q", "HEAD~1")
		default:
			Create(p)
		}
		hashes := filepath.Join(t.TempDir(), "h")
		if got, want := FirstUse(cadrei, p, hashes), setup != "detached"; got != want {
			t.Errorf("%s: FirstUse %v", setup, got)
		}
		if ChangedOutside(cadrei, p, hashes) {
			t.Errorf("%s: taken for a change outside cadrei allow", setup)
		}
		if FirstUse(cadrei, p, hashes) {
			t.Errorf("%s: not recorded", setup)
		}
		g, _ := OpenGrants(p)
		g.Add(Rule, "Bash(curl *)", false, time.Now())
		if !ChangedOutside(cadrei, p, hashes) {
			t.Errorf("%s: a later hand edit was accepted", setup)
		}
	}
}

func TestGrants(t *testing.T) {
	p := newFile(t)
	g, _ := OpenGrants(p)
	now := time.Unix(1_700_000_000, 0)
	if err := g.Add(Rule, "Bash(npm test)", false, now); err != nil {
		t.Fatal(err)
	}
	if err := g.Add(Auto, "Running tests is expected", false, now); err != nil {
		t.Fatal(err)
	}
	if err := g.Add(Rule, "Bash(make deploy)", true, now); err != nil {
		t.Fatal(err)
	}
	if err := g.Add(Rule, "Bash(npm test)", false, now); !errors.Is(err, ErrGranted) {
		t.Errorf("duplicate add: %v", err)
	}
	g, _ = OpenGrants(p)
	list := g.List()
	if len(list) != 3 || list[0].Entry != "Bash(npm test)" || list[1].Entry != "Bash(make deploy)" || list[2].Kind != Auto {
		t.Fatalf("list %+v", list)
	}
	if !list[1].Once || !list[1].Added.Equal(now) || list[0].Once {
		t.Errorf("one-time marks %+v", list)
	}
	if _, err := Load(p); err != nil {
		t.Errorf("the file no longer validates: %v", err)
	}

	if _, err := g.Remove("9"); err == nil || !strings.Contains(err.Error(), "there is no grant number 9") {
		t.Errorf("bad number: %v", err)
	}
	if _, err := g.Remove("Bash(ls)"); err == nil || !strings.Contains(err.Error(), "Bash(ls) is not granted") {
		t.Errorf("unknown grant: %v", err)
	}
	r, err := g.Remove("--once")
	if err != nil || strings.Join(r.Removed, ",") != "Bash(make deploy)" {
		t.Errorf("remove --once: %+v %v", r, err)
	}
	if _, err := g.Remove("--once"); !errors.Is(err, ErrNoOnce) {
		t.Errorf("second remove --once: %v", err)
	}
	r, err = g.Remove("1")
	if err != nil || r.Removed[0] != "Bash(npm test)" {
		t.Errorf("remove 1: %+v %v", r, err)
	}
	g, _ = OpenGrants(p)
	if len(g.List()) != 1 || g.List()[0].Entry != "Running tests is expected" {
		t.Errorf("after removals: %+v", g.List())
	}
	if raw, _ := os.ReadFile(OncePath(p)); len(raw) != 0 {
		t.Errorf("sidecar not emptied: %q", raw)
	}

	// A one-time record whose grant was removed by hand is stale and dropped.
	g.Add(Rule, "Bash(make ship)", true, now)
	g, _ = OpenGrants(p)
	g.Remove("Bash(make ship)")
	os.WriteFile(OncePath(p), []byte("1\tBash(gone)\n"), 0o644)
	g, _ = OpenGrants(p)
	if strings.Join(g.Stale, ",") != "Bash(gone)" || g.HasOnce() {
		t.Errorf("stale %q, once %v", g.Stale, g.HasOnce())
	}
	r, err = g.Remove("--once")
	if err != nil || strings.Join(r.Stale, ",") != "Bash(gone)" {
		t.Errorf("dropping stale records: %+v %v", r, err)
	}
	if raw, _ := os.ReadFile(OncePath(p)); len(raw) != 0 {
		t.Errorf("stale record kept: %q", raw)
	}
}

// Review 10: the physical archive path comes from the archive folder
// itself, which a linked ~/.cadrei doesn't give.
func TestTheArchiveDenyUsesItsOwnPath(t *testing.T) {
	got := strings.Join(Places{Root: "/disk/cadrei", Archive: "/Users/u/.cadrei-archive"}.deny(), "\n")
	if !strings.Contains(got, "Read(//Users/u/.cadrei-archive/**)") || strings.Contains(got, "/disk/.cadrei-archive") {
		t.Errorf("deny:\n%s", got)
	}
}

// Forward-compatibility audit S11: the entries a tracked grants file must
// hold are frozen, word for word, since every cadrei version checks a crew
// written by any other against them.
func TestTheRequiredEntriesAreFrozen(t *testing.T) {
	if strings.Join(Protect, "|") != "Edit(//**/.claude/member-settings.json)|Bash(cadrei allow:*)" {
		t.Errorf("Protect changed: %q", Protect)
	}
	if FixedSoft != "Changing member permissions (editing a cadrei's .claude/member-settings.json or running cadrei allow) is only done by the user through the orchestrator" {
		t.Errorf("FixedSoft changed: %q", FixedSoft)
	}
}
