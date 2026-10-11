//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// modelCrew is a crew named work with its dev team, on a test tmux server,
// whose engineer's file starts with front.
func modelCrew(t *testing.T, front string) (home, c string) {
	t.Helper()
	home = sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c = home + "/.cadrei/work"
	file := c + "/members/dev/engineer.md"
	if front != "" {
		os.WriteFile(file, []byte(front+readFile(t, file)), 0o644)
	}
	return home, c
}

// argsOf is what the stub was started with for a member, one per line.
func argsOf(t *testing.T, home, member string) string {
	t.Helper()
	return "\n" + readFile(t, home+"/args-"+member)
}

// Acceptance 90: front matter starts the member with --model and
// --effort, and stays out of its prompt; without it, neither flag.
func TestFrontMatterSetsModelAndEffort(t *testing.T) {
	home, c := modelCrew(t, "---\nmodel: \"sonnet\"  # the cheaper one\neffort: High\nlabel: kept\n---\n\n")
	must(t, "up", "dev")
	args := argsOf(t, home, "work-dev-engineer")
	if !strings.Contains(args, "\n--model\nsonnet\n") || !strings.Contains(args, "\n--effort\nhigh\n") {
		t.Errorf("engineer args:%s", args)
	}
	if args := argsOf(t, home, "work-dev-reviewer"); strings.Contains(args, "--model") || strings.Contains(args, "--effort") {
		t.Errorf("reviewer args:%s", args)
	}
	prompt := readFile(t, c+"/.claude/build/dev-engineer.md")
	if strings.Contains(prompt, "model:") || strings.Contains(prompt, "label: kept") || !strings.Contains(prompt, "\n# Member:") {
		t.Errorf("prompt:\n%s", prompt)
	}
	if !strings.Contains(readFile(t, c+"/members/dev/engineer.md"), "label: kept") {
		t.Error("the file lost its other keys")
	}
}

// Front matter needs --- on the first line and a closing one within 20
// lines.
func TestFrontMatterBounds(t *testing.T) {
	for _, c := range []struct {
		raw  string
		keys map[string]string
		rest string
	}{
		{"---\nmodel: opus\n---\n\n# Member\n", map[string]string{"model": "opus"}, "# Member\n"},
		{"# Member\n---\nmodel: opus\n---\n", nil, "# Member\n---\nmodel: opus\n---\n"},
		{"---\n" + strings.Repeat("x: y\n", 19) + "---\nrest\n", nil, ""},
		{"---\n" + strings.Repeat("x: y\n", 18) + "---\nrest\n", map[string]string{"x": "y"}, "rest\n"},
		{"---\nmodel: 'opus[1m]'\neffort: max # most\n---\nrest", map[string]string{"model": "opus[1m]", "effort": "max"}, "rest"},
	} {
		keys, rest := session.FrontMatter([]byte(c.raw))
		if c.keys == nil && keys != nil || c.keys != nil && (len(keys) != len(c.keys) || keys["model"] != c.keys["model"] || keys["effort"] != c.keys["effort"] || keys["x"] != c.keys["x"]) {
			t.Errorf("%q: keys %v", c.raw, keys)
		}
		if c.rest != "" && string(rest) != c.rest {
			t.Errorf("%q: rest %q", c.raw, rest)
		}
	}
}

// Acceptance 91 and 92: a flag beats the file, the file beats the local
// cadrei.conf, which beats cadrei.conf, each resolved on its own; a flag
// is for one start only.
func TestModelAndEffortOrder(t *testing.T) {
	home, c := modelCrew(t, "---\neffort: low\n---\n")
	os.WriteFile(c+"/cadrei.conf", []byte(readFile(t, c+"/cadrei.conf")+"\nMODEL=sonnet\nEFFORT=medium\n"), 0o644)
	os.MkdirAll(c+"/.claude/local", 0o700)
	os.WriteFile(c+"/.claude/local/cadrei.conf", []byte("MODEL=opus\n"), 0o600)
	must(t, "up", "dev")
	if args := argsOf(t, home, "work-dev-engineer"); !strings.Contains(args, "\n--model\nopus\n--effort\nlow\n") {
		t.Errorf("engineer:%s", args)
	}
	if args := argsOf(t, home, "work-dev-reviewer"); !strings.Contains(args, "\n--model\nopus\n--effort\nmedium\n") {
		t.Errorf("reviewer:%s", args)
	}
	must(t, "stop", "dev", "--yes")
	must(t, "up", "dev/engineer", "--model", "fable", "--effort=xhigh")
	if args := argsOf(t, home, "work-dev-engineer"); !strings.Contains(args, "\n--model\nfable\n--effort\nxhigh\n") {
		t.Errorf("flags:%s", args)
	}
	var st cadreiStatus
	json.Unmarshal([]byte(must(t, "ls", "--json")), &st)
	if m := st.Running[0].Members[0]; m.Model != "fable" || m.ModelFrom != "start" || m.Effort != "xhigh" || m.EffortFrom != "start" {
		t.Errorf("json member: %+v", m)
	}
	if st.Cadrei.Model != "opus" || st.Cadrei.Effort != "medium" {
		t.Errorf("json crew: %+v", st.Cadrei)
	}
	must(t, "stop", "dev", "--yes")
	must(t, "up", "dev/engineer", "--fresh")
	if args := argsOf(t, home, "work-dev-engineer"); !strings.Contains(args, "\n--model\nopus\n--effort\nlow\n") {
		t.Errorf("after the one start:%s", args)
	}
	// Nothing set anywhere: neither flag.
	os.Remove(c + "/.claude/local/cadrei.conf")
	os.WriteFile(c+"/cadrei.conf", []byte(strings.Replace(readFile(t, c+"/cadrei.conf"), "\nMODEL=sonnet\nEFFORT=medium\n", "", 1)), 0o644)
	os.WriteFile(c+"/members/dev/engineer.md", []byte(strings.TrimPrefix(readFile(t, c+"/members/dev/engineer.md"), "---\neffort: low\n---\n")), 0o644)
	must(t, "stop", "dev", "--yes")
	must(t, "up", "dev/engineer")
	if args := argsOf(t, home, "work-dev-engineer"); strings.Contains(args, "--model") || strings.Contains(args, "--effort") {
		t.Errorf("nothing set:%s", args)
	}
}

// Acceptance 93: a resume passes the flags too, and says what changed.
func TestAResumeSaysWhatItRunsOnNow(t *testing.T) {
	home, c := modelCrew(t, "---\nmodel: sonnet\n---\n")
	must(t, "up", "dev/engineer")
	rec := session.ReadRecord(c + "/.claude/build/sessions/work-dev-engineer.json")
	if rec == nil || rec.Model != "sonnet" || rec.ModelFrom != "member" {
		t.Fatalf("record: %+v", rec)
	}
	dir := filepath.Join(home, ".claude", "projects", regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(rec.Dir, "-"))
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, rec.ID+".jsonl"), []byte("{}\n"), 0o600)
	must(t, "stop", "dev/engineer")
	os.WriteFile(c+"/members/dev/engineer.md", []byte(strings.Replace(readFile(t, c+"/members/dev/engineer.md"), "model: sonnet\n", "model: opus\neffort: high\n", 1)), 0o644)
	out := must(t, "up", "dev/engineer")
	if !strings.Contains(out, "(picked up where it left off, now on opus, high effort)") {
		t.Errorf("up: %q", out)
	}
	if args := argsOf(t, home, "work-dev-engineer"); !strings.Contains(args, "\n--resume\n"+rec.ID+"\n") || !strings.Contains(args, "\n--model\nopus\n--effort\nhigh\n") {
		t.Errorf("args:%s", args)
	}
	// The same again says nothing more.
	must(t, "stop", "dev/engineer")
	if out := must(t, "up", "dev/engineer"); !strings.Contains(out, "(picked up where it left off)") {
		t.Errorf("again: %q", out)
	}
	// An override can't reach it while it runs.
	if out := must(t, "up", "dev/engineer", "--model", "sonnet"); !strings.Contains(flat(out), "The engineer is already running on opus, high effort. Stop it first to start it on sonnet: cadrei stop dev/engineer.") {
		t.Errorf("running: %q", out)
	}
}

// Acceptance 94: a bad effort or a malformed model stops up before
// anything starts, naming where it's set, and shows in --check.
func TestBadModelsAndEffortsStopUp(t *testing.T) {
	_, c := modelCrew(t, "---\neffort: hgih\n---\n")
	refusal(t, []string{"up", "dev"}, "The engineer's effort \"hgih\" isn't one Claude Code knows.",
		"Use low, medium, high, xhigh or max, in members/dev/engineer.md.")
	if out := must(t, "ls"); !strings.Contains(out, "Nothing running.") {
		t.Errorf("something started:\n%s", out)
	}
	os.WriteFile(c+"/members/dev/engineer.md", []byte(strings.Replace(readFile(t, c+"/members/dev/engineer.md"), "effort: hgih", "model: son net", 1)), 0o644)
	refusal(t, []string{"up", "dev/engineer"}, "The engineer's model \"son net\" isn't a model name.",
		"Use an alias such as opus, sonnet or fable, or a full name such as claude-sonnet-5-5, in members/dev/engineer.md.")
	refusal(t, []string{"up", "dev/reviewer", "--effort", "lots"}, "The effort \"lots\" isn't one Claude Code knows.",
		"Use low, medium, high, xhigh or max, with --effort.")
	refusal(t, []string{"up", "dev/reviewer", "--model", "-x"}, "The model \"-x\" isn't a model name.", "with --model.")
	os.WriteFile(c+"/cadrei.conf", []byte(readFile(t, c+"/cadrei.conf")+"\nEFFORT=lots\n"), 0o644)
	_, _, errOut := call("--check")
	for _, want := range []string{"The engineer's model \"son net\" isn't a model name.", "Your crew's effort \"lots\" isn't one Claude Code knows.", "for EFFORT in cadrei.conf."} {
		if !strings.Contains(flat(errOut), want) {
			t.Errorf("--check lacks %q:\n%s", want, errOut)
		}
	}
	for _, m := range []string{"us.anthropic.claude-x:0", "opus[1m]", "a/b@c_d.e"} {
		if !session.GoodModel(m) {
			t.Errorf("%s refused", m)
		}
	}
	for _, m := range []string{"", "a b", "a;b", "a\"b", `a\b`, "a$b", "a\tb", strings.Repeat("m", 201)} {
		if session.GoodModel(m) {
			t.Errorf("%q passed", m)
		}
	}
}

// refusal runs args, which must fail with what and fix on stderr.
func refusal(t *testing.T, args []string, what, fix string) {
	t.Helper()
	code, _, errOut := call(args...)
	// Plain output starts with "cadrei: " and a small letter.
	got := strings.ToLower(flat(errOut))
	if code == 0 || !strings.Contains(got, strings.ToLower(what)) || !strings.Contains(got, strings.ToLower(fix)) {
		t.Errorf("%v: %d %q", args, code, errOut)
	}
}

// Acceptance 94: a well-formed model the runtime doesn't know is passed
// on, and its own last words are shown when it ends at once.
func TestAnUnknownModelSaysWhy(t *testing.T) {
	home, _ := modelCrew(t, "---\nmodel: sonet\n---\n")
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+"echo 'There is an issue with the selected model (sonet).'\nexit 1\n")
	refusal(t, []string{"up", "dev/engineer"}, "The engineer didn't start: There is an issue with the selected model (sonet).",
		"Check model: in members/dev/engineer.md.")
}

// Acceptance 95: ls notes what cadrei passed, against the team default.
func TestLsShowsModels(t *testing.T) {
	_, c := modelCrew(t, "---\nmodel: opus\neffort: high\n---\n")
	os.WriteFile(c+"/cadrei.conf", []byte(readFile(t, c+"/cadrei.conf")+"\nMODEL=sonnet\nEFFORT=medium\n"), 0o644)
	must(t, "up", "dev")
	out := must(t, "ls")
	for _, want := range []string{"\nTeam default  sonnet, medium effort\n", " engineer   starting · opus, high effort\n", " reviewer   starting\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls lacks %q:\n%s", want, out)
		}
	}
	must(t, "stop", "dev/reviewer")
	must(t, "up", "dev/reviewer", "--model", "fable")
	if out := must(t, "ls"); !strings.Contains(out, " reviewer   starting · fable (this start only)\n") {
		t.Errorf("one start:\n%s", out)
	}
	columnsForTests = 50
	t.Cleanup(func() { columnsForTests = 0 })
	if out := must(t, "ls"); !strings.Contains(out, " engineer   starting\n                 opus, high effort\n") {
		t.Errorf("narrow:\n%s", out)
	}
	columnsForTests = 0
	t.Setenv("CADREI_HOME", c)
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	s := snapshotText(t, "resume")
	if !strings.Contains(s, "\n- Team default: sonnet, medium effort\n") || strings.Contains(s, "opus") {
		t.Errorf("snapshot:\n%s", s)
	}
}

// Acceptance 96: the orchestrator takes ORCHESTRATOR_MODEL and
// ORCHESTRATOR_EFFORT, never the members' MODEL and EFFORT.
func TestTheOrchestratorsOwnModel(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	stubClaude(t, home)
	os.WriteFile(home+"/release", nil, 0o644)
	must(t, "init", "work")
	conf := home + "/.cadrei/work/cadrei.conf"
	os.WriteFile(conf, []byte("MODEL=sonnet\nEFFORT=low\n"), 0o644)
	must(t)
	if ran := readFile(t, home+"/orch-ran"); strings.Contains(ran, "--model") || strings.Contains(ran, "--effort") {
		t.Errorf("the members' keys reached the orchestrator:\n%s", ran)
	}
	os.WriteFile(conf, []byte("MODEL=sonnet\nORCHESTRATOR_MODEL=opus\nORCHESTRATOR_EFFORT=max\n"), 0o644)
	must(t)
	if ran := readFile(t, home+"/orch-ran"); !strings.Contains(ran, "\n--model\nopus\n--effort\nmax\n") {
		t.Errorf("the orchestrator's keys:\n%s", ran)
	}
	os.WriteFile(conf, []byte("ORCHESTRATOR_EFFORT=lots\n"), 0o644)
	if code, _, errOut := call(); code != 1 || !strings.Contains(flat(errOut), "for ORCHESTRATOR_EFFORT in cadrei.conf.") {
		t.Errorf("a bad effort: %d %q", code, errOut)
	}
}

// Review round 3, item 6: the start check keeps looking for up to 3
// seconds, so a start that fails after the first half second is caught.
func TestAStartThatEndsLateIsCaught(t *testing.T) {
	home, _ := modelCrew(t, "")
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+"sleep 1.2\necho 'The model ran into a problem.'\nexit 1\n")
	refusal(t, []string{"up", "dev/engineer"}, "The engineer didn't start: the model ran into a problem.", "To see more, run this in")
	// Small after the colon, as written.
	_, _, errOut := call("up", "dev/engineer")
	if !strings.Contains(flat(errOut), "didn't start: the model ran into a problem.") {
		t.Errorf("the line after the colon: %q", errOut)
	}
}
