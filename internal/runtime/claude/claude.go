// Package claude is cadrei's adapter for Claude Code, the only runtime in
// 0.2.0. Everything cadrei knows about Claude Code lives here: its command
// and flags, its settings format and rule grammar (the allow checker), its
// .claude folders, ~/.claude.json trust and its hooks.
package claude

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/gitx"
	"github.com/rafi-ramdhani/cadrei/internal/jsonx"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/runtime/claude/allow"
	"github.com/rafi-ramdhani/cadrei/internal/runtime/claude/settings"
)

func init() { runtime.RegisterDefault(Claude{}) }

// Claude is the Claude Code adapter.
type Claude struct{}

func (Claude) Name() string  { return "claude" }
func (Claude) Title() string { return "Claude Code" }
func (Claude) Model() string { return "Claude" }

func (Claude) ModelHint() string {
	return "an alias such as opus, sonnet or fable, or a full name such as claude-sonnet-5-5"
}

func (Claude) Help() runtime.InstallHelp {
	return runtime.InstallHelp{Install: "brew install --cask claude-code", Reinstall: "brew reinstall --cask claude-code", URL: "https://claude.com/claude-code", Login: "claude auth login"}
}

// Login is claude auth login: it opens the browser, or takes a pasted
// code, in the user's own terminal.
func (c Claude) Login() (runtime.Command, error) {
	in, err := c.Detect()
	if err != nil {
		return runtime.Command{}, err
	}
	return runtime.Command{Argv: []string{in.Path, "auth", "login"}, Unset: sessionVars}, nil
}

func (Claude) Caps() runtime.Capabilities {
	return runtime.Capabilities{
		FixedDenies:     true,
		PermissionModes: []string{"default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"},
		Resume:          true, AssignSessionID: true,
		Trust: true, Instructions: true, OrchestratorHook: true, Messaging: runtime.Native,
	}
}

// Detect finds the claude command.
func (Claude) Detect() (runtime.Install, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return runtime.Install{}, errors.New("Claude Code (claude) is not on your PATH; install it from https://claude.com/claude-code")
	}
	return runtime.Install{Path: path}, nil
}

// Launch builds the claude command for a session. The prompt file is
// appended to Claude Code's system prompt, and a member gets its
// validated settings copy.
func (c Claude) Launch(s runtime.LaunchSpec) (runtime.Command, error) {
	in, err := c.Detect()
	if err != nil {
		return runtime.Command{}, err
	}
	argv := []string{in.Path, "--name", s.Name, "--permission-mode", s.Mode}
	if s.PromptFile != "" {
		argv = append(argv, "--append-system-prompt-file", s.PromptFile)
	}
	switch {
	case s.Grants != "" && s.Settings != "":
		return runtime.Command{}, errors.New("a session gets grants or cadrei's own settings, not both")
	case s.Grants != "":
		argv = append(argv, "--settings", s.Grants)
	case s.Settings != "":
		argv = append(argv, "--settings", s.Settings)
	}
	switch {
	case s.Resume != "":
		argv = append(argv, "--resume", s.Resume)
	case s.SessionID != "":
		argv = append(argv, "--session-id", s.SessionID)
	}
	// On a resume too (T-M1); each value is its own argument.
	if s.Model != "" {
		argv = append(argv, "--model", s.Model)
	}
	if s.Effort != "" {
		argv = append(argv, "--effort", s.Effort)
	}
	if len(s.Allowed) > 0 {
		// Exact rules: other flags, a pipe, a redirect or && still ask.
		argv = append(argv, "--allowedTools")
		for _, c := range s.Allowed {
			argv = append(argv, "Bash("+c+")")
		}
	}
	if s.Prompt != "" {
		// After --, since --allowedTools takes every word after it.
		argv = append(argv, "--", s.Prompt)
	}
	unset := sessionVars
	if s.Role == runtime.Orchestrator {
		// The orchestrator opened in the user's own terminal keeps the
		// IDE connection that terminal has (an IDE's built-in one).
		unset = slices.DeleteFunc(slices.Clone(sessionVars), func(v string) bool { return v == ideVar })
	}
	var env []string
	if s.ConfigHere {
		// In tmux, a server started earlier may have another config dir:
		// the login went to this one.
		if dir, ok := os.LookupEnv(configVar); ok {
			env = append(env, configVar+"="+dir)
		} else {
			unset = append(slices.Clone(unset), configVar)
		}
	}
	return runtime.Command{Argv: argv, Dir: s.WorkDir, Env: env, Unset: unset}, nil
}

// configVar is the folder Claude Code keeps its config and login in.
const configVar = "CLAUDE_CONFIG_DIR"

// ideVar is an IDE's connection: a member never takes the one of the
// session that started it.
const ideVar = "CLAUDE_CODE_SSE_PORT"

// sessionVars are what Claude Code sets for the programs it runs, about
// its own session, and what it removes itself when it starts a child
// session. Run from inside Claude Code (the orchestrator's cadrei up), a
// new session would take them as its own: as a child session with no
// transcript, on the parent's messaging socket, bridge, effort and id.
// The user's own settings (the API key, CLAUDE_CONFIG_DIR, the provider)
// stay.
var sessionVars = []string{
	"CLAUDECODE",
	"CLAUDE_BG_SESSION_PERMISSION_RULES",
	"CLAUDE_CODE_BRIDGE_SESSION_ID",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_CHROME_MCP_ORG_DENIED",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_EVAL_INTERVIEW_SESSION",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDE_CODE_HOST_WORKTREE",
	"CLAUDE_CODE_HOST_WORKTREE_FENCE",
	"CLAUDE_CODE_MESSAGING_SOCKET",
	"CLAUDE_CODE_MESSAGING_TOKEN",
	"CLAUDE_CODE_SESSION_ATTENDED",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_SESSION_KIND",
	ideVar,
	"CLAUDE_EFFORT",
	"CLAUDE_PID",
}

func (Claude) SessionVars() []string { return sessionVars }

// SetupVars: CLAUDE_CONFIG_DIR moves Claude Code's whole config (ConfigDir).
func (Claude) SetupVars() []string { return []string{"CLAUDE_CONFIG_DIR"} }

// BuildDir is <cadrei>/.claude/build: inside a .claude folder, which Claude
// Code protects, and denied to members (N.7).
func (Claude) BuildDir(cadrei string) string { return filepath.Join(cadrei, ".claude", "build") }

func (Claude) Permissions() runtime.PermissionOps { return permissions{} }
func (Claude) Trust() runtime.TrustOps            { return trust{} }

type permissions struct{}

func (permissions) GrantsFile(cadrei string) string { return filepath.Join(cadrei, settings.Rel) }

func (permissions) Validate(cadrei string, known []string, rule string, auto bool) (string, error) {
	c := allow.New(cadrei, known)
	if auto {
		return c.Auto(rule)
	}
	return c.Rule(rule)
}

// hashFile keeps the settings fingerprints, one file for the machine.
func hashFile() string { return cadreis.Config("member-settings.sha256") }

// Prepare makes sure the cadrei has its settings file and writes the
// validated copy a member starts with; a file that cannot be used gives no
// copy and a warning, and an edit made outside cadrei allow a warning.
func (p permissions) Prepare(cadrei string, places runtime.Places) runtime.Prepared {
	var out runtime.Prepared
	file := p.GrantsFile(cadrei)
	if _, err := os.Stat(file); err != nil {
		os.MkdirAll(filepath.Dir(file), 0o755)
		if err := settings.Create(file); err != nil {
			out.Warnings = append(out.Warnings, "Couldn't create "+cadreis.Tilde(file)+": "+err.Error()+".")
			return out
		}
		settings.Record(file, hashFile())
		// Without a git identity it stays uncommitted, silently (D3).
		cadreis.Commit(cadrei, "Add the member settings file", settings.Rel)
	}
	build := Claude{}.BuildDir(cadrei)
	sp := settings.Places{Root: places.Root, Archive: places.Archive, Cadreis: places.Cadreis}
	if places.Binary != "" {
		sp.SessionHook = SessionHook(places.Binary)
		sp.StateHook, sp.StateEvents = StateHook(places.Binary), stateEvents
		sp.GuardHook = GuardHook(places.Binary)
	}
	copyPath, err := settings.Export(file, build, sp)
	if err != nil {
		// The member still gets every deny rule, and no grants.
		out.Warnings = append(out.Warnings, fmt.Sprintf("Your team starts with nothing extra allowed, only cadrei's limits, because %s can't be used: %s. Fix it, or restore it with git -C %s checkout -- %s", cadreis.Tilde(file), err, cadreis.Tilde(cadrei), settings.Rel))
		if out.Grants, err = settings.ExportDenyOnly(build, sp); err != nil {
			out.Warnings = append(out.Warnings, "Couldn't write the limits-only copy: "+err.Error()+".")
		}
		return out
	}
	out.Grants = copyPath
	if g, err := settings.OpenGrants(file); err == nil && g.HasOnce() {
		out.Notes = append(out.Notes, "One-time permissions are still in place. See them with cadrei allow list, and remove each when its task is done.")
	}
	if settings.ChangedOutside(cadrei, file, hashFile()) {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%s was changed outside cadrei allow. Your team still gets it, since it's valid. See what changed with git -C %s log -p -- %s", cadreis.Tilde(file), cadreis.Tilde(cadrei), settings.Rel))
	}
	return out
}

// Config is Claude Code's config file, where folder trust is kept.
func Config() string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir = paths.Home()
	}
	return filepath.Join(dir, ".claude.json")
}

type trust struct{}

// Protected: Claude Code loads skills, commands, hooks and settings from
// ~/.claude, so no project may live there.
func (trust) Protected() []string { return []string{filepath.Join(paths.Home(), ".claude")} }

// projectFiles are what Claude Code reads from a session's folder once the
// folder is trusted: settings (with hooks and permissions), commands,
// agents, skills, output styles, MCP servers and instructions.
var projectFiles = []string{
	".claude/settings.json", ".claude/settings.local.json", ".claude/commands", ".claude/agents",
	".claude/skills", ".claude/output-styles", ".mcp.json", "CLAUDE.md", "CLAUDE.local.md",
}

func (trust) Loaded(dir string) []string {
	var out []string
	for _, f := range projectFiles {
		if _, err := os.Lstat(filepath.Join(dir, f)); err == nil {
			out = append(out, f)
		}
	}
	return out
}

// riskyFiles are the project files that can run commands (hooks) or add
// tools (MCP servers) for a session. CLAUDE.md alone is instructions.
var riskyFiles = []string{".claude/settings.json", ".claude/settings.local.json", ".mcp.json"}

// Risky lists what in a project could run for the team once its folder is
// trusted: the settings files at its top, and at any depth a skills,
// agents or commands folder with anything in it (a link included), or
// .claude as a link. Claude Code loads a nested folder's skills when a
// member works on a file under it, unless git ignores the folder, so only
// .git and what git ignores are left out. Every name is matched in any
// case, as a case-insensitive disk opens it. What Claude Code may load
// from outside the folder counts too (beyond).
func (t trust) Risky(dir string) []string {
	return append(t.within(dir), beyond(dir)...)
}

// beyond lists what Claude Code may load for a project from outside its
// folder, as paths relative to it. For a linked worktree, that's the main
// checkout's skills, agents and commands (taken when the worktree has
// none of its own) and its local settings file. For a submodule, it's the
// same in every folder from the project up to its superproject's top,
// which Claude Code may take for the repository's root, and so on up a
// submodule of a submodule; for a linked worktree of a submodule, from
// the submodule's own checkout up. git is asked with Reading's flags, so
// nothing in the repo's settings runs. The walk up is by physical folders.
func beyond(dir string) []string {
	dir = paths.Real(dir)
	var out []string
	add := func(root string, settings bool) {
		rel := func(p string) string {
			r, _ := filepath.Rel(dir, p)
			return filepath.ToSlash(r)
		}
		for _, c := range spelled(root, ".claude") {
			if st, err := os.Lstat(c); err == nil && st.Mode()&fs.ModeSymlink != 0 {
				out = append(out, rel(c))
				continue
			}
			for _, d := range riskyDirs {
				for _, q := range spelled(c, d) {
					if st, err := os.Lstat(q); err == nil && (!st.IsDir() || !empty(q)) {
						out = append(out, rel(q))
					}
				}
			}
			if settings {
				// Both settings files: whether Claude Code reads the
				// shared one from a superproject's top too isn't
				// documented, so it counts as if it does.
				for _, f := range []string{"settings.json", "settings.local.json"} {
					for _, q := range spelled(c, f) {
						out = append(out, rel(q))
					}
				}
			}
		}
	}
	git := func(d string, args ...string) string {
		raw, err := gitx.Reading(d, args...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(raw))
	}
	// A linked worktree: its git folder isn't the common one, whose
	// parent is the main checkout (none for a bare repository). A
	// submodule's common folder is in its superproject's .git/modules,
	// and names its checkout as core.worktree.
	start := dir
	if both := strings.Split(git(dir, "rev-parse", "--git-dir", "--git-common-dir"), "\n"); len(both) == 2 {
		for i, p := range both {
			if !filepath.IsAbs(p) {
				both[i] = filepath.Join(dir, p)
			}
		}
		if paths.Real(both[0]) != paths.Real(both[1]) {
			main := ""
			if strings.EqualFold(filepath.Base(both[1]), ".git") {
				main = filepath.Dir(both[1])
			} else if wt := git(both[1], "config", "--file", filepath.Join(both[1], "config"), "--get", "core.worktree"); wt != "" {
				if !filepath.IsAbs(wt) {
					wt = filepath.Join(both[1], wt)
				}
				main = paths.Real(wt)
			}
			if main != "" {
				add(main, true)
				start = main
			}
		}
	}
	for sub := start; ; {
		super := git(sub, "rev-parse", "--show-superproject-working-tree")
		if super == "" || !filepath.IsAbs(super) {
			break
		}
		for p := filepath.Dir(sub); ; p = filepath.Dir(p) {
			add(p, paths.Real(p) == paths.Real(super))
			if paths.Real(p) == paths.Real(super) || p == filepath.Dir(p) {
				break
			}
		}
		sub = super
	}
	return out
}

// within is what Risky finds in the project's own folder.
func (trust) within(dir string) []string {
	var out []string
	for _, f := range riskyFiles {
		for _, p := range spelled(dir, f) {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
	}
	ignored := ignoredIn(dir)
	seen := 0
	filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		link := e.Type()&fs.ModeSymlink != 0
		if !e.IsDir() && !link {
			return nil
		}
		if seen++; seen > walkLimit {
			out = append(out, tooManyFolders)
			return filepath.SkipAll
		}
		switch {
		case strings.EqualFold(e.Name(), ".git"):
			return filepath.SkipDir
		case ignored[rel] && !top(rel):
			return filepath.SkipDir
		case !strings.EqualFold(e.Name(), ".claude"):
			return nil
		case link:
			// A linked .claude folder can point anywhere: what it holds
			// isn't the repo's to vouch for.
			out = append(out, rel)
			return nil
		}
		for _, d := range riskyDirs {
			for _, q := range spelled(p, d) {
				r, _ := filepath.Rel(dir, q)
				r = filepath.ToSlash(r)
				if ignored[r] && !top(rel) {
					continue
				}
				if st, err := os.Lstat(q); err == nil && (!st.IsDir() || !empty(q)) {
					out = append(out, r)
				}
			}
		}
		return nil
	})
	return out
}

// top reports whether rel is the project's own .claude folder, which
// Claude Code loads even when git ignores it.
func top(rel string) bool { return strings.EqualFold(rel, ".claude") }

// riskyDirs hold skills, agents and commands, under a .claude folder.
// They can pre-approve tools or add hooks while they're active, and their
// front matter is YAML, which has too many ways to spell a key to read
// safely, so anything in them counts.
var riskyDirs = []string{"skills", "agents", "commands"}

// walkLimit is how many folders Risky looks in. A project bigger than
// that counts as risky: what's past the limit wasn't checked.
var walkLimit = 200000

// tooManyFolders is Risky's entry for a project past walkLimit.
const tooManyFolders = "more folders than cadrei can check"

// ignoredIn is what git ignores in dir and doesn't track, as paths
// relative to dir: Claude Code doesn't load skills from there. Nothing is
// ignored outside a git repo, or when git can't say.
func ignoredIn(dir string) map[string]bool {
	out, err := gitx.Reading(dir, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory").Output()
	ignored := map[string]bool{}
	if err != nil {
		return ignored
	}
	// The paths are relative to dir, which may be inside the repo.
	for _, p := range strings.Split(string(out), "\x00") {
		if p = strings.TrimSuffix(p, "/"); p != "" {
			ignored[p] = true
		}
	}
	return ignored
}

// spelled finds rel under dir in any case, part by part, without following
// a link on the way (a linked .claude is reported as it is).
func spelled(dir, rel string) []string {
	found := []string{dir}
	for _, part := range strings.Split(rel, "/") {
		var next []string
		for _, d := range found {
			if st, err := os.Lstat(d); err != nil || !st.IsDir() {
				continue
			}
			entries, _ := os.ReadDir(d)
			for _, e := range entries {
				if strings.EqualFold(e.Name(), part) {
					next = append(next, filepath.Join(d, e.Name()))
				}
			}
		}
		found = next
	}
	return found
}

// empty reports whether a folder holds nothing at all, at any depth, but
// folders.
func empty(dir string) bool {
	clear := true
	filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || (p != dir && !e.IsDir()) {
			clear = false
			return filepath.SkipAll
		}
		return nil
	})
	return clear
}

func (trust) Mark(folders []runtime.Folder) ([]runtime.TrustResult, string) {
	return edit(folders, func(e []TrustEntry) jsonx.Op { return Trust(e) })
}

func (trust) Unmark(folders []runtime.Folder) ([]runtime.TrustResult, string) {
	return edit(folders, func(e []TrustEntry) jsonx.Op { return Untrust(e) })
}

// edit changes trust for folders in one write with one backup. Each folder
// is keyed by its physical path and, when it differs, by the path as cadrei
// spells it (section C).
func edit(folders []runtime.Folder, op func([]TrustEntry) jsonx.Op) ([]runtime.TrustResult, string) {
	cfg := Config()
	var entries []TrustEntry
	for _, f := range folders {
		phys := paths.Real(f.Dir)
		dirs := []string{phys}
		if abs, err := filepath.Abs(f.Dir); err == nil && filepath.Clean(abs) != phys {
			dirs = append(dirs, filepath.Clean(abs))
		}
		entries = append(entries, TrustEntry{Name: f.Name, Dirs: dirs})
	}
	if len(entries) == 0 {
		return nil, ""
	}
	code, lines := jsonx.Edit(cfg, jsonx.Options{Backup: cfg + ".bak-cadrei"}, op(entries))
	var results []runtime.TrustResult
	var note string
	switch code {
	case jsonx.Changed, jsonx.Unchanged:
		for _, l := range lines {
			name, state, _ := strings.Cut(l, "\t")
			results = append(results, runtime.TrustResult{Name: name, State: state})
		}
		return results, ""
	case jsonx.Missing:
		// Claude Code hasn't run yet: it asks, and that's all there is to say.
	case jsonx.KeptChanged:
		note = cadreis.Tilde(cfg) + " kept changing (a running Claude Code session?), so cadrei left it alone."
	case jsonx.WriteFailed:
		note = "cadrei couldn't write next to " + cadreis.Tilde(cfg) + " (a folder it can't write to, or a full disk?), so it left it alone."
	default:
		note = cadreis.Tilde(cfg) + " isn't a file cadrei can safely edit (unreadable, not valid JSON, an unexpected shape, or owned by another user), so cadrei left it alone."
	}
	for _, e := range entries {
		results = append(results, runtime.TrustResult{Name: e.Name, State: "skipped"})
	}
	return results, note
}
