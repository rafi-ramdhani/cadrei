package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

func TestLaunch(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin)
	cmd, err := Claude{}.Launch(runtime.LaunchSpec{Name: "w-dev-pm", Mode: "auto", PromptFile: "/p.md", Grants: "/g.json", WorkDir: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(bin, "claude") + " --name w-dev-pm --permission-mode auto --append-system-prompt-file /p.md --settings /g.json"
	if strings.Join(cmd.Argv, " ") != want || cmd.Dir != "/w" || len(cmd.Env) != 0 {
		t.Errorf("Launch: %+v", cmd)
	}
	cmd, _ = Claude{}.Launch(runtime.LaunchSpec{Name: "x", Mode: "default"})
	if strings.Contains(strings.Join(cmd.Argv, " "), "--settings") || strings.Contains(strings.Join(cmd.Argv, " "), "--append-system-prompt-file") {
		t.Errorf("no grants and no prompt: %+v", cmd)
	}
	// A member never takes the starting session's IDE connection; the
	// orchestrator keeps the one of the terminal it opens in (review of
	// fix round 5, bugs 7).
	if !slices.Contains(cmd.Unset, "CLAUDE_CODE_SSE_PORT") || !slices.Contains(cmd.Unset, "CLAUDECODE") {
		t.Errorf("a member's unset: %v", cmd.Unset)
	}
	cmd, _ = Claude{}.Launch(runtime.LaunchSpec{Role: runtime.Orchestrator, Name: "x", Mode: "default"})
	if slices.Contains(cmd.Unset, "CLAUDE_CODE_SSE_PORT") || !slices.Contains(cmd.Unset, "CLAUDECODE") || !slices.Contains(sessionVars, "CLAUDE_CODE_SSE_PORT") {
		t.Errorf("the orchestrator's unset: %v", cmd.Unset)
	}
	// After cadrei logged the user in, the session reads the config dir
	// the login did: this process's, set or not, never a tmux server's.
	t.Setenv("CLAUDE_CONFIG_DIR", "/cfg")
	cmd, _ = Claude{}.Launch(runtime.LaunchSpec{Role: runtime.Orchestrator, Name: "x", Mode: "default", ConfigHere: true})
	if !slices.Contains(cmd.Env, "CLAUDE_CONFIG_DIR=/cfg") || slices.Contains(cmd.Unset, "CLAUDE_CONFIG_DIR") {
		t.Errorf("config here, set: %+v", cmd)
	}
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	cmd, _ = Claude{}.Launch(runtime.LaunchSpec{Role: runtime.Orchestrator, Name: "x", Mode: "default", ConfigHere: true})
	if len(cmd.Env) != 0 || !slices.Contains(cmd.Unset, "CLAUDE_CONFIG_DIR") || slices.Contains(sessionVars, "CLAUDE_CONFIG_DIR") {
		t.Errorf("config here, unset: %+v", cmd)
	}
	cmd, _ = Claude{}.Launch(runtime.LaunchSpec{Role: runtime.Orchestrator, Name: "x", Mode: "default"})
	if len(cmd.Env) != 0 || slices.Contains(cmd.Unset, "CLAUDE_CONFIG_DIR") {
		t.Errorf("as before: %+v", cmd)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := (Claude{}).Launch(runtime.LaunchSpec{}); err == nil || !strings.Contains(err.Error(), "not on your PATH") {
		t.Errorf("no claude: %v", err)
	}
}

func TestSessions(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin)
	cmd, _ := Claude{}.Launch(runtime.LaunchSpec{Name: "x", Mode: "default", SessionID: "11111111-2222-4333-8444-555555555555"})
	if !strings.HasSuffix(strings.Join(cmd.Argv, " "), "--session-id 11111111-2222-4333-8444-555555555555") {
		t.Errorf("a new conversation: %v", cmd.Argv)
	}
	cmd, _ = Claude{}.Launch(runtime.LaunchSpec{Name: "x", Mode: "default", Resume: "11111111-2222-4333-8444-555555555555"})
	if !strings.HasSuffix(strings.Join(cmd.Argv, " "), "--resume 11111111-2222-4333-8444-555555555555") || strings.Contains(strings.Join(cmd.Argv, " "), "--session-id") {
		t.Errorf("a resumed conversation: %v", cmd.Argv)
	}
	ops := Claude{}.Sessions()
	id := ops.NewID()
	if !uuidRule.MatchString(id) || id[14] != '4' || ops.NewID() == id {
		t.Errorf("NewID %q", id)
	}
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	if ops.Exists(id, "/Users/me/app") {
		t.Error("a conversation with no transcript exists")
	}
	// Only the transcript in the session's own folder counts, as
	// claude --resume looks there: /Users/me/.cadrei/w is -Users-me--cadrei-w.
	os.MkdirAll(filepath.Join(cfg, "projects", "-Users-me-app"), 0o755)
	os.WriteFile(filepath.Join(cfg, "projects", "-Users-me-app", id+".jsonl"), []byte("{}\n"), 0o600)
	if !ops.Exists(id, "/Users/me/app") || ops.Exists(id, "/Users/me/.cadrei/w") || ops.Exists("../../etc/passwd", "/Users/me/app") {
		t.Error("Exists")
	}
	os.MkdirAll(filepath.Join(cfg, "projects", "-Users-me--cadrei-w"), 0o755)
	os.WriteFile(filepath.Join(cfg, "projects", "-Users-me--cadrei-w", id+".jsonl"), []byte("{}\n"), 0o600)
	if !ops.Exists(id, "/Users/me/.cadrei/w") {
		t.Error("a folder with a dot")
	}
	if _, size, ok := ops.Transcript(id, "/Users/me/.cadrei/w"); !ok || size != 3 {
		t.Errorf("Transcript: %v %d", ok, size)
	}
	if _, _, ok := ops.Transcript(id, "/Users/me/other"); ok {
		t.Error("Transcript in another folder")
	}
	if got := ops.FromHook(strings.NewReader(`{"session_id": "` + id + `", "source": "clear"}`)); got != id {
		t.Errorf("FromHook %q", got)
	}
	if got := ops.FromHook(strings.NewReader(`{"session_id": "../x"}`)); got != "" {
		t.Errorf("a bad id from the hook: %q", got)
	}
}

// The login check's limit itself: a command that hangs is stopped at
// authLimit, with what it started, plus the half second WaitDelay allows,
// and some slack for a loaded machine.
func TestOutputWithinStopsAtItsLimit(t *testing.T) {
	stub := filepath.Join(t.TempDir(), "hang")
	os.WriteFile(stub, []byte("#!/bin/sh\nsleep 60 &\nsleep 60\n"), 0o755)
	start := time.Now()
	if _, err := outputWithin(authLimit, stub); err == nil {
		t.Error("a hung command came back without an error")
	}
	if took := time.Since(start); took > authLimit+500*time.Millisecond+5*time.Second {
		t.Errorf("took %s, for a %s limit", took, authLimit)
	}
}

// Acceptance 67: a login check that hangs (offline) gives up within the
// 3-second limit, as "couldn't check".
func TestAHangingLoginCheckGivesUp(t *testing.T) {
	dir := t.TempDir()
	stub := "#!/bin/sh\ncase \"$1\" in\n  --version) echo '2.1.300 (Claude Code)' ;;\n  auth) sleep 60 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	start := time.Now()
	p := Claude{}.Probe()
	// Cut off well before the stub's 60 s, however loaded the machine (the
	// limit itself is pinned below).
	if took := time.Since(start); took > 30*time.Second {
		t.Errorf("took %s", took)
	}
	if !p.Installed || !p.Runs || p.Login != runtime.LoginUnsure {
		t.Errorf("probe %+v", p)
	}
}

// Audit qual-tests-09, acceptance 67: the login check's limit is 3
// seconds at most, pinned here rather than relative to itself.
func TestTheLoginLimitIsThreeSeconds(t *testing.T) {
	if authLimit > 3*time.Second {
		t.Errorf("the login check may take %s, more than 3 seconds", authLimit)
	}
}

// Skills, agents and commands can grant tools or add hooks, and YAML has
// too many ways to say so: any of them in a project makes it risky, named
// by its folder; an empty folder doesn't.
func TestRiskyFindsSkillsAgentsAndCommands(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(dir+"/.claude/skills/empty", 0o755)
	os.MkdirAll(dir+"/.claude/agents", 0o755)
	if got := (trust{}).Risky(dir); len(got) != 0 {
		t.Errorf("empty folders: %v", got)
	}
	os.WriteFile(dir+"/.claude/skills/empty/SKILL.md", []byte("---\nname: plain\n---\nJust words.\n"), 0o644)
	os.MkdirAll(dir+"/.claude/commands", 0o755)
	os.WriteFile(dir+"/.claude/commands/go.md", []byte("Just words.\n"), 0o644)
	if got := strings.Join((trust{}).Risky(dir), ","); got != ".claude/skills,.claude/commands" {
		t.Errorf("risky %q", got)
	}
}

// Review round 3, item 3, and round 4, C: the ways around the check. Any
// file or link in the skill, agent and command folders, in any case, or a
// linked .claude, is risky, however its front matter is spelled.
func TestRiskyCannotBeTalkedAround(t *testing.T) {
	grant := func(key string) []byte { return []byte("---\nname: x\n" + key + "\n---\n") }
	for name, setup := range map[string]func(dir string){
		"linked skills folder": func(dir string) {
			os.MkdirAll(dir+"/elsewhere/s", 0o755)
			os.WriteFile(dir+"/elsewhere/s/SKILL.md", []byte("---\nname: s\n---\n"), 0o644)
			os.MkdirAll(dir+"/.claude", 0o755)
			os.Symlink("../elsewhere", dir+"/.claude/skills")
		},
		"linked skill file": func(dir string) {
			os.WriteFile(dir+"/plain.md", []byte("words\n"), 0o644)
			os.MkdirAll(dir+"/.claude/skills/x", 0o755)
			os.Symlink("../../../plain.md", dir+"/.claude/skills/x/SKILL.md")
		},
		"linked .claude": func(dir string) {
			os.MkdirAll(dir+"/elsewhere", 0o755)
			os.Symlink("elsewhere", dir+"/.claude")
		},
		"escaped key":    func(dir string) { skill(dir, "SKILL.md", grant(`"allowed\x2dtools": Bash(make:*)`)) },
		"explicit key":   func(dir string) { skill(dir, "SKILL.md", grant("? allowed-tools\n: Bash(make:*)")) },
		"alias key":      func(dir string) { skill(dir, "SKILL.md", grant("k: &k allowed-tools\n*k : Bash(make:*)")) },
		"tagged key":     func(dir string) { skill(dir, "SKILL.md", grant("!!str allowed-tools: Bash(make:*)")) },
		"capital suffix": func(dir string) { skill(dir, "SKILL.MD", grant("allowed-tools: Bash(make:*)")) },
		"capital folders": func(dir string) {
			os.MkdirAll(dir+"/.Claude/Skills/x", 0o755)
			os.WriteFile(dir+"/.Claude/Skills/x/notes.txt", []byte("x\n"), 0o644)
		},
		"capital settings": func(dir string) {
			os.MkdirAll(dir+"/.claude", 0o755)
			os.WriteFile(dir+"/.claude/Settings.JSON", []byte("{}\n"), 0o644)
		},
		"capital mcp": func(dir string) { os.WriteFile(dir+"/.MCP.json", []byte("{}\n"), 0o644) },
	} {
		dir := t.TempDir()
		setup(dir)
		if got := (trust{}).Risky(dir); len(got) == 0 {
			t.Errorf("%s: not risky", name)
		}
	}
}

// Audit sec-trust-1: Claude Code loads a nested folder's skills, agents
// and commands when a member works under it, unless git ignores the
// folder. So they count at any depth, named by their path, but not inside
// .git or what git ignores and doesn't track.
func TestRiskyLooksAtEveryDepth(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	put := func(rel string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755)
		os.WriteFile(filepath.Join(dir, rel), []byte("---\nname: x\nallowed-tools: Bash(make:*)\n---\n"), 0o644)
	}
	git("init", "-q")
	os.WriteFile(dir+"/.gitignore", []byte("build/\n/vendor/\n"), 0o644)
	put("build/out/.claude/skills/x/SKILL.md")
	put("vendor/lib/.claude/agents/a.md")
	put(".git/x/.claude/skills/x/SKILL.md")
	if got := (trust{}).Risky(dir); len(got) != 0 {
		t.Errorf("ignored and .git: %v", got)
	}
	put("packages/web/.claude/skills/x/SKILL.md")
	put("node_modules/pkg/.Claude/Commands/go.md")
	os.MkdirAll(dir+"/apps/api", 0o755)
	os.Symlink("../../elsewhere", dir+"/apps/api/.claude")
	// Tracked although git would ignore it: Claude Code loads it.
	put("build/kept/.claude/skills/x/SKILL.md")
	git("add", "-f", "build/kept/.claude/skills/x/SKILL.md")
	got := strings.Join((trust{}).Risky(dir), ",")
	if got != "apps/api/.claude,build/kept/.claude/skills,node_modules/pkg/.Claude/Commands,packages/web/.claude/skills" {
		t.Errorf("risky %q", got)
	}
	// From inside the repo, paths are relative to the folder asked about.
	if got := strings.Join((trust{}).Risky(dir+"/packages"), ","); got != "web/.claude/skills" {
		t.Errorf("from a subfolder: %q", got)
	}
	// Outside git, nothing is ignored.
	os.RemoveAll(dir + "/.git")
	if got := (trust{}).Risky(dir); !slices.Contains(got, "build/out/.claude/skills") {
		t.Errorf("outside git: %v", got)
	}
	// Past the limit, what wasn't looked at counts.
	defer func(n int) { walkLimit = n }(walkLimit)
	walkLimit = 2
	if got := (trust{}).Risky(dir); !slices.Contains(got, tooManyFolders) {
		t.Errorf("past the limit: %v", got)
	}
}

// Audit sec-trust-1: git runs core.fsmonitor whenever it reads the index.
// The look at what git ignores runs nothing from the repo's own config.
func TestRiskyRunsNothingFromTheRepo(t *testing.T) {
	dir := t.TempDir()
	exec.Command("git", "-C", dir, "init", "-q").Run()
	ran := filepath.Join(t.TempDir(), "ran")
	exec.Command("git", "-C", dir, "config", "core.fsmonitor", "touch "+ran+"; false").Run()
	os.WriteFile(dir+"/f", []byte("x\n"), 0o644)
	exec.Command("git", "-C", dir, "-c", "core.fsmonitor=false", "add", "f").Run()
	(trust{}).Risky(dir)
	if _, err := os.Stat(ran); err == nil {
		t.Error("the repo's fsmonitor ran")
	}
}

// skill writes a skill file, named name, with raw as its content.
func skill(dir, name string, raw []byte) {
	os.MkdirAll(dir+"/.claude/skills/x", 0o755)
	os.WriteFile(dir+"/.claude/skills/x/"+name, raw, 0o644)
}

// sessionVars is the one list of Claude Code's own session marks. Run
// inside a Claude Code shell, every CLAUDE_ variable there is in it, or is
// one of the user's own settings (audit sec-trust-3: the test used to
// look only for the marks it knew).
func TestSessionVarsCoverAClaudeCodeShell(t *testing.T) {
	if os.Getenv("CLAUDECODE") == "" {
		t.Skip("not inside a Claude Code shell")
	}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "CLAUDE") && !slices.Contains(sessionVars, name) && !usersOwn.MatchString(name) {
			t.Errorf("%s isn't in sessionVars, and isn't a setting of the user's", name)
		}
	}
}

// usersOwn are the CLAUDE_ variables a user sets for Claude Code (its
// provider, login, limits and features), which sessions keep.
var usersOwn = regexp.MustCompile(`^CLAUDE_(CONFIG_DIR|ENV_FILE|BASH_.*|CODE_(USE_|DISABLE_|ENABLE_|SKIP_|MAX_|API_KEY|OAUTH_|IDE_|AUTO_CONNECT_IDE|SUBAGENT_MODEL|SHELL|GIT_BASH_PATH|EXTRA_BODY|TMPDIR|CLIENT_|EFFORT_LEVEL|ADDITIONAL_|PROXY|DONT_INHERIT_ENV|EXIT_AFTER_STOP_DELAY|SIMPLE|SHELL_PREFIX|BLOCKING_LIMIT_OVERRIDE|AUTO_COMPACT_WINDOW|FILE_READ_MAX_OUTPUT_TOKENS).*)$`)

// The guard itself: the marks Claude Code strips for child sessions fail
// it when they're missing from sessionVars, and a user's settings pass.
func TestTheSessionVarsGuardCatchesNewMarks(t *testing.T) {
	for _, name := range []string{"CLAUDE_CODE_BRIDGE_SESSION_ID", "CLAUDE_BG_SESSION_PERMISSION_RULES", "CLAUDE_CODE_HOST_WORKTREE_FENCE", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_SOMETHING_NEW"} {
		if usersOwn.MatchString(name) {
			t.Errorf("%s would pass as the user's", name)
		}
	}
	for _, name := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_MAX_OUTPUT_TOKENS"} {
		if !usersOwn.MatchString(name) {
			t.Errorf("%s would fail", name)
		}
	}
}

// The every-chat hook is read in its form, and in the form before
// 2>/dev/null was added, so Outdated offers to rewrite the older one.
func TestBothHookFormsAreRead(t *testing.T) {
	for _, c := range []string{HookCommand("/opt/homebrew/bin/cadrei"), "test -x /opt/homebrew/bin/cadrei && /opt/homebrew/bin/cadrei hook orchestrator; exit 0"} {
		if bin, ok := OrchestratorHook(c); !ok || bin != "/opt/homebrew/bin/cadrei" {
			t.Errorf("%q: %q %v", c, bin, ok)
		}
	}
	// A path that holds " && " is read whole (review of fix round 4).
	odd := "/Users/me/a && b/cadrei"
	if bin, ok := OrchestratorHook(HookCommand(odd)); !ok || bin != odd {
		t.Errorf("%q: %q %v", HookCommand(odd), bin, ok)
	}
	if !strings.HasSuffix(HookCommand("/x/cadrei"), " hook orchestrator 2>/dev/null; exit 0") {
		t.Errorf("form: %q", HookCommand("/x/cadrei"))
	}
	// A path in 0.2.0's bare form, as 0.2.0 wrote it.
	for _, c := range []string{"/opt/homebrew/bin/cadrei hook orchestrator", HookCommand(odd)} {
		if _, ok := OrchestratorHook(c); !ok {
			t.Errorf("%q isn't read", c)
		}
	}
	// Shell syntax that splitting leaves in a word isn't cadrei's form,
	// though the words look like it (review of fix round 5, tests N5a): sh
	// would run something else.
	for _, c := range []string{
		"test -x $(evil)/x/cadrei && $(evil)/x/cadrei hook orchestrator; exit 0",
		"test -x `evil`/x/cadrei && `evil`/x/cadrei hook orchestrator 2>/dev/null; exit 0",
		"test -x evil;/x/cadrei && evil;/x/cadrei hook orchestrator; exit 0",
		"test -x /x/cadrei '&&' /x/cadrei hook orchestrator; exit 0",
		"test -x \"/x/$HOME/cadrei\" && \"/x/$HOME/cadrei\" hook orchestrator; exit 0",
		"test  -x /x/cadrei && /x/cadrei hook orchestrator; exit 0",
		"$(evil)/x/cadrei hook orchestrator",
		"evil;/x/cadrei hook orchestrator",
	} {
		if bin, ok := OrchestratorHook(c); ok {
			t.Errorf("%q read as cadrei's, running %q", c, bin)
		}
	}
}

// Review of fix round 5 (N7): what Claude Code may load from outside a
// project's folder counts too: a linked worktree's main checkout (its
// skills, agents, commands and local settings), and every folder from a
// submodule up to its superproject's top.
func TestRiskyLooksBeyondAWorktreeOrASubmodule(t *testing.T) {
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "protocol.file.allow=always", "-c", "user.name=T", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	repo := func(dir string) string {
		os.MkdirAll(dir, 0o755)
		git(dir, "init", "-q")
		os.WriteFile(dir+"/README", []byte("x\n"), 0o644)
		git(dir, "add", "README")
		git(dir, "commit", "-qm", "first")
		return dir
	}
	skill := func(dir string) {
		os.MkdirAll(dir+"/.claude/skills/x", 0o755)
		os.WriteFile(dir+"/.claude/skills/x/SKILL.md", []byte("Just words.\n"), 0o644)
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())

	main := repo(root + "/main")
	git(main, "worktree", "add", "-q", root+"/wt")
	if got := (trust{}).Risky(root + "/wt"); len(got) != 0 {
		t.Errorf("a worktree of a plain repo: %v", got)
	}
	skill(main)
	os.WriteFile(main+"/.claude/settings.local.json", []byte("{}\n"), 0o644)
	if got := strings.Join((trust{}).Risky(root+"/wt"), ","); got != "../main/.claude/skills,../main/.claude/settings.local.json" {
		t.Errorf("a worktree: %q", got)
	}
	if got := strings.Join((trust{}).Risky(main), ","); got != ".claude/settings.local.json,.claude/skills" {
		t.Errorf("the main checkout itself: %q", got)
	}

	super := repo(root + "/super")
	sub := repo(root + "/sub")
	git(super, "submodule", "add", "-q", sub, "libs/sub")
	if got := (trust{}).Risky(super + "/libs/sub"); len(got) != 0 {
		t.Errorf("a submodule of a plain repo: %v", got)
	}
	skill(super)
	os.WriteFile(super+"/.claude/settings.local.json", []byte("{}\n"), 0o644)
	// The shared settings at a superproject's top count too (review of
	// fix round 6: Claude Code's docs don't say it doesn't read them).
	os.WriteFile(super+"/.claude/settings.json", []byte("{}\n"), 0o644)
	os.MkdirAll(super+"/libs/.claude/agents", 0o755)
	os.WriteFile(super+"/libs/.claude/agents/a.md", []byte("Just words.\n"), 0o644)
	os.WriteFile(super+"/libs/.claude/settings.local.json", []byte("{}\n"), 0o644)
	if got := strings.Join((trust{}).Risky(super+"/libs/sub"), ","); got != "../.claude/agents,../../.claude/skills,../../.claude/settings.json,../../.claude/settings.local.json" {
		t.Errorf("a submodule: %q", got)
	}
	// Through a link to it, and from a linked worktree of it (review of
	// fix round 6).
	os.Symlink(super+"/libs/sub", root+"/link")
	if got := strings.Join((trust{}).Risky(root+"/link"), ","); got != "../.claude/agents,../../.claude/skills,../../.claude/settings.json,../../.claude/settings.local.json" {
		t.Errorf("a submodule through a link: %q", got)
	}
	git(super+"/libs/sub", "worktree", "add", "-q", root+"/subwt")
	if got := strings.Join((trust{}).Risky(root+"/subwt"), ","); got != "../super/libs/.claude/agents,../super/.claude/skills,../super/.claude/settings.json,../super/.claude/settings.local.json" {
		t.Errorf("a worktree of a submodule: %q", got)
	}
}
