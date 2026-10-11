//go:build !windows

package main

import (
	"strings"
	"testing"
)

// The guard refuses cadrei's uninstall and restore in every form a member
// could type, and lets everything else run.
func TestTheGuardSeesEveryWayToUninstall(t *testing.T) {
	for _, c := range []string{
		`cadrei uninstall --all --confirm "delete everything"`,
		`env -u CADREI_MEMBER cadrei uninstall --all`,
		`CADREI_MEMBER= cadrei uninstall --yes`,
		`sh -c "cadrei uninstall --all"`,
		`bash -c 'cadrei restore'`,
		`/opt/homebrew/bin/cadrei uninstall --archive --yes`,
		`./cadrei uninstall`, `../bin/cadrei restore ~/x`,
		`cadre uninstall`,
		`cad"rei" unin'st'all`, `cadrei un\install`, `CADREI UNINSTALL`,
		`ls; cadrei uninstall --yes`, `cadrei ls && cadrei restore`,
		`c=cadrei; $c uninstall`, `x=$(which cadrei); "$x" uninstall --all`,
		"`which cadrei` restore",
		`printf 'uninstall --all' | xargs cadrei`,
		`echo uninstall | bash`,
		`cadrei unin${x}stall --all`,
		"cadrei\tuninstall", "cadrei\nuninstall",
	} {
		if !removesCrews(c) {
			t.Errorf("let through: %q", c)
		}
	}
	for _, c := range []string{
		`cadrei ls --json`, `cadrei up dev my-app`, `cadrei stop dev`, `git status`, `git restore notes.md`,
		`npm uninstall left-pad`, `echo restore`, `ls -la ~/.cadrei`, `go test ./...`,
	} {
		if removesCrews(c) {
			t.Errorf("refused: %q", c)
		}
	}
}

// cadrei hook guard reads Claude Code's tool call: a Bash call it refuses
// ends with exit 2 and why, any other call goes on, and input it can't
// read is refused.
func TestTheGuardHook(t *testing.T) {
	sandbox(t)
	bash := func(command string) string {
		return `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":` + strconvQuote(command) + `}}`
	}
	for input, want := range map[string]int{
		bash("cadrei uninstall --all"):         2,
		bash(`sh -c "cadrei restore"`):         2,
		bash("git status"):                     0,
		`{"tool_name":"Edit","tool_input":{}}`: 0,
		`{"tool_name":"Bash","tool_input":{}}`: 2,
		`{"tool_input":{"command":"ls"}}`:      2,
		"not json":                             2,
		"":                                     2,
	} {
		code, out, errOut := callIn(input, "hook", "guard")
		if code != want || out != "" || (want == 2 && errOut == "") {
			t.Errorf("%q: exit %d, %q %q", input, code, out, errOut)
		}
	}
	if _, _, errOut := callIn(bash("cadrei uninstall --all"), "hook", "guard"); !strings.Contains(errOut, "Team members can't run cadrei uninstall or cadrei restore, in any form.") {
		t.Errorf("why: %q", errOut)
	}
}

// strconvQuote quotes s as a JSON string.
func strconvQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(s) + `"`
}
