//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// sent is Claude Code's PostToolUse input for a SendMessage call.
func sent(to string, notify bool, agent string) string {
	in := map[string]any{"hook_event_name": "PostToolUse", "tool_name": "SendMessage",
		"tool_input": map[string]any{"to": to, "message": "Review my-app."}, "tool_response": map[string]any{"success": true}}
	if notify {
		in["tool_input"].(map[string]any)["notify_when_idle"] = true
	}
	if agent != "" {
		in["agent_id"] = agent
	}
	b, _ := json.Marshal(in)
	return string(b)
}

// cadrei hook notify asks the orchestrator for a pure subscription after
// a message to a member of its crew that didn't ask to hear when the
// member goes idle, and prints nothing for every other call.
func TestTheNotifyHookAsksOnlyWhenAMemberWontBeHeardFrom(t *testing.T) {
	home := sandbox(t)
	must(t, "init", "work")
	must(t, "init", "life")
	c := home + "/.cadrei/work"
	orchestrator := func() {
		t.Setenv("CADREI_ORCHESTRATOR", "1")
		t.Setenv("CADREI_HOME", c)
	}
	orchestrator()
	hook := func(input string) string {
		t.Helper()
		code, out, _ := callIn(input, "hook", "notify")
		if code != 0 {
			t.Errorf("exit %d for %s", code, input)
		}
		return out
	}

	// The note: one JSON object, the fixed text, and nothing else.
	for _, to := range []string{"work-dev-engineer", "work-dev-reviewer [3fa9c1]"} {
		out := hook(sent(to, false, ""))
		var got map[string]map[string]string
		if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 1 || len(got["hookSpecificOutput"]) != 2 ||
			got["hookSpecificOutput"]["hookEventName"] != "PostToolUse" || got["hookSpecificOutput"]["additionalContext"] != notifyNote {
			t.Errorf("%s: %q", to, out)
		}
		if strings.Count(out, "\n") != 1 || strings.Contains(out, to) {
			t.Errorf("%s: not one fixed line: %q", to, out)
		}
	}
	// Everything else: nothing at all.
	for name, input := range map[string]string{
		"asked to hear":         sent("work-dev-engineer", true, ""),
		"from a subagent":       sent("work-dev-engineer", false, "a1b2c3"),
		"another crew's member": sent("life-dev-engineer", false, ""),
		"not a member":          sent("work-dev-nobody", false, ""),
		"an address":            sent("uds:/tmp/x.sock", false, ""),
		"another tool":          `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`,
		"no target":             `{"hook_event_name":"PostToolUse","tool_name":"SendMessage","tool_input":{"message":"x"}}`,
		"not JSON":              "{nope",
		"nothing":               "",
	} {
		if out := hook(input); out != "" {
			t.Errorf("%s: %q", name, out)
		}
	}
	// Not the orchestrator: a member, or a plain chat.
	t.Setenv("CADREI_MEMBER", "work-dev-engineer")
	if out := hook(sent("work-dev-reviewer", false, "")); out != "" {
		t.Errorf("in a member: %q", out)
	}
	os.Unsetenv("CADREI_MEMBER")
	os.Unsetenv("CADREI_ORCHESTRATOR")
	if out := hook(sent("work-dev-reviewer", false, "")); out != "" {
		t.Errorf("outside the orchestrator: %q", out)
	}
	// A crew it can't find, and a lookup that takes too long.
	orchestrator()
	t.Setenv("CADREI_HOME", home+"/nowhere")
	if out := hook(sent("work-dev-reviewer", false, "")); out != "" {
		t.Errorf("no crew: %q", out)
	}
	orchestrator()
	notifyStallForTests, notifyLimit = 3*time.Second, 100*time.Millisecond
	t.Cleanup(func() { notifyStallForTests, notifyLimit = 0, 2*time.Second })
	start := time.Now()
	if out := hook(sent("work-dev-reviewer", false, "")); out != "" || time.Since(start) > 2*time.Second {
		t.Errorf("a slow lookup: %q after %s", out, time.Since(start))
	}
}

// A cadrei that doesn't know a hook word prints nothing on stdout: the
// error goes to stderr, which the hook command drops.
func TestAnUnknownHookWordPrintsNothing(t *testing.T) {
	sandbox(t)
	if code, out, errOut := call("hook", "notify-later"); out != "" || code == 0 || errOut == "" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}
