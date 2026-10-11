package backup

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "t@example.com")
	git(t, dir, "config", "user.name", "T")
	return dir
}

func commit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for p, data := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte(data), 0o644)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "x")
	return git(t, dir, "rev-parse", "HEAD")
}

func TestInstall(t *testing.T) {
	dir := repo(t)
	if wrote, err := Install(dir, "/opt/cadrei/bin/cadrei"); err != nil || !wrote {
		t.Fatalf("install: %v %v", wrote, err)
	}
	hook := filepath.Join(dir, ".git", "hooks", "pre-push")
	raw, _ := os.ReadFile(hook)
	if !strings.Contains(string(raw), "exec '/opt/cadrei/bin/cadrei' hook pre-push \"$@\"") {
		t.Errorf("hook %q", raw)
	}
	if st, _ := os.Stat(hook); st.Mode().Perm()&0o100 == 0 {
		t.Error("the hook is not executable")
	}
	if wrote, _ := Install(dir, "/opt/cadrei/bin/cadrei"); wrote {
		t.Error("a current hook was written again")
	}
	if wrote, _ := Install(dir, "/new/cadrei"); !wrote {
		t.Error("cadrei's hook for another binary was not updated")
	}
	os.WriteFile(hook, []byte("#!/bin/sh\nmy own hook\n"), 0o755)
	if _, err := Install(dir, "/new/cadrei"); err == nil || !strings.Contains(err.Error(), "not cadrei's") {
		t.Errorf("a hook of the user's: %v", err)
	}
	if raw, _ := os.ReadFile(hook); string(raw) != "#!/bin/sh\nmy own hook\n" {
		t.Error("the user's hook was changed")
	}
}

func TestScan(t *testing.T) {
	MaxSize = 1 << 10
	defer func() { MaxSize = 50 << 20 }()
	dir := repo(t)
	// A first commit already on the remote is not checked again.
	commit(t, dir, map[string]string{"playbook.md": "# Playbook\n", ".env": "OLD=1\n"})
	remote := t.TempDir()
	git(t, remote, "init", "-q", "--bare")
	git(t, dir, "remote", "add", "origin", remote)
	git(t, dir, "push", "-q", "origin", "main")
	// A secret added then removed is still in the history that is pushed.
	// Shaped like a key only at run time, so this file holds no
	// secret-shaped string for scanners (or the pre-push check) to find.
	commit(t, dir, map[string]string{"teams/dev/notes.md": "key: " + "sk-ant-" + "api03-" + strings.Repeat("a", 20) + "\n"})
	os.Remove(filepath.Join(dir, "teams/dev/notes.md"))
	head := commit(t, dir, map[string]string{
		"teams/dev/.env.local":   "X=1\n",
		"teams/dev/.env.example": "X=\n",
		"teams/dev/big.bin":      strings.Repeat("x", 2<<10),
		"teams/dev/id_ed25519":   "key\n",
		"teams/dev/ok.md":        "fine, and ghp_ is only a prefix here\n",
		"teams/dev/gh.txt":       "token ghp_" + strings.Repeat("a", 36) + "\n",
		"teams/dev/key.txt":      "-----BEGIN " + "OPENSSH PRIVATE" + " KEY-----\n", // a key header only at run time
		// A password in a URL only at run time; a placeholder isn't one.
		"projects.yaml":    "app:\n  repo: https://alice:" + "hunter2" + "x@git.example.com/alice/app.git\n",
		"teams/dev/how.md": "Clone with https://user:<token>@git.example.com/x.git or https://${USER}:${TOKEN}@host/x\n",
	})
	got, err := Scan(dir, "origin", []Update{{"refs/heads/main", head, "refs/heads/main", "0000"}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"teams/dev/.env.local": "environment file", "teams/dev/big.bin": "over the", "teams/dev/gh.txt": "GitHub token",
		"teams/dev/id_ed25519": "private key", "teams/dev/key.txt": "private key", "teams/dev/notes.md": "Anthropic API key",
		"projects.yaml": "password in a URL",
	}
	if len(got) != len(want) {
		t.Errorf("findings %+v", got)
	}
	for _, f := range got {
		if w, ok := want[f.Path]; !ok || !strings.Contains(f.Why, w) {
			t.Errorf("finding %+v, want %q", f, w)
		}
	}
	// Deleting a branch carries nothing.
	if got, _ := Scan(dir, "origin", []Update{{"(delete)", "0000000000000000000000000000000000000000", "refs/heads/x", head}}); len(got) != 0 {
		t.Errorf("a delete: %+v", got)
	}
}

func TestReadUpdates(t *testing.T) {
	u := ReadUpdates(strings.NewReader("refs/heads/main abc refs/heads/main def\nbad line\n"))
	if len(u) != 1 || u[0].LocalSHA != "abc" || u[0].RemoteSHA != "def" {
		t.Errorf("%+v", u)
	}
}

func TestRemove(t *testing.T) {
	dir := repo(t)
	Install(dir, "/opt/cadrei/bin/cadrei")
	if removed, err := Remove(dir); !removed || err != nil {
		t.Errorf("remove: %v %v", removed, err)
	}
	hook := filepath.Join(dir, ".git", "hooks", "pre-push")
	if _, err := os.Stat(hook); err == nil {
		t.Error("cadrei's hook is still there")
	}
	os.WriteFile(hook, []byte("#!/bin/sh\nmine\n"), 0o755)
	if removed, _ := Remove(dir); removed {
		t.Error("the user's hook was removed")
	}
	if removed, _ := Remove(t.TempDir()); removed {
		t.Error("a folder that is not a repository")
	}
	// A binary whose path holds a quote is still cadrei's own text.
	os.Remove(hook)
	Install(dir, "/opt/it's/cadrei")
	if removed, err := Remove(dir); !removed || err != nil {
		t.Errorf("a quoted path: %v %v", removed, err)
	}
	// Lines the user added to cadrei's hook are theirs: the file stays.
	Install(dir, "/opt/cadrei/bin/cadrei")
	raw, _ := os.ReadFile(hook)
	changed := string(raw) + "echo my own check\n"
	os.WriteFile(hook, []byte(changed), 0o755)
	if removed, err := Remove(dir); removed || !errors.Is(err, ErrChanged) {
		t.Errorf("a hook with the user's lines: %v %v", removed, err)
	}
	if b, _ := os.ReadFile(hook); string(b) != changed {
		t.Error("the user's lines were lost")
	}
}

// findingPaths runs Scan for a push of HEAD to remote and returns the
// paths it stops on.
func findingPaths(t *testing.T, dir, remote string) []string {
	t.Helper()
	head := git(t, dir, "rev-parse", "HEAD")
	got, err := Scan(dir, remote, []Update{{"refs/heads/main", head, "refs/heads/main", "0000"}})
	if err != nil {
		t.Fatal(err)
	}
	var ps []string
	for _, f := range got {
		ps = append(ps, f.Path)
	}
	return ps
}

// Every name a file has in the pushed commits is checked, not only the
// first one git meets: a file renamed later, and a file with an identical
// copy elsewhere, are both stopped.
func TestScanChecksEveryNameOfAFile(t *testing.T) {
	dir := repo(t)
	commit(t, dir, map[string]string{"playbook.md": "# Playbook\n", ".env": "SETTING=fake\n"})
	os.MkdirAll(filepath.Join(dir, "teams"), 0o755)
	git(t, dir, "mv", ".env", "teams/notes.txt")
	git(t, dir, "commit", "-qm", "rename")
	if got := strings.Join(findingPaths(t, dir, "origin"), " "); got != ".env" {
		t.Errorf("renamed: %q", got)
	}

	dir = repo(t)
	commit(t, dir, map[string]string{"teams/.env": "SETTING=fake\n", "a/copy.txt": "SETTING=fake\n",
		"teams/id_ed25519": "not a real key\n", "-notes": "not a real key\n"})
	if got := strings.Join(findingPaths(t, dir, "origin"), " "); got != "teams/.env teams/id_ed25519" {
		t.Errorf("copies: %q", got)
	}
}

// Only what the remote being pushed to has is left out: a commit another
// remote already has is still checked.
func TestScanLeavesOutOnlyThisRemotesCommits(t *testing.T) {
	dir := repo(t)
	commit(t, dir, map[string]string{"playbook.md": "# Playbook\n"})
	for _, r := range []string{"backup", "other"} {
		bare := t.TempDir()
		git(t, bare, "init", "-q", "--bare")
		git(t, dir, "remote", "add", r, bare)
	}
	git(t, dir, "push", "-q", "backup", "main")
	commit(t, dir, map[string]string{"teams/dev/.env.local": "SETTING=fake\n"})
	git(t, dir, "push", "-q", "--no-verify", "other", "main")
	if got := strings.Join(findingPaths(t, dir, "backup"), " "); got != "teams/dev/.env.local" {
		t.Errorf("push to backup: %q", got)
	}
	if got := findingPaths(t, dir, "other"); len(got) != 0 {
		t.Errorf("push to other, which has it: %q", got)
	}
	// A push to a URL leaves nothing out.
	if got := strings.Join(findingPaths(t, dir, "https://example.com/x.git"), " "); got != "teams/dev/.env.local" {
		t.Errorf("push to a URL: %q", got)
	}
}

func TestMoreCredentialNamesAndTokens(t *testing.T) {
	dir := repo(t)
	commit(t, dir, map[string]string{
		"teams/ops/credentials":         "fake\n",
		"teams/ops/.git-credentials":    "fake\n",
		"teams/ops/.aws/config":         "fake\n",
		"teams/ops/.kube/config":        "fake\n",
		"teams/ops/.docker/config.json": "{}\n",
		"teams/ops/aws.txt":             "id AKIA" + strings.Repeat("X", 16) + "\n",
		"teams/ops/slack.txt":           "token xoxb-" + strings.Repeat("0", 12) + "\n",
		"teams/ops/notes.md":            "AKIA is a prefix; xoxb- alone is too\n",
	})
	want := "teams/ops/.aws/config teams/ops/.docker/config.json teams/ops/.git-credentials teams/ops/.kube/config teams/ops/aws.txt teams/ops/credentials teams/ops/slack.txt"
	if got := strings.Join(findingPaths(t, dir, "origin"), " "); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// With the cadrei program gone, the hook stops the push and says why.
func TestTheHookStopsAPushWhenCadreiIsGone(t *testing.T) {
	dir := repo(t)
	commit(t, dir, map[string]string{"playbook.md": "# Playbook\n"})
	bare := t.TempDir()
	git(t, bare, "init", "-q", "--bare")
	git(t, dir, "remote", "add", "backup", bare)
	if _, err := Install(dir, filepath.Join(t.TempDir(), "gone", "cadrei")); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", dir, "push", "-q", "backup", "main").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "cadrei is missing, so this check can't run. Reinstall cadrei, or remove .git/hooks/pre-push.") {
		t.Errorf("push: %v %s", err, out)
	}
}

// Review of fix round 5 (sec-exposure-2): a password holding any
// character, or one with a placeholder and more, counts; so does a token
// used as the user. A placeholder whole doesn't, nor does a plain user.
// Every value is made at run time, so this file holds no URL with a
// password in it.
func TestURLSecret(t *testing.T) {
	url := func(user string) string { return "clone https://" + user + "@git.example.com/me/app.git\n" }
	for _, user := range []string{
		"u:" + "pa$$w0rd", "u:" + "pa*ss", "u:" + "a{b}c", "u:" + "a<b", "u:" + "${TOKEN}" + "real", "u:" + "real${X}",
		strings.Repeat("ab12", 10), "glpat-" + strings.Repeat("x1", 10),
		// A token as the user, with an empty or placeholder password.
		strings.Repeat("ab12", 10) + ":", strings.Repeat("ab12", 10) + ":xxx", strings.Repeat("ab12", 10) + ":${PASS}",
	} {
		if !urlSecret([]byte(url(user))) {
			t.Errorf("missed %q", user)
		}
	}
	for _, user := range []string{"u:<token>", "${USER}:${TOKEN}", "u:$TOKEN", "u:{{token}}", "u:***", "u:xxxx", "git", "me", "u:",
		"<token>", "${TOKEN}", strings.Repeat("a", 30), "first.last"} {
		if urlSecret([]byte(url(user))) {
			t.Errorf("%q counted", user)
		}
	}
	if urlSecret([]byte("git@github.com:me/app.git and ssh://git@example.com/x\n")) {
		t.Error("a plain ssh user counted")
	}
}
