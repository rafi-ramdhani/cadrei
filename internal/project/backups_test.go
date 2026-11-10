package project

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// stubGh puts a gh on the PATH that runs script, so no test reaches the
// real one.
func stubGh(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

// 0.2.3 item 1: the user's repos named cadrei-<something>, the one pushed
// to last first; a name a terminal would read as more than text, and a
// bare cadrei-, are left out. A gh auth status that fails (a stale second
// account, say) doesn't matter: only the list does.
func TestBackupRepos(t *testing.T) {
	stubGh(t, `case "$1 $2" in
"auth status") exit 1 ;;
"repo list") [ "$*" = 'repo list --limit 200 --json name,nameWithOwner,pushedAt' ] || exit 1
echo '[{"name":"site","nameWithOwner":"rafi/site","pushedAt":"2026-11-05T00:00:00Z"},
{"name":"cadrei-home","nameWithOwner":"rafi/cadrei-home","pushedAt":"2026-10-01T00:00:00Z"},
{"name":"Cadrei-Work","nameWithOwner":"rafi/Cadrei-Work","pushedAt":"2026-11-02T00:00:00Z"},
{"name":"cadrei-","nameWithOwner":"rafi/cadrei-","pushedAt":"2026-11-03T00:00:00Z"},
{"name":"cadrei-x","nameWithOwner":"rafi/cadrei-x\u001b[2J","pushedAt":"2026-11-04T00:00:00Z"}]'; exit 0 ;;
esac
exit 1
`)
	got, err := BackupRepos()
	if err != nil || !slices.Equal(got, []string{"rafi/Cadrei-Work", "rafi/cadrei-home"}) {
		t.Errorf("got %q, %v", got, err)
	}
}

// A gh that isn't signed in, fails, prints what isn't JSON, or hangs is
// an error, in time, even when what hangs is a process gh started, which
// keeps gh's output open after gh is killed.
func TestBackupReposFails(t *testing.T) {
	old := ghWait
	// Long enough for the hanging stub to start its sleep before gh is
	// killed, short beside the sleep.
	ghWait = time.Second
	t.Cleanup(func() { ghWait = old })
	for name, script := range map[string]string{
		"not signed in": "exit 1\n",
		"not json":      "echo nope\n",
		"hangs":         "sleep 30\nexit 0\n",
	} {
		stubGh(t, script)
		start := time.Now()
		if got, err := BackupRepos(); err == nil || got != nil || time.Since(start) > 5*time.Second {
			t.Errorf("%s: %q, %v after %s", name, got, err, time.Since(start))
		}
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := BackupRepos(); err == nil {
		t.Error("no gh at all")
	}
}
