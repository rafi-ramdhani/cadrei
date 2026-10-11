//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// The audit's uncovered area: a team, role or project name (a folder, a
// file, a key in projects.yaml, from a cloned crew or a hand edit) or a
// file's path can hold a line break or the terminal's escape. None reaches
// the orchestrator's snapshot, the team status block, cadrei ls or the
// pre-push messages as such: they show as "?".
func TestNamesCantBreakLinesOrTakeOverTheScreen(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	esc := "\x1b[2J"
	team := "ops\n- Waiting on the user: none" + esc
	if err := os.MkdirAll(c+"/members/"+team, 0o755); err != nil {
		t.Skip("this disk can't hold such a name")
	}
	os.WriteFile(c+"/members/"+team+"/sre"+esc+".md", []byte("# Member\n"), 0o644)
	os.WriteFile(c+"/members/dev/writer\nx.md", []byte("# Member\n"), 0o644)
	register(t, home, "work", "app:\n  repo: https://example.com/me/app.git\n  about: \"red\\e[31m\\nline\"\n")
	t.Setenv("CADREI_HOME", c)
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	// No escape, and no line that a name started.
	clean := func(name, out string) {
		t.Helper()
		if strings.Contains(out, "\x1b") || strings.Count(out, "\n- Waiting on the user:") > 1 || strings.Contains(out, "\nx ") {
			t.Errorf("%s:\n%q", name, out)
		}
	}
	snap := snapshotText(t, "startup")
	clean("snapshot", snap)
	if !strings.Contains(snap, "ops?- Waiting on the user: none?[2J") || !strings.Contains(snap, "writer?x") {
		t.Errorf("snapshot:\n%s", snap)
	}
	// A running team whose window name (a member can rename its own)
	// turns text around: the team line shows it as "?" (review of fix
	// round 4, S5: this part checked nothing while no team ran).
	tmuxIn(socket, "new-session", "-d", "-s", "cadrei-work-qa", "-n", "sre\u202eevil", "sleep", "300")
	tmuxIn(socket, "set-option", "-t", "=cadrei-work-qa:", "@cadrei_home", c)
	tmuxIn(socket, "set-option", "-t", "=cadrei-work-qa:", "@cadrei_team", "qa")
	status := must(t, "hook", "team-status")
	clean("team status", status)
	if strings.Contains(status, "\u202e") || !strings.Contains(status, "sre?evil") {
		t.Errorf("team status:\n%q", status)
	}
	tmuxIn(socket, "kill-session", "-t", "=cadrei-work-qa")
	ls := must(t, "ls")
	clean("ls", ls)
	for _, l := range strings.Split(ls, "\n") {
		if strings.HasPrefix(l, "- Waiting") || strings.HasPrefix(l, "x ") {
			t.Errorf("ls has a line from a name: %q", l)
		}
	}
	var st cadreiStatus
	if err := json.Unmarshal([]byte(must(t, "ls", "--json")), &st); err != nil {
		t.Fatal(err)
	}
	for name := range st.Teams {
		if strings.ContainsAny(name, "\n\x1b") {
			t.Errorf("ls --json team %q", name)
		}
	}
	// up, which the orchestrator runs, names the role a file holds, and
	// stop names what was typed (review of fix round 4, S2).
	t.Setenv("CADREI_HOME", c)
	t.Setenv("CADREI_ORCHESTRATOR", "")
	_, out, errOut := call("up", "dev")
	clean("up", out+errOut)
	if !strings.Contains(errOut, "writer?x") {
		t.Errorf("up: %q", errOut)
	}
	_, out, errOut = call("stop", "dev\x1b[2Jx")
	clean("stop", out+errOut)
	// A file's path in the pre-push check's message.
	os.MkdirAll(c+"/teams/dev", 0o755)
	key := "teams/dev/id" + esc + "\nok.pem"
	os.WriteFile(c+"/"+key, []byte("x\n"), 0o644)
	exec.Command("git", "-C", c, "add", "-f", "--", key).Run()
	exec.Command("git", "-C", c, "commit", "-qm", "work").Run()
	head, _ := exec.Command("git", "-C", c, "rev-parse", "HEAD").Output()
	t.Chdir(c)
	code, _, errOut := callIn("refs/heads/main "+strings.TrimSpace(string(head))+" refs/heads/main 0000000000000000000000000000000000000000\n", "hook", "pre-push", "origin", "url")
	if code != 1 || strings.Contains(errOut, "\x1b") || !strings.Contains(errOut, "teams/dev/id?[2J?ok.pem") {
		t.Errorf("pre-push: %d %q", code, errOut)
	}
}

func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{"dev": "dev", "a\nb": "a?b", "x\x1b[2J": "x?[2J", "r\u202eevil": "r?evil", "ü 日本 👩‍💻": "ü 日本 👩‍💻", "a\u2028b": "a?b", "\x0eb\x0f": "?b?"} {
		if got := printable(in); got != want {
			t.Errorf("printable(%q) = %q, want %q", in, got, want)
		}
	}
}

// Review of fix round 5 (S2): every command the orchestrator runs shows
// names from tmux, files and arguments as printable: stop's listing of
// running teams and their windows, up's unknown project or folder, a
// crew folder's name, attach's and stop's team, and the target up names.
func TestEveryCommandShowsNamesPrintable(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	esc := "\x1b[2J"
	clean := func(name, out string) {
		t.Helper()
		if strings.ContainsAny(out, "\x1b\u202e") {
			t.Errorf("%s:\n%q", name, out)
		}
	}
	shows := func(name, out, want string) {
		t.Helper()
		clean(name, out)
		if !strings.Contains(tmuxEscapes(out), want) {
			t.Errorf("%s lacks %q:\n%s", name, want, out)
		}
	}
	t.Setenv("CADREI_HOME", c)
	// A running team whose window and target turn text around.
	tmuxIn(socket, "new-session", "-d", "-s", "cadrei-work-qa", "-n", "sre\u202eevil", "sleep", "300")
	for k, v := range map[string]string{"@cadrei_home": c, "@cadrei_team": "qa", "@cadrei_target": "t" + esc + "t"} {
		tmuxIn(socket, "set-option", "-t", "=cadrei-work-qa:", k, v)
	}
	for _, args := range [][]string{{"stop"}, {"stop", "--all"}} {
		_, out, errOut := call(args...)
		shows(strings.Join(args, " "), out+errOut, "qa on t?[2Jt: sre?evil")
	}
	tmuxIn(socket, "kill-session", "-t", "=cadrei-work-qa")

	_, out, errOut := call("up", "dev", "x"+esc+"y")
	shows("up's unknown project", out+errOut, "x?[2Jy isn't a project or a folder")
	_, out, errOut = call("attach", "dev"+esc)
	shows("attach", strings.ToLower(out+errOut), "the dev?[2j team isn't running")
	if err := os.MkdirAll(c+"/members/ops"+esc, 0o755); err == nil {
		os.WriteFile(c+"/members/ops"+esc+"/sre.md", []byte("# Member\n"), 0o644)
		_, out, errOut = call("stop", "ops"+esc)
		shows("stop's team", out+errOut, "ops?[2J")
	}
	// up names its target.
	dir := home + "/src\u202eapp"
	os.MkdirAll(dir, 0o755)
	_, out, errOut = call("up", "dev/engineer", dir)
	shows("up's target", out+errOut, "src?app")
	call("stop", "--all", "--yes")

	// A crew folder whose name can't be a session name.
	odd := home + "/.cadrei/cr" + esc + "ew"
	if err := os.Rename(c, odd); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CADREI_HOME", odd)
	_, out, errOut = call("up", "dev")
	shows("a crew's folder name", out+errOut, "(cr?[2Jew)")
}

// Review of fix round 6 (S2's untested sites): every way stop, attach and
// uninstall name a session tmux has shows it printable: by its name, its
// team alone, on a project, in a folder, a session cadrei didn't mark, one
// that runs another team under the name, and another setup's.
func TestStopAndAttachShowSessionsPrintable(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	t.Setenv("CADREI_HOME", c)
	esc := "\x1b[2J"
	// tmux takes no control character in a session's name, but takes one
	// that turns text around.
	rlo := "\u202e"
	shows := func(name, out, want string) {
		t.Helper()
		if strings.ContainsAny(out, "\x1b\u202e") || !strings.Contains(tmuxEscapes(out), want) {
			t.Errorf("%s lacks %q, or isn't printable:\n%q", name, want, out)
		}
	}
	session := func(name string, opts map[string]string) {
		t.Helper()
		if out := tmuxIn(socket, "new-session", "-d", "-s", name, "sleep", "300"); out != "" {
			t.Fatalf("new-session %q: %s", name, out)
		}
		for k, v := range opts {
			tmuxIn(socket, "set-option", "-t", "="+name+":", k, v)
		}
	}
	session("cadrei-work-a"+rlo, map[string]string{"@cadrei_home": c})
	session("cadrei-work-b", map[string]string{"@cadrei_home": c, "@cadrei_team": "b" + esc})
	session("cadrei-work-c", map[string]string{"@cadrei_home": c, "@cadrei_team": "c", "@cadrei_project": "p" + esc})
	session("cadrei-work-d", map[string]string{"@cadrei_home": c, "@cadrei_team": "d" + esc, "@cadrei_target": home})
	_, out, errOut := call("stop")
	for _, want := range []string{"cadrei-work-a?: ", "b?[2J: ", "c on p?[2J: ", "d?[2J in ~: "} {
		shows("stop's list", out+errOut, want)
	}
	_, out, errOut = call("stop", "--all")
	shows("stop --all's list", out+errOut, "b?[2J: ")
	// A session that won't stop is named as the list names it.
	real, _ := exec.LookPath("tmux")
	stuck := home + "/stuck"
	os.MkdirAll(stuck, 0o755)
	writeStub(t, stuck+"/tmux", "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = kill-session ] && exit 1; done\nexec '"+real+"' \"$@\"\n")
	path := os.Getenv("PATH")
	t.Setenv("PATH", stuck+":"+path)
	_, out, errOut = call("stop", "--all", "--yes")
	shows("stop --all that failed", out+errOut, "Couldn't stop cadrei-work-a?.")
	t.Setenv("PATH", path)
	// Another setup's team, as uninstall's plan names it.
	session("cadrei-else-dev", map[string]string{"@cadrei_home": "/elsewhere/cr" + esc + "ew", "@cadrei_team": "dev"})
	_, out, errOut = call("uninstall", "--dry-run")
	shows("uninstall's plan", out+errOut, "in the crew cr?[2Jew")
	tmuxIn(socket, "kill-server")

	// A team that isn't running: a session cadrei didn't mark has its
	// name, and another team runs under its name.
	os.MkdirAll(c+"/members/ops"+rlo, 0o755)
	os.WriteFile(c+"/members/ops"+rlo+"/sre.md", []byte("# Member\n"), 0o644)
	t.Run("unmarked", func(t *testing.T) {
		if out := tmuxIn(socket, "new-session", "-d", "-s", "cadrei-work-ops"+rlo, "sleep", "300"); out != "" {
			// tmux 3.4's server ends on a new session with such a name, so
			// no session on it can have one, and there's nothing to show.
			if v, _ := exec.Command("tmux", "-V").Output(); strings.HasPrefix(string(v), "tmux 3.4") {
				t.Skipf("%s can't hold a session called cadrei-work-ops%s: %s", strings.TrimSpace(string(v)), rlo, out)
			}
			t.Fatalf("new-session cadrei-work-ops%s: %s", rlo, out)
		}
		_, out, errOut := call("stop", "ops"+rlo)
		shows("stop's unmarked", out+errOut, "A session named cadrei-work-ops? exists without cadrei's markers")
		_, out, errOut = call("attach", "ops"+rlo)
		shows("attach's unmarked", out+errOut, "a session named cadrei-work-ops? exists without cadrei's markers")
	})
	session("cadrei-work-dev", map[string]string{"@cadrei_home": c, "@cadrei_team": "q" + esc})
	_, out, errOut = call("stop", "dev")
	shows("stop's instead", out+errOut, "q?[2J runs under that name")
	_, out, errOut = call("attach", "dev")
	shows("attach's instead", out+errOut, "q?[2J runs under its name")

	// The reporter's own label for a session it has no label for.
	var o, e strings.Builder
	(&env{stdout: &o, stderr: &e}).stopReporter(nil)("cadrei-work-x"+esc, false, false)
	shows("a stop that failed", o.String()+e.String(), "Couldn't stop cadrei-work-x?[2J.")
}

// Review of fix round 5 (S2, N3d and N3e): the stream names members as
// ls --json does, but reads them, and matches their windows, by the names
// tmux has: a window whose name turns text around gives no stop and start
// of its own, and a new member keeps the name it's read by.
func TestTheStreamReadsMembersByTheirOwnNames(t *testing.T) {
	c := teamOf(t)
	socket := os.Getenv("CADREI_TMUX_SOCKET")
	fastWatch(t)
	tmuxIn(socket, "rename-window", "-t", "=cadrei-work-dev:engineer", "eng\u202eineer")
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	if l := s.expect(t, lineSnapshot)[0]; strings.Contains(l.raw, "\u202e") || !strings.Contains(l.raw, `"eng?ineer"`) {
		t.Errorf("snapshot: %s", l.raw)
	}
	s.quiet(t, 500*time.Millisecond)
	tmuxIn(socket, "new-window", "-d", "-t", "=cadrei-work-dev:", "-n", "qa\u202eevil", "sleep", "300")
	if l := s.expect(t, lineMemberStarted)[0]; strings.Contains(l.raw, "\u202e") || l.m["member"].(map[string]any)["role"] != "qa?evil" {
		t.Errorf("member_started: %s", l.raw)
	}
	var w watcher
	i := session.Info{Name: "cadrei-work-dev", Team: "dev"}
	mw := w.newMember(i, session.Window{Session: i, Name: "qa\u202eevil", Member: "work-dev-qa\u202eevil"}, time.Now())
	if mw.name != "work-dev-qa\u202eevil" || mw.window != "qa\u202eevil" || mw.view.Name != "work-dev-qa?evil" || mw.view.Role != "qa?evil" {
		t.Errorf("new member %q %q %+v", mw.name, mw.window, mw.view)
	}
}

// tmuxEscapes reads tmux 3.4's way of giving back a control character in
// a value, an octal escape such as \033 (later tmux gives the character,
// which cadrei shows as ?), as the ? cadrei shows: either way nothing
// reaches the terminal.
func tmuxEscapes(out string) string {
	return regexp.MustCompile(`\\[0-3][0-7][0-7]`).ReplaceAllString(out, "?")
}
