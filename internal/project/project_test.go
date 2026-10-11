package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnerRepo(t *testing.T) {
	for _, in := range []string{"me/app", "Me/App", "https://github.com/me/app", "https://github.com/me/app.git",
		"git@github.com:me/app.git", "ssh://git@github.com/me/app", "https://github.com/me/app/"} {
		if got := OwnerRepo(in); got != "me/app" {
			t.Errorf("OwnerRepo(%q) = %q", in, got)
		}
	}
	if OwnerRepo("me/app") == OwnerRepo("you/app") {
		t.Error("different owners compare equal")
	}
}

func TestCheckName(t *testing.T) {
	for _, ok := range []string{"app", "my.app", "a_b-c"} {
		if CheckName(ok) != nil {
			t.Errorf("%s refused", ok)
		}
	}
	for _, bad := range []string{"", "..", "a..b", "../x", ".hidden", "a/b", "a b"} {
		if CheckName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSameRepo(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"me/app", "https://github.com/me/app.git", true},
		{"git@github.com:me/app.git", "https://GitHub.com/me/app", true},
		{"https://github.com/me/app", "https://gitlab.com/me/app", false},
		{"git@gitlab.com:me/app.git", "https://github.com/me/app", false},
		{"ssh://git@github.com:22/me/app", "https://github.com/me/app", true},
		{"me/app", "you/app", false},
		{"", "", false},
		{"github.com/me/app", "me/app", true},
		{"github.com/me/app", "git@github.com:me/app.git", true},
		// Another host that only spells github.com in its path or name.
		{"github.com/acme/tool", "https://evil.example/github.com/acme/tool", false},
		{"github.com/acme/tool", "https://github.com.evil.example/acme/tool", false},
		{"acme/tool", "https://evil.example/acme/tool", false},
		{"github.com/acme/tool", "https://github.com/acme/tool.evil", false},
		// A repository on disk matches only itself.
		{"github.com/acme/tool", "file:///x/acme/tool", false},
		{"acme/tool", "/x/acme/tool", false},
		{"/x/acme/tool.git", "/x/acme/tool.git", true},
		{"file:///x/acme/tool", "/x/acme/tool", true},
		{"/x/acme/tool", "/y/acme/tool", false},
		// git reads the host as evil.example in each of these.
		{"acme/tool", "https://evil.example#@github.com/acme/tool", false},
		{"acme/tool", "https://evil.example?@github.com/acme/tool", false},
		{"acme/tool", `https://evil.example\@github.com/acme/tool`, false},
		{"acme/tool", "https://evil.example/#@github.com/acme/tool", false},
		{"acme/tool", "evil.example#@github.com:acme/tool", false},
		{"acme/tool", "https://x:" + "y@github.com/acme/tool", false}, // split, so the repo holds no password in a URL
		{"acme/tool", "https://a@b@github.com/acme/tool", false},
		{"acme/tool", "https://me@github.com/acme/tool", true},
		{"acme/tool", "ssh://git@github.com:22/acme/tool.git", true},
	} {
		if got := SameRepo(tc.a, tc.b); got != tc.want {
			t.Errorf("SameRepo(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}

func TestCloneRefusesOptions(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	for _, repo := range []string{"--upload-pack=touch " + marker, "-u touch " + marker} {
		if err := Clone(repo, filepath.Join(dir, "x")); err == nil || !strings.Contains(err.Error(), "can't start with -") {
			t.Errorf("Clone(%q): %v", repo, err)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("an option in a repo ran")
	}
}

// Finder reads the projects folder's origins once and finds a clone of a
// repository by any spelling of it.
func TestFinder(t *testing.T) {
	dir := t.TempDir()
	for name, origin := range map[string]string{"app": "https://github.com/me/app.git", "blog": "git@github.com:me/blog.git", "plain": ""} {
		p := filepath.Join(dir, name)
		os.MkdirAll(p, 0o755)
		exec.Command("git", "-C", p, "init", "-q").Run()
		if origin != "" {
			exec.Command("git", "-C", p, "remote", "add", "origin", origin).Run()
		}
	}
	f := NewFinder(dir)
	if f.Find("me/app") != filepath.Join(dir, "app") || f.Find("github.com/me/blog") != filepath.Join(dir, "blog") || f.Find("me/other") != "" || f.Find("") != "" {
		t.Error("Find")
	}
	// The origins were read once: a clone added later is not looked at.
	os.MkdirAll(filepath.Join(dir, "late"), 0o755)
	exec.Command("git", "-C", filepath.Join(dir, "late"), "init", "-q").Run()
	exec.Command("git", "-C", filepath.Join(dir, "late"), "remote", "add", "origin", "https://github.com/me/late").Run()
	if f.Find("me/late") != "" {
		t.Error("the projects folder was read again")
	}
	if NewFinder("").Find("me/app") != "" {
		t.Error("no projects folder finds nothing")
	}
}

// Audit sec-exposure-2: a remote's password or token never reaches the
// registry, ls or an archive; a plain ssh user stays.
func TestRedact(t *testing.T) {
	pass := "pass" + "word"
	for in, want := range map[string]string{
		"https://me:" + pass + "@git.example.com/me/app.git": "https://git.example.com/me/app.git",
		"https://sometoken@github.com/me/app.git":            "https://github.com/me/app.git",
		"http://me@example.com/app":                          "http://example.com/app",
		"ssh://git:" + pass + "@example.com/me/app.git":      "ssh://git@example.com/me/app.git",
		"ssh://git@example.com/me/app.git":                   "ssh://git@example.com/me/app.git",
		"git@github.com:me/app.git":                          "git@github.com:me/app.git",
		"me/app":                                             "me/app",
		"/srv/git/app.git":                                   "/srv/git/app.git",
		// URLs Go won't parse, which git takes: the user part goes whole.
		"https://alice:p%zz" + "@host/x.git": "https://host/x.git",
		"https://alice:pa ss@host/x.git":     "https://host/x.git",
		"https://u:p" + "@host:badport/x":    "https://host:badport/x",
		"git+https://u:" + pass + "@host/x":  "git+https://host/x",
		// Git sends ftp(s) and git:// users through too (review of fix
		// round 5, sec N4), and any other scheme's password goes.
		"ftp://u:" + pass + "@host/x.git":  "ftp://host/x.git",
		"ftps://u:" + pass + "@host/x.git": "ftps://host/x.git",
		"git://u:" + pass + "@host/x.git":  "git://host/x.git",
		// A token as the user alone (review of fix round 6).
		"ftp://sometoken@host/x.git":    "ftp://host/x.git",
		"ftps://sometoken@host/x.git":   "ftps://host/x.git",
		"git://sometoken@host/x.git":    "git://host/x.git",
		"other://u:" + pass + "@host/x": "other://host/x",
		"other://u@host/x":              "other://u@host/x",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

// Review of fix round 5 (sec N6, N7 and N10): the trust and place checks
// go by identity, so another spelling of ~/.cadrei or the home folder is
// refused (where the disk ignores case), ~/.claude is refused in any case
// before it exists, and a folder that shows the whole disk again holds
// ~/.cadrei.
func TestRefusalsGoByIdentity(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("CADREI_HOME", "")
	os.MkdirAll(home+"/.cadrei/work", 0o755)
	if why := DestRefusal(home+"/.CLAUDE/skills/x", []string{home + "/.claude"}); !strings.Contains(why, "inside ~/.claude") {
		t.Errorf("~/.CLAUDE before ~/.claude is there: %q", why)
	}
	if _, err := os.Stat(home + "/.CADREI"); err == nil {
		// A disk that ignores case.
		if why := TrustRefusal(home+"/.CADREI/work", nil); !strings.Contains(why, "inside ~/.cadrei") {
			t.Errorf("trust ~/.CADREI/work: %q", why)
		}
		upper := filepath.Join(filepath.Dir(home), strings.ToUpper(filepath.Base(home)))
		if why := TrustRefusal(upper, nil); why != "it's your home folder" {
			t.Errorf("trust another spelling of home: %q", why)
		}
	}
	if _, err := os.Stat("/System/Volumes/Data" + home); err == nil {
		// Each by its own rule (review of fix round 6): any refusal would
		// come anyway, as /System/Volumes/Data is no git top.
		if why := DestRefusal("/System/Volumes/Data", nil); why != "it contains ~/.cadrei" {
			t.Errorf("/System/Volumes/Data can hold a project: %q", why)
		}
		if why := TrustRefusal("/System/Volumes/Data", nil); why != "it's ~/.cadrei, or holds it" {
			t.Errorf("/System/Volumes/Data can be trusted: %q", why)
		}
		// With no ~/.cadrei, a folder the runtime loads code from.
		os.RemoveAll(home + "/.cadrei")
		os.MkdirAll(home+"/.claude", 0o755)
		if why := DestRefusal("/System/Volumes/Data", []string{home + "/.claude"}); why != "it contains ~/.claude" {
			t.Errorf("/System/Volumes/Data holds ~/.claude: %q", why)
		}
	}
}
