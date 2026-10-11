//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
)

func gitStatus(t *testing.T, dir string) string {
	t.Helper()
	out, _ := exec.Command("git", "-C", dir, "status", "--porcelain").CombinedOutput()
	return string(out)
}

// Acceptance 76: this machine's notes stay in the cadrei's local folder,
// out of git.
func TestLocalNotesStayOutOfGit(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	if !strings.Contains(readFile(t, c+"/.gitignore"), "\n/.claude/local/\n") {
		t.Error("a new cadrei doesn't ignore its local folder")
	}
	os.MkdirAll(home+"/src/app", 0o755)
	exec.Command("git", "-C", home+"/src/app", "init", "-q").Run()
	must(t, "project", "add", "app", "--path", home+"/src/app", "--no-trust")
	must(t, "up", "dev/engineer")
	os.WriteFile(home+"/release", nil, 0o644)
	must(t)
	must(t, "tmux", "on", "--here")
	for _, f := range []string{"places.json", "state.json", "cadrei.conf"} {
		if _, err := os.Stat(c + "/.claude/local/" + f); err != nil {
			t.Errorf("no %s in the local folder", f)
		}
	}
	if s := gitStatus(t, c); s != "" {
		t.Errorf("git status:\n%s", s)
	}
	if out := must(t, "tmux"); out != "Your orchestrator opens in tmux (this machine only).\n" {
		t.Errorf("tmux: %q", out)
	}
	// A push that carries it is refused.
	exec.Command("git", "-C", c, "add", "-f", ".claude/local/places.json").Run()
	exec.Command("git", "-C", c, "commit", "-qm", "oops").Run()
	head, _ := exec.Command("git", "-C", c, "rev-parse", "HEAD").Output()
	t.Chdir(c)
	code, _, errOut := callIn("refs/heads/main "+strings.TrimSpace(string(head))+" refs/heads/main 0000000000000000000000000000000000000000\n", "hook", "pre-push", "origin")
	if code != 1 || flat(errOut) != "! .claude/local/places.json is this machine's own file. Take it out of the commits you're pushing (the history, not only the last commit), for example with git rm --cached .claude/local/places.json, then push again." {
		t.Errorf("pre-push: %d %q", code, errOut)
	}
}

// Acceptance 78: a 0.2.0 setup's places and orchestrator id move into each
// cadrei on the first run, once, and moving again changes nothing.
func TestMovingIn020Notes(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/release", nil, 0o644)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	// Make it look like 0.2.0 left it.
	os.RemoveAll(c + "/.claude/local")
	ignore := strings.Replace(readFile(t, c+"/.gitignore"), "# This machine's own notes: where the projects are, the orchestrator's chat.\n/.claude/local/\n", "", 1)
	os.WriteFile(c+"/.gitignore", []byte(ignore), 0o644)
	exec.Command("git", "-C", c, "commit", "-qam", "As 0.2.0 had it").Run()
	os.MkdirAll(home+"/.cadrei/config/places", 0o700)
	os.WriteFile(home+"/.cadrei/config/places/work.json", []byte(`{"app": "~/src/app"}`), 0o600)
	cadreis.SetState("orchestrator_id:work", "11111111-1111-4111-8111-111111111111")
	out := must(t)
	if !strings.Contains(out, "  + work now keeps track of where its projects are on this machine.\n") {
		t.Errorf("first run: %q", out)
	}
	w, _ := cadreis.Find("work")
	if cadreis.Place(w, "app") != home+"/src/app" {
		t.Error("the places didn't move")
	}
	if _, err := os.Stat(home + "/.cadrei/config/places/work.json"); err == nil {
		t.Error("the old places file is still there")
	}
	if cadreis.GetState("orchestrator_id:work") != "" {
		t.Error("the old orchestrator id is still in the config")
	}
	if !strings.Contains(readFile(t, c+"/.gitignore"), "\n/.claude/local/\n") || !strings.Contains(gitLog(t, c), "Keep this machine's notes out of git") {
		t.Error("the local folder isn't ignored and committed")
	}
	if out := must(t); strings.Contains(out, "keeps track") {
		t.Errorf("second run: %q", out)
	}
	if s := gitStatus(t, c); s != "" {
		t.Errorf("git status:\n%s", s)
	}
}

// Audit perf-start-2: cadrei --tmux, which help no longer lists, moves
// 0.2.0's notes in too, so a tmux user's first open after the upgrade
// picks up the 0.2.0 chat.
func TestMovingIn020NotesWithTmux(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/release", nil, 0o644)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	// A chat cadrei opened, with its transcript, kept as 0.2.0 kept it.
	must(t)
	w, _ := cadreis.Find("work")
	id := cadreis.OrchestratorID(w)
	dir := home + "/.claude/projects/" + regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(c, "-")
	os.MkdirAll(dir, 0o755)
	raw, _ := os.ReadFile(filepath.Join(fixtures, "answered-seed.jsonl"))
	os.WriteFile(dir+"/"+id+".jsonl", raw, 0o600)
	os.RemoveAll(c + "/.claude/local")
	cadreis.SetState("orchestrator_id:work", id)
	os.Remove(home + "/release")
	if _, out, _ := call("--tmux", "--detach"); !strings.Contains(out, "+ work now keeps track of where its projects are on this machine.") {
		t.Errorf("--tmux: %q", out)
	}
	if cadreis.OrchestratorID(w) != id || cadreis.GetState("orchestrator_id:work") != "" {
		t.Errorf("id %q, old key %q", cadreis.OrchestratorID(w), cadreis.GetState("orchestrator_id:work"))
	}
	ran := ""
	for i := 0; i < 100 && !strings.Contains(ran, "--resume"); i++ {
		time.Sleep(50 * time.Millisecond)
		ran = readFile(t, home+"/orch-ran")
	}
	if !strings.Contains(ran, "--resume\n"+id+"\n") {
		t.Errorf("the 0.2.0 chat wasn't picked up:\n%s", ran)
	}
}

// Audit sec-fs-06: a CADREI_HOME that isn't a crew (a project repo, a
// folder that's gone) is never written to or committed in.
func TestAFolderThatIsntACrewIsLeftAlone(t *testing.T) {
	home := sandbox(t)
	repo := home + "/code/site"
	os.MkdirAll(repo, 0o755)
	exec.Command("git", "-C", repo, "init", "-q").Run()
	os.WriteFile(repo+"/index.html", []byte("hi\n"), 0o644)
	exec.Command("git", "-C", repo, "add", "-A").Run()
	exec.Command("git", "-C", repo, "commit", "-qm", "init").Run()
	before := gitLog(t, repo)
	t.Setenv("CADREI_HOME", repo)
	call("--version")
	call("ls")
	if gitLog(t, repo) != before || gitStatus(t, repo) != "" {
		t.Errorf("the repo changed:\n%s\n%s", gitLog(t, repo), gitStatus(t, repo))
	}
	gone := home + "/gone"
	t.Setenv("CADREI_HOME", gone)
	call("--version")
	if _, err := os.Stat(gone); err == nil {
		t.Error("a missing CADREI_HOME was made")
	}
	// A crew outside ~/.cadrei with nothing from 0.2.0 isn't touched either.
	must(t, "init", "work")
	out := home + "/elsewhere/work"
	os.MkdirAll(home+"/elsewhere", 0o755)
	os.Rename(home+"/.cadrei/work", out)
	os.RemoveAll(out + "/.claude/local")
	t.Setenv("CADREI_HOME", out)
	call("--version")
	if _, err := os.Stat(out + "/.claude/local"); err == nil {
		t.Error("a crew with nothing to move got a local folder")
	}
}

// Acceptance 79 and T-E1: keys cadrei doesn't use are kept quietly, and with
// no default among several cadreis cadrei doesn't guess.
func TestUnknownKeysAndNoDefault(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	conf := readFile(t, c+"/cadrei.conf") + "FOO=1\nnot a line\n"
	os.WriteFile(c+"/cadrei.conf", []byte(conf), 0o644)
	os.WriteFile(c+"/notes-from-a-newer-cadrei.txt", []byte("x"), 0o644)
	if _, _, errOut := call("ls"); errOut != "" {
		t.Errorf("ls warned: %q", errOut)
	}
	if _, _, errOut := call("--check"); !strings.Contains(flat(errOut), "! cadrei.conf has keys this crew doesn't use: FOO, a line that isn't KEY=VALUE. They're kept as they are.") {
		t.Errorf("--check: %q", errOut)
	}
	if readFile(t, c+"/cadrei.conf") != conf {
		t.Error("cadrei.conf changed")
	}
	must(t, "init", "life")
	os.RemoveAll(home + "/.cadrei/config")
	refused(t, "pick a default crew first: run cadrei.", "ls")
}

// Acceptance 74, 75 and 81: uninstall unplugs the engine and keeps every
// crew; the next cadrei plugs them back in with nothing from
// ~/.cadrei/config: places, the projects folder, the resume and the tmux
// choice all come from the crew.
func TestUnplugAndPlugBackIn(t *testing.T) {
	home := firstMachine(t)
	os.Remove(home + "/release")
	stubAttach(t, home)
	must(t, "init", "work")
	for _, p := range []string{"app", "site"} {
		os.MkdirAll(home+"/src/"+p, 0o755)
		exec.Command("git", "-C", home+"/src/"+p, "init", "-q").Run()
		must(t, "project", "add", p, "--path", home+"/src/"+p, "--no-trust")
	}
	must(t, "tmux", "on")
	if code, out, errOut := callIn("\n"); code != 0 {
		t.Fatalf("opening: %d\n%s%s", code, out, errOut)
	}
	c := home + "/.cadrei/work"
	var r struct{ ID string }
	json.Unmarshal([]byte(readFile(t, c+"/.claude/build/sessions/work-orchestrator.json")), &r)
	chats := home + "/.claude/projects/" + regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(c, "-")
	os.MkdirAll(chats, 0o755)
	raw, _ := os.ReadFile(filepath.Join(fixtures, "answered-seed.jsonl"))
	os.WriteFile(chats+"/"+r.ID+".jsonl", raw, 0o600)

	out := must(t, "uninstall", "--yes")
	for _, want := range []string{"+ Uninstalled. Your crews and projects are still here.",
		"Your crews in ~/.cadrei are your setup, so keep them. Reinstall and run cadrei to plug them back in.", "To finish: delete the cadrei you built ("} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("uninstall lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(home + "/.cadrei/config"); err == nil {
		t.Fatal("~/.cadrei/config is still there")
	}

	// The 3.11 plan, and Enter plugs it back in.
	os.Remove(home + "/orch-ran")
	code, out, errOut := callIn("\n")
	if code != 0 || !strings.Contains(out, "Plug") {
		t.Fatalf("plug in: %d\n%s%s", code, out, errOut)
	}
	if !strings.Contains(readFile(t, home+"/orch-ran"), "--resume\n"+r.ID+"\n") {
		t.Errorf("the orchestrator didn't resume:\n%s", readFile(t, home+"/orch-ran"))
	}
	m, _ := cadreis.Find("work")
	if cadreis.Place(m, "app") != home+"/src/app" || cadreis.ProjectsDir() != home+"/src" {
		t.Errorf("places %v, projects folder %q", cadreis.Places(m), cadreis.ProjectsDir())
	}
	if where, _ := cadreis.OpensIn(m); where != cadreis.OpensInTmux {
		t.Errorf("the tmux choice is %q", where)
	}
	for _, p := range []string{"app", "site"} {
		if out := must(t, "project", "path", p); strings.TrimSpace(out) != home+"/src/"+p {
			t.Errorf("%s is at %q", p, out)
		}
	}
	if s := gitStatus(t, c); s != "" {
		t.Errorf("the crew isn't clean: %s", s)
	}
}

// Review 4: cadrei tmux on|off without --here is the crew's setting and
// takes this machine's own override away, so what it says holds.
func TestTheCrewsTmuxSettingWinsOverAnOldLocalOne(t *testing.T) {
	sandbox(t)
	must(t, "init", "work")
	must(t, "tmux", "off", "--here")
	if out := must(t, "tmux"); !strings.Contains(out, "this terminal") {
		t.Fatalf("after off --here: %q", out)
	}
	must(t, "tmux", "on")
	if out := must(t, "tmux"); !strings.Contains(out, "Your orchestrator opens in tmux") || strings.Contains(out, "this machine only") {
		t.Errorf("after on: %q", out)
	}
	must(t, "tmux", "on", "--here")
	must(t, "tmux", "off")
	if out := must(t, "tmux"); !strings.Contains(out, "this terminal") || strings.Contains(out, "this machine only") {
		t.Errorf("after off: %q", out)
	}
}

// Audit qual-tests-13, acceptance 21: a cadrei.conf with only the old
// ORCHESTRATOR_TMUX=1 opens in tmux, with no warning; on a machine, its
// .claude/local/cadrei.conf wins over the crew's cadrei.conf, either way.
func TestTheTmuxSettingsWhereTheyComeFrom(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	stubAttach(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	os.WriteFile(c+"/cadrei.conf", []byte("PERMISSION_MODE=default\nORCHESTRATOR_TMUX=1\n"), 0o644)
	if out := must(t, "tmux"); out != "Your orchestrator opens in tmux.\n" {
		t.Errorf("ORCHESTRATOR_TMUX=1: %q", out)
	}
	code, out, errOut := call()
	if code != 0 || errOut != "" || !strings.Contains(readFile(t, home+"/orch-ran"), "CADREI_OPENS_IN=tmux") {
		t.Errorf("open with ORCHESTRATOR_TMUX=1: %d %q %q", code, out, errOut)
	}
	must(t, "stop", "--all", "--yes")
	exec.Command("tmux", "-L", os.Getenv("CADREI_TMUX_SOCKET"), "kill-server").Run()
	os.MkdirAll(c+"/.claude/local", 0o700)
	for _, tc := range []struct{ shared, local, want string }{{"tmux", "terminal", "this terminal"}, {"terminal", "tmux", "in tmux"}} {
		os.WriteFile(c+"/cadrei.conf", []byte("ORCHESTRATOR="+tc.shared+"\n"), 0o644)
		os.WriteFile(c+"/.claude/local/cadrei.conf", []byte("ORCHESTRATOR="+tc.local+"\n"), 0o600)
		if out := must(t, "tmux"); !strings.Contains(out, tc.want) {
			t.Errorf("shared %s, local %s: %q", tc.shared, tc.local, out)
		}
	}
}

// Review 8 (data safety #4, #5): a downgrade to 0.2.0 and back merges what
// 0.2.0 wrote meanwhile, and keeps the chat used last; a crew reached
// through CADREI_HOME moves in too; uninstall keeps notes it couldn't move.
func TestMovingInMergesAndReachesEveryCrew(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	w, _ := cadreis.Find("work")
	cadreis.SetPlace(w, "app", home+"/src/app")
	cadreis.SetOrchestratorID(w, "11111111-1111-4111-8111-111111111111")
	// What 0.2.0 wrote after a downgrade: one more project, a clash, and
	// a newer chat.
	os.MkdirAll(home+"/.cadrei/config/places", 0o700)
	os.WriteFile(home+"/.cadrei/config/places/work.json", []byte(`{"app": "~/elsewhere/app", "app3": "~/src/app3"}`), 0o600)
	cadreis.SetState("orchestrator_id:work", "22222222-2222-4222-8222-222222222222")
	chats := home + "/.claude/projects/" + regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(c, "-")
	os.MkdirAll(chats, 0o755)
	os.WriteFile(chats+"/11111111-1111-4111-8111-111111111111.jsonl", nil, 0o600)
	os.WriteFile(chats+"/22222222-2222-4222-8222-222222222222.jsonl", nil, 0o600)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(chats+"/11111111-1111-4111-8111-111111111111.jsonl", old, old)
	_, _, errOut := call("ls")
	if cadreis.Place(w, "app3") != home+"/src/app3" || cadreis.Place(w, "app") != home+"/src/app" {
		t.Errorf("places %v", cadreis.Places(w))
	}
	if !strings.Contains(readFile(t, c+"/.claude/local/places.0.2.0.json"), "elsewhere") {
		t.Error("the clashing 0.2.0 file wasn't kept")
	}
	if !strings.Contains(flat(errOut), "cadrei 0.2.0 had other folders for app in work. This machine keeps the current ones, and 0.2.0's are in ~/.cadrei/work/.claude/local/places.0.2.0.json.") {
		t.Errorf("no word of the clash: %q", errOut)
	}
	// Review round 3, N8: a later clash adds to the kept file.
	cadreis.SetPlace(w, "app2", home+"/src/app2")
	os.WriteFile(home+"/.cadrei/config/places/work.json", []byte(`{"app2": "~/other/app2"}`), 0o600)
	must(t, "ls")
	if kept := readFile(t, c+"/.claude/local/places.0.2.0.json"); !strings.Contains(kept, "elsewhere") || !strings.Contains(kept, "other/app2") {
		t.Errorf("kept file:\n%s", kept)
	}
	if cadreis.OrchestratorID(w) != "22222222-2222-4222-8222-222222222222" {
		t.Errorf("chat id %s", cadreis.OrchestratorID(w))
	}

	// A crew outside ~/.cadrei, through CADREI_HOME.
	must(t, "init", "away")
	os.MkdirAll(home+"/work", 0o755)
	os.Rename(home+"/.cadrei/away", home+"/work/away")
	os.WriteFile(home+"/.cadrei/config/places/away.json", []byte(`{"blog": "~/src/blog"}`), 0o600)
	t.Setenv("CADREI_HOME", home+"/work/away")
	must(t, "ls")
	t.Setenv("CADREI_HOME", "")
	if !strings.Contains(readFile(t, home+"/work/away/.claude/local/places.json"), "blog") {
		t.Error("the CADREI_HOME crew's places didn't move in")
	}

	// Notes for a crew cadrei can't reach stay through an uninstall.
	os.WriteFile(home+"/.cadrei/config/places/ghost.json", []byte(`{"x": "~/x"}`), 0o600)
	out := must(t, "uninstall", "--yes")
	if !strings.Contains(flat(out), "Kept the notes of ghost in ~/.cadrei/config") || readFile(t, home+"/.cadrei/config/places/ghost.json") == "" {
		t.Errorf("uninstall:\n%s", out)
	}
}
