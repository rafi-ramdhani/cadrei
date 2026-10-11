//go:build !windows

package main

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// contractCrew is a crew named work with a member in every state, a
// project in every state, model settings, a member started with --model,
// another crew, a 0.1.x session and one of a crew cadrei doesn't know.
func contractCrew(t *testing.T) (home, c, socket string) {
	t.Helper()
	home = sandbox(t)
	socket = withTmux(t, home)
	must(t, "init", "work")
	must(t, "init", "life")
	must(t, "use", "work")
	c = home + "/.cadrei/work"
	os.WriteFile(c+"/cadrei.conf", []byte("MODEL=sonnet\nEFFORT=medium\n"), 0o644)
	os.MkdirAll(home+"/Developer/app", 0o755)
	os.MkdirAll(home+"/Developer/lib", 0o755)
	exec.Command("git", "-C", home+"/Developer/lib", "init", "-q").Run()
	exec.Command("git", "-C", home+"/Developer/lib", "remote", "add", "origin", "https://example.com/me/lib.git").Run()
	must(t, "project", "dir", "~/Developer")
	register(t, home, "work", "app:\n  repo: https://example.com/me/app.git\n  team: dev\n  about: the app\n  path: ~/Developer/app\n"+
		"lib:\n  repo: https://example.com/me/lib.git\n"+
		"gone:\n  path: ~/Developer/gone\n"+
		"usb:\n  path: /Volumes/NoSuchDisk/usb\n")
	// Every member state, one member each.
	states := []string{"starting", "working", "approval", "question", "idle", "error", "unknown", "stopped", "quiet"}
	for _, s := range states {
		os.WriteFile(c+"/members/dev/"+s+".md", []byte("# Member: "+s+"\n"), 0o644)
	}
	os.Remove(c + "/members/dev/engineer.md")
	os.Remove(c + "/members/dev/reviewer.md")
	must(t, "up", "dev")
	member := func(s string) string { return "work-dev-" + s }
	feed(t, c, member("working"), `{"hook_event_name":"UserPromptSubmit"}`)
	feed(t, c, member("approval"), `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`)
	feed(t, c, member("question"), `{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion"}`)
	feed(t, c, member("idle"), `{"hook_event_name":"Stop"}`)
	feed(t, c, member("error"), `{"hook_event_name":"StopFailure","error":"rate_limit"}`)
	os.Remove(c + "/.claude/build/state/" + member("unknown") + ".json")
	feed(t, c, member("stopped"), `{"hook_event_name":"Stop"}`)
	placeTranscript(t, c, member("stopped"), "safeguard-stuck.jsonl", nil)
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(transcript, []byte("{}\n"), 0o600)
	old := time.Now().Add(-12 * time.Minute)
	os.Chtimes(transcript, old, old)
	feed(t, c, member("quiet"), `{"hook_event_name":"UserPromptSubmit","transcript_path":"`+transcript+`"}`)
	// A member started with --model, on the project.
	must(t, "up", "dev/working", "app", "--model", "opus")
	// The orchestrator in tmux, a 0.1.x session and one of a crew cadrei
	// doesn't know.
	tmuxIn(socket, "new-session", "-d", "-s", "cadrei-work", "-n", "orchestrator", "sleep", "300")
	tmuxIn(socket, "set-option", "-t", "=cadrei-work:", "@cadrei_role", "orchestrator")
	tmuxIn(socket, "set-option", "-t", "=cadrei-work:", "@cadrei_home", c)
	tmuxIn(socket, "new-session", "-d", "-s", "cadre-dev", "-n", "pm", "sleep", "300")
	tmuxIn(socket, "new-session", "-d", "-s", "cadrei-gone-dev", "-n", "engineer", "sleep", "300")
	tmuxIn(socket, "set-option", "-t", "=cadrei-gone-dev:", "@cadrei_home", "/elsewhere/gone")
	tmuxIn(socket, "set-option", "-t", "=cadrei-gone-dev:", "@cadrei_team", "dev")
	t.Chdir(c)
	return home, c, socket
}

// normalized is JSON with times, the home folder and the stub's pane
// details written the same way on every run.
func normalized(home, out string) string {
	out = regexp.MustCompile(`"\d{4}-\d\d-\d\dT[0-9:.]+(Z|[+-]\d\d:\d\d)"`).ReplaceAllString(out, `"<time>"`)
	return strings.ReplaceAll(out, home, "<home>")
}

// Acceptance 113 and 114: ls --json has exactly 7.13.6's fields, every
// state of members and projects, the model fields, the orchestrator in
// tmux and in a terminal, and with --all the 0.1.x and unknown sessions.
// Optional fields are left out, never null, and empty lists are [].
func TestTheLsJSONContract(t *testing.T) {
	home, c, _ := contractCrew(t)
	out := must(t, "ls", "--json")
	all := must(t, "ls", "--json", "--all")
	for name, raw := range map[string]string{"ls": out, "ls --all": all} {
		if strings.Contains(raw, "null") {
			t.Errorf("%s has a null:\n%s", name, raw)
		}
	}
	golden(t, "ls-contract.json", normalized(home, out))
	golden(t, "ls-contract-all.json", normalized(home, all))
	var st cadreiStatus
	json.Unmarshal([]byte(out), &st)
	byName := map[string]memberView{}
	for _, v := range st.Running {
		for _, m := range v.Members {
			byName[m.Name] = m
		}
	}
	if m := byName["work-dev-quiet"]; m.NoActivitySince == nil || m.State != "working" {
		t.Errorf("no activity: %+v", m)
	}
	if m := byName["work-dev-error"]; !m.TurnError || m.State != "idle" {
		t.Errorf("turn error: %+v", m)
	}
	for name, m := range byName {
		if name != "work-dev-quiet" && m.NoActivitySince != nil || name != "work-dev-error" && m.TurnError {
			t.Errorf("%s: %+v", name, m)
		}
	}
	// The orchestrator in a terminal.
	rt, _ := runtime.Get(runtimeName())
	if _, err := orchestrator.WriteLock(orchestrator.LockPath(rt.BuildDir(c)), os.Getpid(), orchestrator.Terminal, "", ""); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(must(t, "ls", "--json")), &st)
	if o := st.Orchestrator; !o.Running || o.Mode != "terminal" || o.Since == nil {
		t.Errorf("terminal: %+v", o)
	}
}

// Acceptance 115: no output holds chat text. A marker in a member's
// prompt, its transcript and a question it waits on never shows.
func TestNoChatTextInTheJSON(t *testing.T) {
	c := teamOf(t)
	marker := "MARKER-" + strings.Repeat("q", 6)
	os.WriteFile(c+"/members/dev/engineer.md", []byte("# Member: Engineer\n\n"+marker+"\n"), 0o644)
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(transcript, []byte(`{"type":"user","message":{"content":"`+marker+`"}}`+"\n"), 0o600)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"`+marker+`"}]},"transcript_path":"`+transcript+`"}`)
	t.Setenv("CADREI_HOME", c)
	fastWatch(t)
	s := startWatch(t)
	first := s.next(t, 5*time.Second)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit","prompt":"`+marker+`","transcript_path":"`+transcript+`"}`)
	t.Setenv("CADREI_HOME", c)
	next := s.expect(t, lineMemberState)[0]
	for name, out := range map[string]string{"ls --json": must(t, "ls", "--json"), "--all": must(t, "ls", "--json", "--all"),
		"snapshot": first.raw, "member_state": next.raw} {
		if strings.Contains(out, marker) {
			t.Errorf("%s holds chat text:\n%s", name, out)
		}
	}
}

// Acceptance 116: with the folder's project in two crews, ls --json asks
// nothing, even in a terminal: exit 1, the message on stderr, nothing on
// stdout. --watch does the same.
func TestLsJSONNeverAsks(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	must(t, "init", "life")
	os.MkdirAll(home+"/src/app", 0o755)
	register(t, home, "work", "app:\n  path: ~/src/app\n")
	register(t, home, "life", "app:\n  path: ~/src/app\n")
	t.Chdir(home + "/src/app")
	ttyForTests = true
	t.Cleanup(func() { ttyForTests = false })
	for _, args := range [][]string{{"ls", "--json"}, {"ls", "--json", "--watch"}} {
		code, out, errOut := callIn("work\n", args...)
		if code != 1 || out != "" || errOut == "" || strings.Contains(errOut, "Which one?") {
			t.Errorf("%v: %d %q %q", args, code, out, errOut)
		}
	}
	// Without --json it still asks, as before.
	if _, _, errOut := callIn("work\n", "ls"); !strings.Contains(errOut, "Which one?") {
		t.Errorf("ls didn't ask: %q", errOut)
	}
}

var _ = session.Bare

// Audit qual-maint-6: with no crew at all, ls --json --all still keeps to
// version 1: orchestrator_opens_in is terminal, the list of crews is
// empty, and it exits 0, as docs/tools.md says.
func TestLsJSONAllWithNoCrews(t *testing.T) {
	sandbox(t)
	code, out, errOut := call("ls", "--json", "--all")
	var st allStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil || code != 0 || errOut != "" {
		t.Fatalf("exit %d %q %q %v", code, out, errOut, err)
	}
	if st.OpensIn != "terminal" || len(st.Cadreis) != 0 || strings.Contains(out, "null") {
		t.Errorf("%s", out)
	}
}

// Audit qual-tests-05: every key in the ls --json goldens is a field of
// its own table on the tools page (the crew's in the crew's table, a
// running team's in the team table), so the page and the output can't
// drift apart (review of fix round 4, S5: any table used to do).
func TestTheGoldensUseOnlyDocumentedFields(t *testing.T) {
	page, err := os.ReadFile(filepath.Join(repoRoot, "docs", "tools.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Each table's fields, under the line that names what it describes.
	tables := map[string]map[string]bool{}
	caption := ""
	// A field, or a field with its value (`missing: true`).
	field := regexp.MustCompile("`([a-z_]+)(?:: [^`]*)?`")
	// Another crew's fields are named in its row's text, not in a table
	// (review of fix round 5, tests N4d).
	other := map[string]bool{}
	for _, line := range strings.Split(string(page), "\n") {
		switch {
		case strings.HasPrefix(line, "| `other_cadreis` |"):
			_, text, _ := strings.Cut(line[len("| `other_cadreis` |"):], " |")
			for _, m := range field.FindAllStringSubmatch(text, -1) {
				other[m[1]] = true
			}
		}
		switch {
		case strings.HasPrefix(line, "| `"):
			cell, _, _ := strings.Cut(line[2:], " |")
			for _, m := range field.FindAllStringSubmatch(cell, -1) {
				if tables[caption] == nil {
					tables[caption] = map[string]bool{}
				}
				tables[caption][m[1]] = true
			}
		case line != "" && !strings.HasPrefix(line, "|"):
			caption = line
		}
	}
	table := func(prefix string) map[string]bool {
		for c, fields := range tables {
			if strings.HasPrefix(c, prefix) {
				return fields
			}
		}
		t.Fatalf("no table under %q", prefix)
		return nil
	}
	top, crew, orch := table("Top level of `cadrei ls --json`"), table("`cadrei`, your crew"), table("`orchestrator`:")
	team, member, proj := table("Each team in `running`"), table("Each member in a running team"), table("Each project in `projects`")
	all := table("`cadrei ls --json --all` has")
	if strings.Join(slices.Sorted(maps.Keys(other)), ",") != "missing,name,path,running" {
		t.Errorf("other_cadreis row's fields: %v", other)
	}
	// What each key of a table holds, by table.
	children := map[*map[string]bool]map[string]*map[string]bool{
		&top:  {"cadrei": &crew, "orchestrator": &orch, "running": &team, "projects": &proj, "teams": nil, "other_cadreis": &other},
		&team: {"members": &member},
		&all:  {"cadreis": &top, "legacy": &team, "unknown": &team},
	}
	var walk func(path string, fields *map[string]bool, v any)
	walk = func(path string, fields *map[string]bool, v any) {
		switch v := v.(type) {
		case []any:
			for _, x := range v {
				walk(path, fields, x)
			}
		case map[string]any:
			if fields == nil { // teams: keyed by team, each a list of members
				for name, x := range v {
					walk(path+"."+name, &member, x)
				}
				return
			}
			for k, x := range v {
				in := (*fields)[k] || (fields == &all && top[k])
				if !in {
					t.Errorf("%s.%s isn't in its table on the tools page", path, k)
				}
				next, ok := children[fields][k]
				if ok {
					walk(path+"."+k, next, x)
					continue
				}
				// An object, or a list of them, with no table: its fields
				// would go unchecked (review of fix round 6).
				if _, nested := x.(map[string]any); nested {
					t.Errorf("%s.%s holds fields no table describes", path, k)
				}
				if list, isList := x.([]any); isList && len(list) > 0 {
					if _, nested := list[0].(map[string]any); nested {
						t.Errorf("%s.%s holds objects no table describes", path, k)
					}
				}
			}
		}
	}
	for name, root := range map[string]*map[string]bool{"ls-contract.json": &top, "ls-contract-all.json": &all} {
		raw, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		walk(name, root, v)
	}
}

// Forward-compatibility audit S4: a tool pins version 1 with
// --json-version 1; any other number, or the flag without --json, is
// refused with nothing on stdout.
func TestAToolCanPinTheJSONVersion(t *testing.T) {
	sandbox(t)
	must(t, "init", "work")
	if plain, pinned := must(t, "ls", "--json"), must(t, "ls", "--json", "--json-version", "1"); plain != pinned {
		t.Errorf("pinned to 1:\n%s\nplain:\n%s", pinned, plain)
	}
	for _, args := range [][]string{{"ls", "--json", "--json-version", "2"}, {"ls", "--json", "--json-version"}, {"ls", "--json-version", "1"}} {
		if code, out, _ := call(args...); code != 1 || out != "" {
			t.Errorf("%v: exit %d, stdout %q", args, code, out)
		}
	}
}
