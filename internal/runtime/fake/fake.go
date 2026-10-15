//go:build cadreitest

// Package fake is a runtime for tests only (section P.8): it is compiled
// only with -tags cadreitest, never into a release binary. Its capabilities
// can be switched off per test, or by CADREI_FAKE_OFF (a comma-separated
// list of capability names) for the smoke test, and it launches
// CADREI_FAKE_BIN, a stub that records its arguments and environment.
package fake

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

func init() { runtime.Register(Fake{}) }

// Off lists capabilities switched off, for unit tests.
var Off = map[string]bool{}

// Fake is the test runtime.
type Fake struct{}

func (Fake) Name() string  { return "fake" }
func (Fake) Title() string { return "the fake runtime" }
func (Fake) Model() string { return "the fake model" }

func (Fake) ModelHint() string { return "a fake model name" }

func (Fake) SessionVars() []string { return nil }

func (Fake) SetupVars() []string { return nil }

func off(name string) bool {
	if Off[name] {
		return true
	}
	for _, n := range strings.Split(os.Getenv("CADREI_FAKE_OFF"), ",") {
		if strings.TrimSpace(n) == name {
			return true
		}
	}
	return false
}

func (Fake) Caps() runtime.Capabilities {
	c := runtime.Capabilities{
		FixedDenies:     !off("FixedDenies"),
		PermissionModes: []string{"default", "fake-mode"},
		Resume:          !off("Resume"), AssignSessionID: !off("AssignSessionID"), Trust: !off("Trust"),
		Instructions: !off("Instructions"), OrchestratorHook: !off("OrchestratorHook"), Messaging: runtime.Native,
	}
	if off("Messaging") {
		c.Messaging = runtime.NoMessaging
	}
	return c
}

func (Fake) Help() runtime.InstallHelp { return runtime.InstallHelp{URL: "https://example.com/fake"} }

// Login: the fake runtime is always logged in.
func (Fake) Login() (runtime.Command, error) {
	return runtime.Command{}, errors.New("the fake runtime has no login")
}

func (Fake) Probe(context.Context) runtime.Probe {
	return runtime.Probe{Installed: true, Runs: true, Version: "1.0.0", Login: runtime.LoggedIn}
}

func (Fake) Detect() (runtime.Install, error) {
	return runtime.Install{Path: os.Getenv("CADREI_FAKE_BIN"), Version: "0.0.0-fake"}, nil
}

// Launch returns a command whose every part comes from the spec, so a test
// can check that what runs in tmux is what Launch built.
func (f Fake) Launch(s runtime.LaunchSpec) (runtime.Command, error) {
	in, _ := f.Detect()
	return runtime.Command{
		Argv: []string{in.Path, "--fake-name", s.Name, "--fake-mode", s.Mode, "--fake-prompt", s.PromptFile, "--fake-grants", s.Grants,
			"--fake-session", s.SessionID, "--fake-resume", s.Resume, "--fake-settings", s.Settings,
			"--fake-allowed", strings.Join(s.Allowed, "|"), "--fake-first", s.Prompt, "--fake-model", s.Model, "--fake-effort", s.Effort},
		Env: []string{"CADREI_FAKE_LAUNCHED=" + s.Name},
		Dir: s.WorkDir,
	}, nil
}

func (Fake) BuildDir(cadrei string) string { return filepath.Join(cadrei, ".fake", "build") }

// Health finds nothing wrong: the fake is always installed.
func (Fake) Health(bool, []string) []runtime.Problem { return nil }

func (Fake) Instructions() runtime.InstructionOps { return instructions{} }
func (Fake) Hooks() runtime.HookOps               { return hooks{} }
func (Fake) Sessions() runtime.SessionOps         { return sessions{} }

// sessions hand out counted ids and know every one they gave.
type sessions struct{}

func (sessions) NewID() string              { return "fake-session" }
func (sessions) Exists(id, dir string) bool { return os.Getenv("CADREI_FAKE_GONE") != id }

// Transcript reads the fake's transcript, <dir>/.fake-transcripts/<id>,
// which a test's stub agent writes to say it got going.
func (sessions) Transcript(id, dir string) (time.Time, int64, bool) {
	if st, err := os.Stat(filepath.Join(dir, ".fake-transcripts", id)); err == nil {
		return st.ModTime(), st.Size(), true
	}
	return time.Time{}, 0, false
}
func (sessions) FromHook(input io.Reader) string {
	line, _ := bufio.NewReader(input).ReadString('\n')
	return strings.TrimSpace(line)
}

// instructions link the skill under $HOME/.fake, as the real adapter does
// under Claude Code's folder.
type instructions struct{}

func (instructions) Path() string {
	return filepath.Join(os.Getenv("HOME"), ".fake", "skills", "cadrei")
}

func (i instructions) Target() (string, error) {
	st, err := os.Lstat(i.Path())
	if err != nil {
		return "", err
	}
	if st.Mode()&os.ModeSymlink == 0 {
		return "", runtime.ErrNotLink
	}
	return os.Readlink(i.Path())
}

// OldClone finds none: the fake never had a 0.1.x.
func (instructions) OldClone(string) bool { return false }

func (i instructions) Unlink(dir string) (bool, error) {
	if t, err := i.Target(); err != nil || t != dir {
		return false, nil
	}
	return true, os.Remove(i.Path())
}

func (i instructions) Link(dir string) error {
	os.MkdirAll(filepath.Dir(i.Path()), 0o755)
	os.Remove(i.Path())
	return os.Symlink(dir, i.Path())
}

// hooks keep no settings: the fake has no orchestrator hook.
type hooks struct{}

func (hooks) File() string                  { return filepath.Join(os.Getenv("HOME"), ".fake", "settings.json") }
func (hooks) Find() ([]string, error)       { return nil, nil }
func (hooks) Set(string) (bool, error)      { return false, errors.New("the fake runtime has no hooks") }
func (hooks) Remove(string) (bool, error)   { return false, nil }
func (hooks) Outdated(string) (bool, error) { return false, nil }
func (hooks) Output(text string) []byte     { return []byte(text + "\n") }
func (hooks) SnapshotSettings(dir, binary string) (string, error) {
	return filepath.Join(dir, "fake-snapshot-settings"), nil
}
func (Fake) Permissions() runtime.PermissionOps { return permissions{} }
func (Fake) Trust() runtime.TrustOps            { return trust{} }

type permissions struct{}

func (permissions) GrantsFile(cadrei string) string { return filepath.Join(cadrei, ".fake", "grants") }
func (permissions) Validate(cadrei string, known []string, rule string, auto bool) (string, error) {
	return "", nil
}
func (permissions) Prepare(cadrei string, places runtime.Places) runtime.Prepared {
	return runtime.Prepared{Grants: "fake-grants"}
}
func (permissions) Open(string, bool) (runtime.GrantStore, error) {
	return nil, errors.New("the fake runtime keeps no grants")
}
func (permissions) Unchanged(string) bool { return true }
func (permissions) Record(string)         {}
func (permissions) BuiltIn() string       { return "" }

type trust struct{}

func (trust) Mark(f []runtime.Folder) ([]runtime.TrustResult, string)   { return nil, "" }
func (trust) Unmark(f []runtime.Folder) ([]runtime.TrustResult, string) { return nil, "" }
func (trust) Protected() []string                                       { return nil }
func (trust) Risky(string) []string                                     { return nil }
func (trust) Loaded(string) []string                                    { return nil }

// StateEvent reads {"event": ..., "session_id": ...}: the fake's hooks.
func (sessions) StateEvent(input io.Reader) runtime.StateEvent {
	var in struct {
		Event   string `json:"event"`
		Session string `json:"session_id"`
	}
	json.NewDecoder(input).Decode(&in)
	return runtime.StateEvent{Name: in.Event, Session: in.Session}
}

func (sessions) TranscriptFile(id, dir string) string { return "" }

func (sessions) Context(string, string) (runtime.ContextUse, bool) {
	return runtime.ContextUse{}, false
}

func (sessions) UnansweredPrompt(string, string) bool { return false }

func (sessions) SafeguardStop(string) (time.Time, bool) { return time.Time{}, false }

// Ready: the fake has no screen, so it's ready once it runs.
func (sessions) Ready(string) bool { return true }

func (sessions) Waiting(string) bool { return false }

func (sessions) Sent(input io.Reader) (runtime.Sent, bool) {
	var in struct {
		To       *string
		Notify   bool
		Subagent bool
	}
	if json.NewDecoder(input).Decode(&in) != nil || in.To == nil {
		return runtime.Sent{}, false
	}
	return runtime.Sent{To: *in.To, Notify: in.Notify, Subagent: in.Subagent}, true
}

func (sessions) SentContext(text string) []byte { return []byte(text + "\n") }

func (sessions) ShellCall(input io.Reader) (runtime.ShellCall, error) {
	var in struct{ Command, Mode string }
	if err := json.NewDecoder(input).Decode(&in); err != nil {
		return runtime.ShellCall{}, err
	}
	return runtime.ShellCall{Command: in.Command, Shell: true, Mode: in.Mode}, nil
}

func (sessions) SessionSettings(string) (string, bool) { return "", false }

func (sessions) InSession() bool { return false }

func (sessions) Ask(why string) []byte {
	b, _ := json.Marshal(map[string]string{"ask": why})
	return append(b, '\n')
}
