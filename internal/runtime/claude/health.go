package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/jsonx"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/runtime/claude/settings"
)

// ConfigDir is Claude Code's config folder: CLAUDE_CONFIG_DIR, else
// ~/.claude. User skills and settings.json live in it.
func ConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(paths.Home(), ".claude")
}

// authStatusSince is the first version whose docs list `claude auth
// status` (with configDirectory). Older versions are not asked: an
// unknown subcommand could be read as a prompt.
var authStatusSince = [3]int{2, 1, 268}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func parseVersion(s string) ([3]int, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

// versionText writes a version as people read it: 2.1.295.
func versionText(v [3]int) string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

func atLeast(v, floor [3]int) bool {
	for i := range v {
		if v[i] != floor[i] {
			return v[i] > floor[i]
		}
	}
	return true
}

// output runs claude with args, no input and a time limit. Ending ctx
// ends it sooner.
func output(ctx context.Context, bin string, args ...string) ([]byte, error) {
	return outputWithin(ctx, 15*time.Second, bin, args...)
}

// authLimit is how long the login check may take: offline, claude auth
// status can wait a long time, and setup shows the check as it runs.
const authLimit = 3 * time.Second

func outputWithin(parent context.Context, limit time.Duration, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = nil
	// At the limit, everything it started goes too, and a child that
	// keeps the output open can't hold cadrei past it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 500 * time.Millisecond
	return cmd.Output()
}

func (c Claude) Health(full bool, cadreiDirs []string) []runtime.Problem {
	var out []runtime.Problem
	help := c.Help()
	if _, err := c.Detect(); err != nil {
		fix := "Install it from " + help.URL
		if _, err := exec.LookPath("brew"); err == nil {
			fix = "Install it with " + help.Install + ", or see " + help.URL
		}
		return []runtime.Problem{{What: runtime.Probe{}.Says(c.Title()), Fatal: true, Fix: fix}}
	}
	// The full check asks Claude Code as the first run's screen does: the
	// one Probe, so both say the same.
	if full {
		switch p := c.Probe(context.Background()); {
		case !p.Runs:
			out = append(out, runtime.Problem{What: p.Says(c.Title()), Fatal: true, Fix: "Reinstall it with " + help.Reinstall})
		case p.Login == runtime.LoggedOut:
			out = append(out, runtime.Problem{What: p.Says(c.Title()), Fix: "Log in with " + help.Login + "."})
		case p.Login != runtime.LoggedIn:
			out = append(out, runtime.Problem{What: p.Says(c.Title())})
		}
	}
	// Member settings with no record here and no commit (a crew without
	// a git identity, after ~/.cadrei/config was deleted): taken as they
	// are, and said so.
	for _, d := range cadreiDirs {
		file := filepath.Join(d, settings.Rel)
		if full && settings.FirstUse(d, file, hashFile()) {
			settings.Record(file, hashFile())
			out = append(out, runtime.Problem{
				What: filepath.Base(d) + "'s team permissions (" + cadreis.Tilde(file) + ") have no record on this machine and no commit to check them against, so cadrei takes them as they are now.",
				Fix:  "See them with cadrei allow list."})
		}
	}
	// Once a cadrei folder is trusted, Claude Code loads these for its
	// orchestrator; cadrei never writes them (N.7).
	for _, d := range cadreiDirs {
		for _, f := range []string{".claude/settings.json", ".claude/settings.local.json", "CLAUDE.md", "CLAUDE.local.md", ".mcp.json"} {
			p := filepath.Join(d, f)
			if _, err := os.Lstat(p); err == nil {
				out = append(out, runtime.Problem{
					What: cadreis.Tilde(p) + " exists. Claude Code loads it into your orchestrator, and cadrei never writes it.",
					Fix:  "Check what's in it, and remove it unless you added it."})
			}
		}
	}
	return out
}

func (Claude) Instructions() runtime.InstructionOps { return instructions{} }
func (Claude) Hooks() runtime.HookOps               { return hooks{} }

type instructions struct{}

// Path is the user skill folder Claude Code reads cadrei's skill from.
func (instructions) Path() string { return filepath.Join(ConfigDir(), "skills", "cadrei") }

func (i instructions) Target() (string, error) {
	p := i.Path()
	st, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if st.Mode()&fs.ModeSymlink == 0 {
		return "", fmt.Errorf("%s: %w", p, runtime.ErrNotLink)
	}
	to, err := os.Readlink(p)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(to) {
		to = filepath.Join(filepath.Dir(p), to)
	}
	return filepath.Clean(to), nil
}

func (i instructions) Link(dir string) error {
	p := i.Path()
	if _, err := i.Target(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// A new link renamed over the old one, so the skill is never missing.
	tmp := fmt.Sprintf("%s.cadrei-%d", p, os.Getpid())
	os.Remove(tmp)
	if err := os.Symlink(dir, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (i instructions) Unlink(dir string) (bool, error) {
	target, err := i.Target()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if paths.Real(target) != paths.Real(dir) {
		return false, nil
	}
	return true, os.Remove(i.Path())
}

// OldClone needs every part of a 0.1.x clone, so a link into a repository
// of the user's own is never taken for one.
func (instructions) OldClone(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false
	}
	for _, f := range []string{"bin/cadre", "bin/orchestrator-hook.sh", "skills/cadre/SKILL.md"} {
		if st, err := os.Stat(filepath.Join(dir, f)); err != nil || !st.Mode().IsRegular() {
			return false
		}
	}
	return true
}

type hooks struct{}

func (hooks) File() string { return filepath.Join(ConfigDir(), "settings.json") }

func isOrchestratorHook(command string) bool {
	_, ok := OrchestratorHook(command)
	return ok
}

func (h hooks) Find() ([]string, error) {
	commands, err := h.commands()
	if err != nil {
		return nil, err
	}
	var found []string
	for _, c := range commands {
		if prog, ok := OrchestratorHook(c); ok {
			found = append(found, prog)
		}
	}
	return found, nil
}

// commands lists the SessionStart hook commands in the settings file.
func (h hooks) commands() ([]string, error) {
	raw, err := os.ReadFile(h.File())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	root, err := jsonx.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", h.File(), err)
	}
	_, groups, err := sessionStart(root, false)
	if err != nil || groups == nil {
		return nil, err
	}
	var found []string
	for _, g := range groups.Items {
		for _, hk := range g.Get("hooks").Items {
			if c, ok := commandOf(hk); ok {
				found = append(found, c)
			}
		}
	}
	return found, nil
}

// HookCommand is the every-chat hook's command for a cadrei binary.
func HookCommand(binary string) string { return hookCommand(binary, "orchestrator") }

// hookCommand is the form of every hook command cadrei writes: it runs
// `<binary> hook <sub>` only while the program is there, with its stderr
// dropped, and always ends with exit 0, so a removed program, or an older
// one that doesn't know sub, shows nothing at all. test is a shell
// builtin, and there's no exec, since a failed exec would end the shell
// before exit 0. cadrei's hooks never write to stderr themselves.
func hookCommand(binary, sub string) string {
	q := quote(binary)
	return "test -x " + q + " && " + q + " hook " + sub + " 2>/dev/null; exit 0"
}

// Outdated reports whether the every-chat hook for binary is written in
// an older form than HookCommand's, so Set would rewrite it.
func (h hooks) Outdated(binary string) (bool, error) {
	commands, err := h.commands()
	if err != nil {
		return false, err
	}
	bin := paths.Real(binary)
	for _, c := range commands {
		if prog, ok := OrchestratorHook(c); ok && paths.Real(prog) == bin && c != HookCommand(binary) {
			return true, nil
		}
	}
	return false, nil
}

func quote(w string) string {
	if w != "" && strings.Trim(w, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./=:@%+,") == "" {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}

func (h hooks) Set(binary string) (bool, error) {
	file := h.File()
	if _, err := os.Lstat(file); errors.Is(err, fs.ErrNotExist) {
		// Claude Code has no user settings yet: start them empty.
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return false, err
		}
		f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, err = f.WriteString("{}\n")
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil && !errors.Is(err, fs.ErrExist) {
			return false, err
		}
	}
	code, _ := jsonx.Edit(file, jsonx.Options{Backup: file + ".bak-cadrei"}, AddHook(HookCommand(binary), isOrchestratorHook))
	switch code {
	case jsonx.Changed:
		return true, nil
	case jsonx.Unchanged:
		return false, nil
	case jsonx.KeptChanged:
		return false, fmt.Errorf("%s kept changing (a running Claude Code session?), so it was left unchanged; try again", file)
	case jsonx.WriteFailed:
		return false, fmt.Errorf("could not write next to %s, so it was left unchanged", file)
	}
	return false, fmt.Errorf("%s is not a file cadrei can safely edit (unreadable, not valid JSON, an unexpected shape, or owned by another user), so it was left unchanged", file)
}

func (h hooks) Remove(binary string) (bool, error) {
	file := h.File()
	if _, err := os.Lstat(file); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	bin := paths.Real(binary)
	isOurs := func(c string) bool {
		prog, ok := OrchestratorHook(c)
		return ok && paths.Real(prog) == bin
	}
	code, _ := jsonx.Edit(file, jsonx.Options{Backup: file + ".bak-cadrei"}, Unhook(isOurs, isOrchestratorHook))
	switch code {
	case jsonx.Changed:
		return true, nil
	case jsonx.Unchanged, jsonx.Missing:
		return false, nil
	case jsonx.KeptChanged:
		return false, fmt.Errorf("%s kept changing (a running Claude Code session?), so it was left unchanged; try again", file)
	case jsonx.WriteFailed:
		return false, fmt.Errorf("could not write next to %s, so it was left unchanged", file)
	}
	return false, fmt.Errorf("%s is not a file cadrei can safely edit, so it was left unchanged", file)
}

// Output is a SessionStart hook's answer: text added to the new session's
// context.
func (hooks) Output(text string) []byte {
	type specific struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	}
	b, _ := json.Marshal(struct {
		HookSpecificOutput specific `json:"hookSpecificOutput"`
	}{specific{"SessionStart", text}})
	return append(b, '\n')
}

// SnapshotSettings writes <dir>/orchestrator-settings.json, holding the
// session start hook that runs `<binary> hook snapshot`, the prompt hook
// that runs `<binary> hook team-status`, and the hook after each
// SendMessage call that runs `<binary> hook notify`. Claude Code adds their
// output to the session's context (the snapshot on startup, on resume and
// after /clear and /compact; the team status on each prompt; the notify
// note after a send), and shows none of it in the chat.
func (hooks) SnapshotSettings(dir, binary string) (string, error) {
	entry := func(sub, timeout, matcher string) *jsonx.Value {
		hook := jsonx.NewObject("type", jsonx.String("command"), "command", jsonx.String(hookCommand(binary, sub)), "timeout", jsonx.Literal(timeout))
		group := jsonx.NewObject("hooks", &jsonx.Value{Kind: jsonx.Array, Items: []*jsonx.Value{hook}})
		if matcher != "" {
			group = jsonx.NewObject("matcher", jsonx.String(matcher), "hooks", &jsonx.Value{Kind: jsonx.Array, Items: []*jsonx.Value{hook}})
		}
		return &jsonx.Value{Kind: jsonx.Array, Items: []*jsonx.Value{group}}
	}
	root := jsonx.NewObject("hooks", jsonx.NewObject("SessionStart", entry("snapshot", "10", ""), "UserPromptSubmit", entry("team-status", "5", ""),
		"PostToolUse", entry("notify", "5", "SendMessage")))
	path := filepath.Join(dir, "orchestrator-settings.json")
	return path, fsx.WriteFile(path, jsonx.Format(root, "  "), 0o600)
}

// Probe runs claude --version, and claude auth status when this version
// has it (3 seconds at most: offline it can wait much longer). Ending ctx
// ends both, and what they started.
func (c Claude) Probe(ctx context.Context) runtime.Probe {
	in, err := c.Detect()
	if err != nil {
		return runtime.Probe{}
	}
	p := runtime.Probe{Installed: true, Login: runtime.LoginUnsure}
	raw, err := output(ctx, in.Path, "--version")
	v, ok := parseVersion(string(raw))
	if err != nil || !ok {
		return p
	}
	p.Runs, p.Version = true, versionText(v)
	if !atLeast(v, authStatusSince) {
		return p
	}
	raw, err = outputWithin(ctx, authLimit, in.Path, "auth", "status")
	var exit *exec.ExitError
	isJSON := json.Valid(bytes.TrimSpace(raw))
	switch {
	case err == nil && isJSON:
		p.Login = runtime.LoggedIn
	case errors.As(err, &exit) && exit.ExitCode() == 1 && isJSON:
		p.Login = runtime.LoggedOut
	}
	return p
}
