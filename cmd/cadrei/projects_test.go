//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/project"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// bareRepo makes a local repository with one commit and returns its path,
// to clone from without a network.
func bareRepo(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), name+"-src")
	bare := filepath.Join(t.TempDir(), name+".git")
	run := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.MkdirAll(src, 0o755)
	run(src, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(src, "README"), []byte(name), 0o644)
	run(src, "add", "-A")
	run(src, "commit", "-qm", "first")
	if out, err := exec.Command("git", "clone", "-q", "--bare", src, bare).CombinedOutput(); err != nil {
		t.Fatalf("bare clone: %v %s", err, out)
	}
	return bare
}

// register writes a cadrei's registry from text that gives each project a
// path: the paths become this machine's places, as cadrei keeps them, and
// the registry holds none.
func register(t *testing.T, home, name, text string) {
	t.Helper()
	c := cadreis.Cadrei{Name: name, Path: home + "/.cadrei/" + name}
	var kept []string
	current := ""
	for _, l := range strings.Split(text, "\n") {
		if !strings.HasPrefix(l, " ") && strings.HasSuffix(l, ":") {
			current = strings.TrimSuffix(l, ":")
		}
		if p, ok := strings.CutPrefix(l, "  path: "); ok {
			if p == "~" || strings.HasPrefix(p, "~/") {
				p = home + p[1:]
			}
			if err := cadreis.SetPlace(c, current, p); err != nil {
				t.Fatal(err)
			}
			continue
		}
		kept = append(kept, l)
	}
	if err := os.WriteFile(c.Path+"/projects.yaml", []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, _ := os.ReadFile(p)
	return string(b)
}

func TestProjectsFolderIsAskedOnce(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	repo := bareRepo(t, "app")
	// Without a terminal: refused, with the suggestion.
	refused(t, "your projects folder isn't set yet. Set it with cadrei project dir <folder>, for example cadrei project dir ~/Developer", "project", "add", "app", repo)
	if strings.Contains(readFile(t, home+"/.cadrei/work/projects.yaml"), "app") {
		t.Error("a refused add was registered")
	}
	// On a terminal: asked, answered with the default, remembered.
	ttyForTests = true
	t.Cleanup(func() { ttyForTests = false })
	code, out, errOut := callIn("\n", "project", "add", "app", repo)
	if code != 0 || !strings.Contains(errOut, "Where do you keep your projects? [~/Developer]") {
		t.Fatalf("add on a terminal: %d %q %q", code, out, errOut)
	}
	if !strings.Contains(out, "+ New clones go to ~/Developer\n+ Cloned app into ~/Developer/app\n") {
		t.Errorf("add: %q", out)
	}
	if got := strings.TrimSpace(readFile(t, home+"/.cadrei/config/projects-dir")); got != "~/Developer" {
		t.Errorf("projects-dir %q", got)
	}
	// The registry holds no local path; this machine's places do.
	reg := readFile(t, home+"/.cadrei/work/projects.yaml")
	if !strings.Contains(reg, "app:\n  repo: "+repo+"\n  team: dev\n  about:\n") || strings.Contains(reg, "path:") {
		t.Errorf("registry:\n%s", reg)
	}
	if places := readFile(t, home+"/.cadrei/work/.claude/local/places.json"); !strings.Contains(places, `"app": "~/Developer/app"`) {
		t.Errorf("places:\n%s", places)
	}
	if _, err := os.Stat(home + "/.cadrei/work/projects"); err == nil {
		t.Error("the add made projects/ inside the cadrei")
	}
	ttyForTests = false
	if out := must(t, "project", "dir"); strings.TrimSpace(out) != "~/Developer" {
		t.Errorf("project dir: %q", out)
	}
}

func TestProjectAddTrustsAndLinks(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	must(t, "project", "dir", "~/Code")
	cfg := home + "/.claude.json"
	os.WriteFile(cfg, []byte("{\n  \"numStartups\": 1\n}\n"), 0o600)
	repo := bareRepo(t, "app")
	out := must(t, "project", "add", "app", repo, "dev", "the app")
	if out != "+ Cloned app into ~/Code/app, and trusted it in Claude Code\n" {
		t.Errorf("add: %q", out)
	}
	if !strings.Contains(readFile(t, cfg), `"`+home+`/Code/app": {`) || readFile(t, cfg+".bak-cadrei") != "{\n  \"numStartups\": 1\n}\n" {
		t.Errorf("config %s", readFile(t, cfg))
	}
	refused(t, "app is already a project in this crew", "project", "add", "app", repo)
	refused(t, "letters, digits", "project", "add", "../x", repo)
	// An existing folder that is the same repository is linked, another is refused.
	os.MkdirAll(home+"/Code/other", 0o755)
	refused(t, "isn't a clone of", "project", "add", "other", repo)

	// Linking a folder the user already has.
	mine := home + "/src/mine"
	exec.Command("git", "clone", "-q", repo, mine).Run()
	out = must(t, "project", "add", "mine", "--path", mine, "research", "--no-trust")
	if out != "+ Added mine from ~/src/mine\n! mine has no GitHub repo, so it won't come along when you restore on another machine.\n" && out != "+ Added mine from ~/src/mine\n" {
		t.Errorf("link: %q", out)
	}
	if reg := readFile(t, home+"/.cadrei/work/projects.yaml"); !strings.Contains(reg, "mine:\n  repo: "+repo+"\n  team: research\n  about:\n") || strings.Contains(reg, "path:") {
		t.Errorf("registry:\n%s", reg)
	}
	if places := readFile(t, home+"/.cadrei/work/.claude/local/places.json"); !strings.Contains(places, `"mine": "~/src/mine"`) {
		t.Errorf("places:\n%s", places)
	}
	refused(t, "already linked as mine", "project", "add", "again", "--path", mine)
	refused(t, "not the top folder of a git repository", "project", "add", "sub", "--path", home+"/src")
	refused(t, "inside ~/.cadrei", "project", "add", "x", "--path", home+"/.cadrei/work")
	refused(t, "your home folder", "project", "add", "x", "--path", home)
	t.Setenv("CADREI_MEMBER", "x")
	refused(t, "team members can't add projects", "project", "add", "y", "--path", mine)
	refused(t, "team members can't add projects", "project", "add", "z", "me/z")
	refused(t, "team members can't trust", "project", "trust", "app")
}

// Review of fix round 5 (sec N4 and N10): a password in a project's repo
// never shows in an error: a clone that fails, a folder that isn't its
// clone (sync, add and link), and a link to another repo's clone.
func TestProjectErrorsLeaveOutThePassword(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	must(t, "project", "dir", "~/Code")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	secret := "not" + "-a-real-one"
	repo := "https://u:" + secret + "@127.0.0.1:1/x.git"
	other := home + "/src/other"
	os.MkdirAll(other, 0o755)
	exec.Command("git", "-C", other, "init", "-q").Run()
	exec.Command("git", "-C", other, "remote", "add", "origin", "https://example.com/me/other.git").Run()
	for _, d := range []string{"taken", "busy"} {
		os.MkdirAll(home+"/Code/"+d, 0o755)
		exec.Command("git", "-C", home+"/Code/"+d, "init", "-q").Run()
		exec.Command("git", "-C", home+"/Code/"+d, "remote", "add", "origin", "https://example.com/me/other.git").Run()
	}
	register(t, home, "work", "gone:\n  repo: "+repo+"\n  team: dev\ntaken:\n  repo: "+repo+"\n  team: dev\nmoved:\n  repo: "+repo+"\n  team: dev\n  path: ~/src/other\n")
	check := func(what string, args ...string) {
		t.Helper()
		_, out, errOut := call(args...)
		if strings.Contains(out+errOut, secret) || !strings.Contains(out+errOut, "127.0.0.1:1/x.git") {
			t.Errorf("%s:\n%s%s", what, out, errOut)
		}
	}
	check("sync", "project", "sync")
	check("link", "project", "link", "moved", other)
	check("add", "project", "add", "busy", repo)
	if err := project.Clone(repo, home+"/Code/x"); err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("clone: %v", err)
	}
}

func TestProjectSyncAndTrust(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	must(t, "project", "dir", "~/Code")
	os.WriteFile(home+"/.claude.json", []byte("{}\n"), 0o600)
	repo := bareRepo(t, "app")
	register(t, home, "work", "app:\n  repo: "+repo+"\n  team: dev\n  about: a\nlocal:\n  team: dev\n  about: no repo\n  path: ~/nowhere/local\n")
	out := must(t, "project", "sync")
	for _, want := range []string{"+ Cloned app into ~/Code/app and trusted it", "! local isn't on this machine, and has no repo to clone. Link its folder with cadrei project link local <folder>"} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("sync lacks %q:\n%s", want, out)
		}
	}
	if out := must(t, "project", "sync"); !strings.Contains(out, "+ app is on this machine") {
		t.Errorf("second sync: %q", out)
	}
	out = must(t, "project", "trust", "--all")
	if !strings.Contains(out, "+ app was already trusted in Claude Code") || !strings.Contains(flat(out), "! local isn't on this machine, so it was skipped. Get it with cadrei project sync") {
		t.Errorf("trust --all: %q", out)
	}
	refused(t, "there's no project called nope in this crew", "project", "trust", "nope")
}

func TestTrustRefusals(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	os.WriteFile(home+"/.claude.json", []byte("{}\n"), 0o600)
	register(t, home, "work", "me:\n  path: ~\nteam:\n  path: ~/.cadrei/work/teams/dev\nnotgit:\n  path: ~/plain\n")
	os.MkdirAll(home+"/.cadrei/work/teams/dev", 0o755)
	os.MkdirAll(home+"/plain", 0o755)
	out := must(t, "project", "trust", "--all")
	for _, want := range []string{"me isn't trusted in Claude Code: it's your home folder.", "team isn't trusted in Claude Code: it's inside ~/.cadrei", "notgit isn't trusted in Claude Code: it is not the top folder of a git repository."} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("trust --all lacks %q:\n%s", want, out)
		}
	}
	if readFile(t, home+"/.claude.json") != "{}\n" {
		t.Error("a refused trust changed the config")
	}
}

// A shared registry's paths are checked like links: a project cannot be
// cloned where Claude Code or cadrei load files.
func TestSyncAndAddRefuseCadreisAndClaudesFolders(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	repo := bareRepo(t, "demo")
	register(t, home, "work", "demo:\n  repo: "+repo+"\n  path: ~/.claude/skills/demo\nmine:\n  repo: "+repo+"\n  path: ~/.cadrei/config/x\n")
	code, out, _ := call("project", "sync")
	if code == 0 || !strings.Contains(flat(out), "! Couldn't clone demo: it can't go into ~/.claude/skills/demo: it is inside ~/.claude.") || !strings.Contains(out, "Couldn't clone mine") {
		t.Errorf("sync: %d %q", code, out)
	}
	for _, p := range []string{home + "/.claude/skills/demo", home + "/.cadrei/config/x"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was made", p)
		}
	}
	refused(t, "can't hold projects: it is inside ~/.claude", "project", "dir", "~/.claude/skills")
	refused(t, "can't hold projects: it is inside ~/.cadrei", "project", "dir", "~/.cadrei/x")
	// A repo that looks like an option never reaches git: the command line
	// refuses it, and Clone refuses it too (its own test).
	refused(t, "there's no option --upload-pack=touch /tmp/x for cadrei project add", "project", "add", "evil", "--upload-pack=touch /tmp/x")
}

// Projects across machines: the registry is portable, the places are this
// machine's, and a missing project is never unlinked by cadrei.
func TestProjectLinkUnlinkAndMissing(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	must(t, "project", "dir", "~/Code")
	repo := bareRepo(t, "app")
	other := bareRepo(t, "other")
	must(t, "project", "add", "app", repo, "--no-trust")
	// The folder moves away: missing, and nothing unlinks it.
	os.MkdirAll(home+"/Moved", 0o755)
	os.Rename(home+"/Code/app", home+"/Moved/app")
	if out := must(t, "ls"); !strings.Contains(out, "\n  app   folder is gone · ask your orchestrator to find it\n") {
		t.Errorf("ls:\n%s", out)
	}
	refused(t, "app is missing: ~/Code/app is gone", "up", "dev/engineer", "app")
	refused(t, "app is missing: ~/Code/app is gone", "attach", "dev", "app")
	refused(t, "app is missing: ~/Code/app is gone", "project", "path", "app")
	// A clone of the same repo in the projects folder is suggested.
	exec.Command("git", "clone", "-q", repo, home+"/Code/app-copy").Run()
	var st cadreiStatus
	json.Unmarshal([]byte(must(t, "ls", "--json")), &st)
	if len(st.Projects) != 1 || st.Projects[0].State != "missing" || st.Projects[0].Found != home+"/Code/app-copy" {
		t.Errorf("ls --json: %+v", st.Projects)
	}
	// Link it where it is now.
	exec.Command("git", "clone", "-q", other, home+"/Moved/other").Run()
	refused(t, "is a clone of", "project", "link", "app", home+"/Moved/other")
	refused(t, "there's no project called nope in this crew", "project", "link", "nope", home+"/Moved/app")
	refused(t, "your home folder", "project", "link", "app", home)
	out := must(t, "project", "link", "app", home+"/Moved/app", "--no-trust")
	if !strings.Contains(out, "+ app is at ~/Moved/app on this machine") || strings.TrimSpace(must(t, "project", "path", "app")) != home+"/Moved/app" {
		t.Errorf("link: %q", out)
	}
	if strings.Contains(readFile(t, home+"/.cadrei/work/projects.yaml"), "Moved") {
		t.Error("a local path went into the registry")
	}
	// Members cannot change links.
	t.Setenv("CADREI_MEMBER", "x")
	refused(t, "team members can't link folders", "project", "link", "app", home+"/Moved/app")
	refused(t, "team members can't unlink projects", "project", "unlink", "app")
	os.Unsetenv("CADREI_MEMBER")
	// Unlink: out of the registry and the places, the folder kept, and
	// with --untrust out of the runtime's trust too.
	os.WriteFile(home+"/.claude.json", []byte("{}\n"), 0o600)
	must(t, "project", "trust", "app")
	out = must(t, "project", "unlink", "app", "--untrust")
	if !strings.Contains(out, "+ Removed app from this crew. Its folder ~/Moved/app stays.") || !strings.Contains(out, "app: untrusted") {
		t.Errorf("unlink: %q", out)
	}
	if _, err := os.Stat(home + "/Moved/app/.git"); err != nil {
		t.Error("unlink touched the folder")
	}
	if strings.Contains(readFile(t, home+"/.cadrei/work/projects.yaml"), "app:") || strings.Contains(readFile(t, home+"/.cadrei/work/.claude/local/places.json"), "app") {
		t.Error("app is still registered or placed")
	}
	if strings.Contains(readFile(t, home+"/.claude.json"), "hasTrustDialogAccepted\": true") {
		t.Error("--untrust left the trust")
	}
	if log, _ := exec.Command("git", "-C", home+"/.cadrei/work", "log", "-1", "--format=%s").Output(); strings.TrimSpace(string(log)) != "Remove project app" {
		t.Errorf("commit %q", log)
	}
	refused(t, "there's no project called app in this crew", "project", "unlink", "app")
}

// A registry from another machine: sync clones what is not here and uses a
// clone of the same repository already in the projects folder.
func TestSyncPlacesProjects(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	must(t, "project", "dir", "~/Code")
	app, blog := bareRepo(t, "app"), bareRepo(t, "blog")
	os.WriteFile(home+"/.cadrei/work/projects.yaml", []byte("app:\n  repo: "+app+"\nblog:\n  repo: "+blog+"\n  path: /other/machine/blog\n"), 0o644)
	exec.Command("git", "clone", "-q", app, home+"/Code/app").Run()
	if out := must(t, "ls"); !strings.Contains(out, "\n  app    not on this machine · ask your orchestrator to clone it\n  blog   not on this machine") {
		t.Errorf("ls before sync:\n%s", out)
	}
	out := must(t, "project", "sync", "--no-trust")
	if !strings.Contains(out, "+ Found app at ~/Code/app") || !strings.Contains(out, "+ Cloned blog into ~/Code/blog") {
		t.Errorf("sync: %q", out)
	}
	places := readFile(t, home+"/.cadrei/work/.claude/local/places.json")
	if !strings.Contains(places, `"app": "~/Code/app"`) || !strings.Contains(places, `"blog": "~/Code/blog"`) {
		t.Errorf("places:\n%s", places)
	}
	if out := must(t, "ls"); !strings.Contains(out, "\n  app    ~/Code/app\n  blog   ~/Code/blog\n") {
		t.Errorf("ls after sync:\n%s", out)
	}
}

// A registry can come from another machine or a shared backup, and a
// member with a shell can write it: a name that climbs out of the projects
// folder or nests is left out everywhere, with a warning, and nothing is
// cloned for it, whoever runs sync.
func TestRegistryNamesNeverLeaveTheProjectsFolder(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	must(t, "project", "dir", "~/Code")
	app := bareRepo(t, "app")
	bad := []string{"../.vim/pack/x/start/evil", "a/b", "/abs", "~/evil", ".hidden", "x..y"}
	var text string
	for _, n := range bad {
		text += n + ":\n  repo: " + app + "\n"
	}
	os.WriteFile(home+"/.cadrei/work/projects.yaml", []byte(text+"good:\n  repo: "+app+"\n"), 0o644)
	for _, member := range []string{"work-dev-engineer", ""} {
		t.Setenv("CADREI_MEMBER", member)
		code, out, errOut := call("project", "sync", "--no-trust")
		if code != 0 || !strings.Contains(out, "good") || strings.Contains(out, "evil") || strings.Contains(out, "a/b") {
			t.Errorf("sync (member %q): %d\n%s%s", member, code, out, errOut)
		}
		if !strings.Contains(flat(errOut), `projects.yaml has an entry named "../.vim/pack/x/start/evil", which can't be a project name`) {
			t.Errorf("no warning: %q", errOut)
		}
	}
	for _, p := range []string{home + "/.vim", home + "/Code/a", home + "/Code/abs", home + "/evil", home + "/Code/.hidden", home + "/Code/x..y"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was made", p)
		}
	}
	if places := readFile(t, home+"/.cadrei/work/.claude/local/places.json"); strings.Contains(places, "evil") || !strings.Contains(places, `"good"`) {
		t.Errorf("places:\n%s", places)
	}
	if out := must(t, "ls"); !strings.Contains(flat(out), `entry named "a/b", which can't be a project name`) {
		t.Errorf("ls:\n%s", out)
	}
	refused(t, "", "project", "path", "../.vim/pack/x/start/evil")
	refused(t, "", "project", "link", "a/b", home+"/Code/good")
}

// A folder whose origin only seems to name the project's host is not a
// clone of it: git reads the host as evil.example in each of these.
func TestLinkRefusesAnOriginThatHidesItsHost(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	os.WriteFile(home+"/.cadrei/work/projects.yaml", []byte("tool:\n  repo: acme/tool\n"), 0o644)
	for i, origin := range []string{"https://evil.example#@github.com/acme/tool", "https://evil.example?@github.com/acme/tool", `https://evil.example\@github.com/acme/tool`} {
		dir := filepath.Join(home, "src", "tool"+string(rune('a'+i)))
		os.MkdirAll(dir, 0o755)
		exec.Command("git", "-C", dir, "init", "-q").Run()
		exec.Command("git", "-C", dir, "remote", "add", "origin", origin).Run()
		refused(t, "is a clone of", "project", "link", "tool", dir, "--no-trust")
	}
	if places := readFile(t, home+"/.cadrei/work/.claude/local/places.json"); strings.Contains(places, "tool") {
		t.Errorf("a place was recorded:\n%s", places)
	}
}

// Audit sec-exposure-2: a folder whose origin has a user and password in
// it is linked with the URL cleaned: projects.yaml, its commit and ls
// --json never hold the secret.
func TestALinkedOriginsSecretIsLeftOut(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	dir := home + "/code/app"
	os.MkdirAll(dir, 0o755)
	exec.Command("git", "-C", dir, "init", "-q").Run()
	secret := "hunter" + "2-placeholder"
	exec.Command("git", "-C", dir, "remote", "add", "origin", "https://alice:"+secret+"@git.example.com/alice/app.git").Run()
	must(t, "project", "add", "app", "--path", dir, "--no-trust")
	c := home + "/.cadrei/work"
	t.Setenv("CADREI_HOME", c)
	history, _ := exec.Command("git", "-C", c, "log", "-p").Output()
	for name, text := range map[string]string{"projects.yaml": readFile(t, c+"/projects.yaml"), "ls --json": must(t, "ls", "--json"), "git log -p": string(history)} {
		if strings.Contains(text, secret) || strings.Contains(text, "alice:") {
			t.Errorf("%s holds the secret:\n%s", name, text)
		}
	}
	if !strings.Contains(readFile(t, c+"/projects.yaml"), "repo: https://git.example.com/alice/app.git") {
		t.Errorf("projects.yaml:\n%s", readFile(t, c+"/projects.yaml"))
	}
}

// Audit qual-bugs-1: a member's sync clones, but trusts nothing: only the
// user trusts folders.
func TestAMembersSyncTrustsNothing(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	must(t, "project", "dir", "~/Code")
	os.WriteFile(home+"/.claude.json", []byte("{}\n"), 0o600)
	app := bareRepo(t, "app")
	register(t, home, "work", "app:\n  repo: "+app+"\n")
	t.Setenv("CADREI_HOME", home+"/.cadrei/work")
	t.Setenv("CADREI_MEMBER", "work-dev-engineer")
	out := must(t, "project", "sync")
	if !strings.Contains(out, "+ Cloned app into ~/Code/app\n") || !strings.Contains(flat(out), "! app isn't trusted in Claude Code: team members can't trust folders.") {
		t.Errorf("sync:\n%s", out)
	}
	if cfg := readFile(t, home+"/.claude.json"); cfg != "{}\n" {
		t.Errorf("trust:\n%s", cfg)
	}
}

// No caller trusts a folder in a member's session.
func TestProjectTrustRefusesMembers(t *testing.T) {
	home := sandbox(t)
	os.WriteFile(home+"/.claude.json", []byte("{}\n"), 0o600)
	dir := home + "/code/app"
	os.MkdirAll(dir, 0o755)
	exec.Command("git", "-C", dir, "init", "-q").Run()
	rt, _ := runtime.Get(runtimeName())
	// Set, even empty, counts (review of fix round 7), and so does 0.1.x's
	// CADRE_PERSONA.
	for _, v := range []string{"CADREI_MEMBER", "CADRE_PERSONA"} {
		for _, value := range []string{"work-dev-engineer", ""} {
			t.Setenv(v, value)
			res, _ := project.Trust(rt, []runtime.Folder{{Name: "app", Dir: dir}})
			if res[0].State != "refused" || readFile(t, home+"/.claude.json") != "{}\n" {
				t.Errorf("%s=%q: trust: %+v", v, value, res)
			}
			os.Unsetenv(v)
		}
	}
}

// Acceptance 66: sync clones every project, and trusts only those without
// their own Claude Code settings.
func TestSyncTrustsOnlyCleanClones(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	must(t, "project", "dir", "~/Code")
	os.WriteFile(home+"/.claude.json", []byte("{}\n"), 0o600)
	app := bareRepo(t, "app")
	src := t.TempDir() + "/tools"
	exec.Command("git", "clone", "-q", app, src).Run()
	os.WriteFile(src+"/.mcp.json", []byte("{}"), 0o644)
	exec.Command("git", "-C", src, "add", "-A").Run()
	exec.Command("git", "-C", src, "commit", "-qm", "tools").Run()
	tools := t.TempDir() + "/tools.git"
	exec.Command("git", "clone", "-q", "--bare", src, tools).Run()
	register(t, home, "work", "app:\n  repo: "+app+"\ntools:\n  repo: "+tools+"\n")
	out := must(t, "project", "sync")
	if !strings.Contains(out, "+ Cloned app into ~/Code/app and trusted it") ||
		!strings.Contains(flat(out), "! Cloned tools into ~/Code/tools. It has its own Claude Code settings (.mcp.json), so it isn't trusted yet. Ask your orchestrator to trust it if you trust it.") {
		t.Errorf("sync:\n%s", out)
	}
	cfg := readFile(t, home+"/.claude.json")
	if !strings.Contains(cfg, home+"/Code/app") || strings.Contains(cfg, home+"/Code/tools") {
		t.Errorf("trust:\n%s", cfg)
	}
}

// Review 2: project add (a clone or --path) and project link don't trust a
// repo with its own Claude Code settings; project trust still can.
func TestAProjectWithItsOwnSettingsIsNotTrusted(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	must(t, "project", "dir", "~/Code")
	cfg := home + "/.claude.json"
	os.WriteFile(cfg, []byte("{}\n"), 0o600)
	src := filepath.Join(t.TempDir(), "hooked")
	os.MkdirAll(src+"/.claude", 0o755)
	exec.Command("git", "-C", src, "init", "-q", "-b", "main").Run()
	// A settings file with no hooks in it: its presence is what counts.
	os.WriteFile(src+"/.claude/settings.json", []byte("{}\n"), 0o644)
	exec.Command("git", "-C", src, "add", "-A").Run()
	exec.Command("git", "-C", src, "commit", "-qm", "first").Run()

	out := must(t, "project", "add", "hooked", src)
	if !strings.Contains(flat(out), "! hooked has its own Claude Code settings (.claude/settings.json), which would run for your team once it's trusted. So it isn't trusted yet.") {
		t.Errorf("add a clone:\n%s", out)
	}
	if strings.Contains(readFile(t, cfg), "hooked") {
		t.Error("the clone was trusted")
	}
	out = must(t, "project", "add", "mine", "--path", src)
	if !strings.Contains(flat(out), "isn't trusted yet") || strings.Contains(readFile(t, cfg), "hooked") {
		t.Errorf("add --path:\n%s", out)
	}
	must(t, "project", "unlink", "mine")
	out = must(t, "project", "link", "hooked", src)
	if !strings.Contains(flat(out), "isn't trusted yet") || strings.Contains(readFile(t, cfg), src) {
		t.Errorf("link:\n%s", out)
	}
	if out := must(t, "project", "trust", "hooked"); !strings.Contains(out, "hooked is trusted") {
		t.Errorf("an explicit trust: %s", out)
	}
}

// Audit sec-trust-1: a skill deep in a repo counts as its own settings,
// and the note names where it is.
func TestANestedSkillIsNotTrusted(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	cfg := home + "/.claude.json"
	os.WriteFile(cfg, []byte("{}\n"), 0o600)
	src := home + "/code/nest"
	os.MkdirAll(src+"/packages/web/.claude/skills/x", 0o755)
	exec.Command("git", "-C", src, "init", "-q").Run()
	os.WriteFile(src+"/packages/web/.claude/skills/x/SKILL.md", []byte("---\nname: x\nallowed-tools: Bash(make:*)\n---\n"), 0o644)
	out := must(t, "project", "add", "nest", "--path", src)
	if !strings.Contains(flat(out), "! nest has its own Claude Code settings (packages/web/.claude/skills), which would run for your team once it's trusted.") {
		t.Errorf("add:\n%s", out)
	}
	if strings.Contains(readFile(t, cfg), "nest") {
		t.Error("the folder was trusted")
	}
}

// Review round 3, item 3, end to end: a clone whose skill is a link, or
// whose key is quoted, isn't trusted.
func TestAClonedProjectCantTalkItsWayIntoTrust(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	must(t, "project", "dir", "~/Code")
	cfg := home + "/.claude.json"
	os.WriteFile(cfg, []byte("{}\n"), 0o600)
	for name, write := range map[string]func(src string){
		"linked": func(src string) {
			os.MkdirAll(src+"/docs", 0o755)
			os.WriteFile(src+"/docs/skill.md", []byte("---\nallowed-tools: Bash(make:*)\n---\n"), 0o644)
			os.MkdirAll(src+"/.claude/skills/x", 0o755)
			os.Symlink("../../../docs/skill.md", src+"/.claude/skills/x/SKILL.md")
		},
		"quoted": func(src string) {
			os.MkdirAll(src+"/.claude/skills/x", 0o755)
			os.WriteFile(src+"/.claude/skills/x/SKILL.md", []byte("---\n\"allowed-tools\" : Bash(make:*)\n---\n"), 0o644)
		},
	} {
		src := filepath.Join(t.TempDir(), name)
		os.MkdirAll(src, 0o755)
		exec.Command("git", "-C", src, "init", "-q", "-b", "main").Run()
		write(src)
		exec.Command("git", "-C", src, "add", "-A").Run()
		exec.Command("git", "-C", src, "commit", "-qm", "first").Run()
		if out := must(t, "project", "add", name, src); !strings.Contains(flat(out), "So it isn't trusted yet.") {
			t.Errorf("%s:\n%s", name, out)
		}
		if strings.Contains(readFile(t, cfg), "/"+name) {
			t.Errorf("%s was trusted", name)
		}
	}
}

// Docs consistency audit: what a member's sync suggests is a command that
// works: project trust takes one name, so several get one each.
func TestAMembersUntrustedProjectsGetCommandsThatWork(t *testing.T) {
	if got := flat(render(untrustedByMember([]string{"my-app"}, "Claude Code"), false)); got != "my-app isn't trusted in Claude Code: team members can't trust folders. Ask the user to run cadrei project trust my-app." {
		t.Errorf("one: %q", got)
	}
	if got := flat(render(untrustedByMember([]string{"my-app", "blog"}, "Claude Code"), false)); got != "my-app and blog aren't trusted in Claude Code: team members can't trust folders. Ask the user to run cadrei project trust my-app, then cadrei project trust blog." {
		t.Errorf("several: %q", got)
	}
}
