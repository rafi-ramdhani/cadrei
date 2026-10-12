package runtime

import (
	"strings"
	"testing"
)

type testRuntime struct{ caps Capabilities }

func (t testRuntime) Name() string                       { return "test" }
func (t testRuntime) Title() string                      { return "Test" }
func (t testRuntime) Probe() Probe                       { return Probe{} }
func (t testRuntime) Help() InstallHelp                  { return InstallHelp{} }
func (t testRuntime) Login() (Command, error)            { return Command{}, nil }
func (t testRuntime) Model() string                      { return "Test" }
func (t testRuntime) ModelHint() string                  { return "a test model" }
func (t testRuntime) SessionVars() []string              { return nil }
func (t testRuntime) SetupVars() []string                { return nil }
func (t testRuntime) Caps() Capabilities                 { return t.caps }
func (t testRuntime) Detect() (Install, error)           { return Install{}, nil }
func (t testRuntime) Launch(LaunchSpec) (Command, error) { return Command{}, nil }
func (t testRuntime) BuildDir(string) string             { return "" }
func (t testRuntime) Permissions() PermissionOps         { return nil }
func (t testRuntime) Trust() TrustOps                    { return nil }
func (t testRuntime) Health(bool, []string) []Problem    { return nil }
func (t testRuntime) Instructions() InstructionOps       { return nil }
func (t testRuntime) Hooks() HookOps                     { return nil }
func (t testRuntime) Sessions() SessionOps               { return nil }

func TestTheDefaultRuntime(t *testing.T) {
	RegisterDefault(testRuntime{})
	defer func() { delete(registry, "test"); defaultName = "" }()
	if Default() != "test" || len(Supported()) != 1 {
		t.Errorf("default %q, supported %v", Default(), Supported())
	}
}

func TestGetRefusesAnUnknownRuntime(t *testing.T) {
	Register(testRuntime{})
	defer delete(registry, "test")
	if _, err := Get("test"); err != nil {
		t.Fatal(err)
	}
	_, err := Get("codex")
	if err == nil || !strings.HasPrefix(err.Error(), "runtime codex is not supported yet (supported: ") {
		t.Errorf("Get(codex): %v", err)
	}
}

func TestUsable(t *testing.T) {
	ok := Capabilities{FixedDenies: true, Messaging: Native, PermissionModes: []string{"default"}}
	if err := Usable(testRuntime{ok}, "default"); err != nil {
		t.Errorf("a usable runtime: %v", err)
	}
	noDeny := ok
	noDeny.FixedDenies = false
	if err := Usable(testRuntime{noDeny}, "default"); err == nil || !strings.Contains(err.Error(), "fixed denies") {
		t.Errorf("no fixed denies: %v", err)
	}
	noMsg := ok
	noMsg.Messaging = NoMessaging
	if err := Usable(testRuntime{noMsg}, "default"); err == nil || !strings.Contains(err.Error(), "message the orchestrator") {
		t.Errorf("no messaging: %v", err)
	}
	if err := Usable(testRuntime{ok}, "yolo"); err == nil || !strings.Contains(err.Error(), "no permission mode yolo") {
		t.Errorf("an unknown mode: %v", err)
	}
}

// The checks' sentences about the runtime, said in one place (review of
// fix round 4, qual-maint-3).
func TestProbeSays(t *testing.T) {
	for p, want := range map[Probe]string{
		{}:                "X isn't installed.",
		{Installed: true}: "X is installed but won't start.",
		{Installed: true, Runs: true, Version: "1.2.3", Login: LoggedIn}:    "X 1.2.3, logged in",
		{Installed: true, Runs: true, Version: "1.2.3", Login: LoggedOut}:   "X 1.2.3, not logged in yet",
		{Installed: true, Runs: true, Version: "1.2.3", Login: LoginUnsure}: "X 1.2.3. Couldn't check your login. If it asks, log in.",
	} {
		if got := p.Says("X"); got != want {
			t.Errorf("%+v: %q", p, got)
		}
	}
}
