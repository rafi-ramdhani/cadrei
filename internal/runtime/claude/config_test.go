package claude

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafi-ramdhani/cadrei/internal/jsonx"
)

// A config as Claude Code writes it (JSON.stringify(x, null, 2)).
const claudeJSON = `{
  "numStartups": 3,
  "zeta": "z",
  "alpha": {
    "nested": [
      1,
      2.50,
      1e3
    ],
    "empty": {},
    "none": []
  },
  "history": "pasted \ud83d broken <b>&amp;</b> \u00e9",
  "projects": {
    "/elsewhere": {
      "allowedTools": [],
      "hasTrustDialogAccepted": false
    }
  }
}
`

func writeConfig(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func trustOp(dirs ...string) jsonx.Op { return Trust([]TrustEntry{{Name: "app", Dirs: dirs}}) }

func TestTrustKeepsEverythingElse(t *testing.T) {
	p := writeConfig(t, claudeJSON, 0o644)
	code, lines := jsonx.Edit(p, jsonx.Options{Backup: p + ".bak-cadrei"}, trustOp("/w/app", "/typed/app"))
	if code != jsonx.Changed || strings.Join(lines, ",") != "app\ttrusted" {
		t.Fatalf("jsonx.Edit = %d %q", code, lines)
	}
	got, _ := os.ReadFile(p)
	want := strings.Replace(claudeJSON, `      "hasTrustDialogAccepted": false
    }
  }`, `      "hasTrustDialogAccepted": false
    },
    "/w/app": {
      "hasTrustDialogAccepted": true
    },
    "/typed/app": {
      "hasTrustDialogAccepted": true
    }
  }`, 1)
	if string(got) != want {
		t.Errorf("config after trust:\n%s\nwant:\n%s", got, want)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o644 {
		t.Errorf("mode changed to %v", st.Mode().Perm())
	}
	if bak, _ := os.ReadFile(p + ".bak-cadrei"); string(bak) != claudeJSON {
		t.Error("the backup is not the original")
	}
	// Trusting again changes nothing and writes nothing.
	st1, _ := os.Stat(p)
	code, lines = jsonx.Edit(p, jsonx.Options{Backup: p + ".bak-cadrei"}, trustOp("/w/app", "/typed/app"))
	st2, _ := os.Stat(p)
	if code != jsonx.Unchanged || lines[0] != "app\talready" || !st1.ModTime().Equal(st2.ModTime()) {
		t.Errorf("second trust: %d %q, mtime changed %v", code, lines, !st1.ModTime().Equal(st2.ModTime()))
	}
	// The first backup is never overwritten.
	jsonx.Edit(p, jsonx.Options{Backup: p + ".bak-cadrei"}, trustOp("/w/other"))
	if bak, _ := os.ReadFile(p + ".bak-cadrei"); string(bak) != claudeJSON {
		t.Error("a later edit overwrote the first backup")
	}
}

func TestUntrustRemovesOnlyTheKey(t *testing.T) {
	p := writeConfig(t, `{"projects": {"/a": {"allowedTools": ["x"], "hasTrustDialogAccepted": true}, "/b": {}}}`, 0o600)
	code, lines := jsonx.Edit(p, jsonx.Options{Backup: p + ".bak"}, Untrust([]TrustEntry{{Name: "a", Dirs: []string{"/a", "/missing"}}, {Name: "b", Dirs: []string{"/b"}}}))
	got, _ := os.ReadFile(p)
	if code != jsonx.Changed || string(got) != `{"projects":{"/a":{"allowedTools":["x"]},"/b":{}}}` {
		t.Errorf("code %d, file %s", code, got)
	}
	if strings.Join(lines, ",") != "a\tuntrusted,b\tnot trusted" {
		t.Errorf("lines %q", lines)
	}
}

func isOrch(c string) bool { _, ok := OrchestratorHook(c); return ok }

func TestHooks(t *testing.T) {
	settings := `{
  "model": "x",
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "bash /old/cadrei/bin/orchestrator-hook.sh"}, {"type": "command", "command": "echo shared"}]},
      {"hooks": [{"type": "command", "command": "echo mine"}]}
    ],
    "Stop": []
  }
}`
	p := writeConfig(t, settings, 0o600)
	add := AddHook("/opt/bin/cadrei hook orchestrator", isOrch)
	if code, _ := jsonx.Edit(p, jsonx.Options{Backup: p + ".bak"}, add); code != jsonx.Changed {
		t.Fatalf("add: %d", code)
	}
	got, _ := os.ReadFile(p)
	root, _ := jsonx.Parse(got)
	groups := root.Get("hooks").Get("SessionStart").Items
	if len(groups) != 3 {
		t.Fatalf("groups after add: %s", got)
	}
	// A hook of the user's that shared cadrei's group stays (review of
	// fix round 6), and so does the user's own group.
	if c, _ := groups[0].Get("hooks").Items[0].Get("command").Text(); c != "echo shared" || len(groups[0].Get("hooks").Items) != 1 {
		t.Errorf("the hook that shared cadrei's group was not kept alone: %s", got)
	}
	if c, _ := groups[1].Get("hooks").Items[0].Get("command").Text(); c != "echo mine" {
		t.Errorf("the user's own hook was not kept: %s", got)
	}
	if c, _ := groups[2].Get("hooks").Items[0].Get("command").Text(); c != "/opt/bin/cadrei hook orchestrator" {
		t.Errorf("the new hook is %q", c)
	}
	if code, _ := jsonx.Edit(p, jsonx.Options{}, add); code != jsonx.Unchanged {
		t.Errorf("adding again: %d", code)
	}

	ours := func(c string) bool { return c == "/opt/bin/cadrei hook orchestrator" }
	code, lines := jsonx.Edit(p, jsonx.Options{Backup: p + ".bak-uninstall", FreshBackup: true}, Unhook(ours, isOrch))
	if code != jsonx.Changed || strings.Join(lines, ",") != "removed\t/opt/bin/cadrei hook orchestrator" {
		t.Errorf("unhook: %d %q", code, lines)
	}
	got, _ = os.ReadFile(p)
	if bytes.Contains(got, []byte("orchestrator")) || !bytes.Contains(got, []byte("echo mine")) || !bytes.Contains(got, []byte(`"Stop": []`)) {
		t.Errorf("after unhook: %s", got)
	}
}

func TestUnhookReportsAnotherFrameworksHooks(t *testing.T) {
	p := writeConfig(t, `{"hooks": {"SessionStart": [{"hooks": [
		{"command": "bash /mine/bin/orchestrator-hook.sh"},
		{"command": "bash '/elsewhere/my cadrei/bin/orchestrator-hook.sh'"}]}]}}`, 0o600)
	ours := func(c string) bool { s, _ := OrchestratorHook(c); return s == "/mine/bin/orchestrator-hook.sh" }
	code, lines := jsonx.Edit(p, jsonx.Options{}, Unhook(ours, isOrch))
	want := "removed\tbash /mine/bin/orchestrator-hook.sh,kept\tbash '/elsewhere/my cadrei/bin/orchestrator-hook.sh'"
	if code != jsonx.Changed || strings.Join(lines, ",") != want {
		t.Errorf("code %d, lines %q", code, lines)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Contains(got, []byte("elsewhere")) {
		t.Errorf("another framework's hook was removed: %s", got)
	}
	// Only hooks that are not ours, or none at all: nothing to do.
	p = writeConfig(t, `{"model": "x"}`, 0o600)
	if code, _ := jsonx.Edit(p, jsonx.Options{}, Unhook(ours, isOrch)); code != jsonx.Unchanged {
		t.Errorf("no hooks: %d", code)
	}
	p = writeConfig(t, `{"hooks": {"SessionStart": {}}}`, 0o600)
	if code, _ := jsonx.Edit(p, jsonx.Options{}, Unhook(ours, isOrch)); code != jsonx.Unusable {
		t.Errorf("SessionStart not a list: %d", code)
	}
}

func TestOrchestratorHook(t *testing.T) {
	for _, tc := range []struct {
		cmd, want string
		ok        bool
	}{
		{"bash /x/bin/orchestrator-hook.sh", "/x/bin/orchestrator-hook.sh", true},
		{`bash '/x/my cadrei/bin/orchestrator-hook.sh'`, "/x/my cadrei/bin/orchestrator-hook.sh", true},
		// 0.1.x wrote only `bash <path>`: shell syntax isn't its (review
		// of fix round 6).
		{`bash /x/my\ cadrei/bin/orchestrator-hook.sh`, "", false},
		{`bash '/x/it''s/orchestrator-hook.sh'`, "", false},
		{"bash $(x)/orchestrator-hook.sh", "", false},
		{"bash `x`/orchestrator-hook.sh", "", false},
		{"bash x;/orchestrator-hook.sh", "", false},
		{"/opt/homebrew/opt/cadrei/bin/cadrei hook orchestrator", "/opt/homebrew/opt/cadrei/bin/cadrei", true},
		{"bash /x/my-orchestrator-hook.sh", "", false},
		{"/x/cadrei hook statusline", "", false},
		{"sh /x/orchestrator-hook.sh", "", false},
		{"bash 'unclosed", "", false},
	} {
		got, ok := OrchestratorHook(tc.cmd)
		if got != tc.want || ok != tc.ok {
			t.Errorf("OrchestratorHook(%q) = %q, %v", tc.cmd, got, ok)
		}
	}
}
