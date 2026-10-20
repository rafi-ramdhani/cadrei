package session

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// Up starts a team, or one of its roles, for this cadrei.
type Up struct {
	Scope
	Team     string
	Role     string // "" for every role of the team
	Project  string // the project part of the key, or ""
	Target   string // @cadrei_target: the registry name or folder it was started for
	Dir      string // the working folder
	Explicit bool   // Dir was given (a project or folder): a role's .workdir pin does not apply
	Protocol []byte // the member protocol every prompt starts with
	Mode     string // the permission mode
	// Pick returns a role's runtime and the grants artifact it starts with
	// ("" for none), or why the role cannot start.
	Pick func(role string) (runtime.Runtime, string, error)
	// Wait is the longest the start check looks at a new member (3
	// seconds when 0).
	Wait time.Duration
	// Fresh starts new conversations instead of resuming recorded ones.
	Fresh bool
	// StateHooks says the members start with hooks that report their
	// state, so a new start is "starting" until the first one writes.
	StateHooks bool
	// Version is the cadrei that starts the session, recorded on it as
	// @cadrei_version, so a later cadrei can tell its sessions apart (none
	// is 0.2.0 or older).
	Version string
	// Override is the model and effort given for this start only, and
	// Defaults the crew's (its local cadrei.conf over its cadrei.conf).
	// A member file's front matter comes between them (7.11).
	Override, Defaults Choice
	// Report, when set, is given what happened to each role, in place of
	// the plain lines Start prints.
	Report func(Result)
}

// Result is what happened to one role on Start.
type Result struct {
	Role    string
	Started bool   // it started, or picked its chat up again
	Resumed bool   // it picked its last chat up again
	Note    string // the conversation note, when it started fresh
	Dir     string // the folder it runs in, when it started
	Waiting bool   // it started on a screen that waits on the user, as a login does
	Running bool   // it was running already
	Legacy  string // the 0.1.x session it runs in, when it does
	Err     error  // why it didn't start
	// Choice is the model and effort it started with, and Was those of its
	// last start (of the running one, when it was running already).
	Choice, Was Choice
}

// ExitedError is a command that ended as soon as it started.
type ExitedError struct {
	Name string // the session or member
	Dir  string // where to run it by hand
	Line string // the command, with its environment
	Last string // the last line it wrote, when it could be read
}

func (e *ExitedError) Error() string {
	return fmt.Sprintf("%s failed to start: its command exited at once; run it by hand in %s to see why:\n    %s", e.Name, e.Dir, e.Line)
}

// Roles lists a team's roles: the .md files in members/<team>.
func Roles(cadrei, team string) []string {
	files, _ := filepath.Glob(filepath.Join(cadrei, "members", team, "*.md"))
	var out []string
	for _, f := range files {
		out = append(out, strings.TrimSuffix(filepath.Base(f), ".md"))
	}
	sort.Strings(out)
	return out
}

// workdir reads the first line of a .workdir file, with ~ expanded.
func workdir(file string) string {
	raw, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	line = strings.TrimSpace(line)
	if line == "~" || strings.HasPrefix(line, "~/") {
		line = paths.Home() + line[1:]
	}
	return line
}

// DefaultDir is a team's working folder: its .workdir file, or
// <cadrei>/teams/<team>.
func DefaultDir(cadrei, team string) string {
	if d := workdir(filepath.Join(cadrei, "members", team, ".workdir")); d != "" {
		return d
	}
	return filepath.Join(cadrei, "teams", team)
}

// Start starts the roles and prints a line each. It reports whether any
// failed to start, and the roles it started (or resumed).
func (u Up) Start(out io.Writer) (failed bool, started []string) {
	roles := []string{u.Role}
	if u.Role == "" {
		roles = Roles(u.Path, u.Team)
	}
	for _, r := range roles {
		res := u.start(r)
		res.Role = r
		if res.Err != nil {
			failed = true
			res.Choice = u.ChoiceOf(r)
		}
		if res.Started {
			started = append(started, r)
		}
		if u.Report != nil {
			u.Report(res)
		} else {
			u.print(out, res)
		}
	}
	return failed, started
}

// print writes a role's result as a plain line.
func (u Up) print(out io.Writer, res Result) {
	name := MemberName(u.Name, Key(u.Team, u.Project), res.Role)
	switch {
	case res.Err != nil:
		fmt.Fprintf(out, "  %s not started: %s\n", name, res.Err)
	case res.Legacy != "":
		fmt.Fprintf(out, "  %s-%s already running (legacy session %s; it keeps its old name until restarted)\n", Key(u.Team, u.Project), res.Role, res.Legacy)
	case res.Running:
		fmt.Fprintf(out, "  %s already running\n", name)
	case res.Resumed:
		fmt.Fprintf(out, "  %s started in %s (resumed its conversation)\n", name, res.Dir)
	case res.Note != "":
		fmt.Fprintf(out, "  %s started in %s (%s)\n", name, res.Dir, res.Note)
	default:
		fmt.Fprintf(out, "  %s started in %s\n", name, res.Dir)
	}
	if res.Waiting {
		fmt.Fprintf(out, "  %s waits on a login or setup screen; answer it in its window\n", name)
	}
}

func (u Up) start(role string) Result {
	res, err := u.starting(role)
	res.Err = err
	return res
}

func (u Up) starting(role string) (Result, error) {
	key := Key(u.Team, u.Project)
	session := SessionName(u.Name, key)
	name := MemberName(u.Name, key, role)
	memberFile := filepath.Join(u.Path, "members", u.Team, role+".md")
	if _, err := os.Stat(memberFile); err != nil {
		return Result{}, fmt.Errorf("there's no team member %s/%s", u.Team, role)
	}
	if u.T.HasWindow(session, role) && u.mine(u.Team, u.Project) == session {
		// What it runs on matters only to an override, which can't reach it.
		var was Choice
		if u.Override != (Choice{}) {
			if rt, _, err := u.Pick(role); err == nil {
				was = ReadRecord(RecordPath(rt.BuildDir(u.Path), name)).Choice()
			}
		}
		return Result{Running: true, Was: was}, nil
	}
	if l := u.legacy(key); l != "" && u.T.HasWindow(l, role) {
		return Result{Running: true, Legacy: l}, nil
	}
	if err := u.CheckMember(session, role, name); err != nil {
		return Result{}, err
	}
	rt, grants, err := u.Pick(role)
	if err != nil {
		return Result{}, err
	}
	dir := u.Dir
	if !u.Explicit {
		// A <role>.workdir file pins one member to its own folder.
		if d := workdir(filepath.Join(u.Path, "members", u.Team, role+".workdir")); d != "" {
			dir = d
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, err
	}
	prompt, err := u.writePrompt(rt.BuildDir(u.Path), key, role, memberFile)
	if err != nil {
		return Result{}, err
	}
	record := RecordPath(rt.BuildDir(u.Path), name)
	choice := u.ChoiceOf(role)
	was := ReadRecord(record).Choice()
	conv := Plan(rt, record, dir, u.Fresh)
	// Its state says starting before its window opens, so whoever sees the
	// window (cadrei ls, the --watch stream) sees it starting, and its own
	// hooks, which come after, are never written over.
	state := StatePath(rt.BuildDir(u.Path), name)
	starting := func(conv Conversation) {
		switch {
		case u.StateHooks && state != "":
			id := conv.SessionID
			if id == "" {
				id = conv.Resume
			}
			ResetState(state, &State{State: Starting, Since: time.Now(), Session: id})
		case state != "":
			ResetState(state, nil)
		}
	}
	starting(conv)
	started, err := u.run(rt, session, role, name, dir, prompt, grants, conv, choice)
	if err != nil && conv.Resume != "" {
		// The runtime would not resume it: start a new conversation.
		u.T.KillWindow(session, role)
		conv = Conversation{SessionID: rt.Sessions().NewID(), Note: "a new conversation: resuming failed"}
		starting(conv)
		started, err = u.run(rt, session, role, name, dir, prompt, grants, conv, choice)
	}
	if err != nil {
		if state != "" {
			ResetState(state, nil)
		}
		return Result{}, err
	}
	id := conv.SessionID
	if id == "" {
		id = conv.Resume
	}
	if id != "" && record != "" {
		// A resume keeps its record's id and time, with this start's model
		// and effort, so the next start can say what changed.
		rec := Record{ID: id, Dir: started, Since: time.Now()}
		if old := ReadRecord(record); conv.Resume != "" && old != nil {
			rec = *old
		}
		rec.Model, rec.ModelFrom = choice.Model.Value, choice.Model.From
		rec.Effort, rec.EffortFrom = choice.Effort.Value, choice.Effort.From
		WriteRecord(record, rec)
	}
	// A window only fails to start when it ends. One on a login or setup
	// screen (a runtime that isn't logged in, say) started, but won't get
	// going until the user answers it.
	ops := rt.Sessions()
	waiting := ops != nil && ops.Waiting(u.T.Screen(session, role))
	return Result{Started: true, Resumed: conv.Resume != "", Note: conv.Note, Dir: started, Waiting: waiting, Choice: choice, Was: was}, nil
}

// ChoiceOf is the model and effort role starts with: this start's, over
// its member file's, over the crew's.
func (u Up) ChoiceOf(role string) Choice {
	return u.Override.Over(MemberChoice(filepath.Join(u.Path, "members", u.Team, role+".md"))).Over(u.Defaults)
}

// run starts one member's window with the conversation conv, and checks
// that it is still there a moment later. It returns the folder it runs in.
func (u Up) run(rt runtime.Runtime, session, role, name, dir, prompt, grants string, conv Conversation, choice Choice) (string, error) {
	cmd, err := rt.Launch(runtime.LaunchSpec{Role: runtime.Member, Name: name, Cadrei: u.Path, WorkDir: dir,
		Mode: u.Mode, PromptFile: prompt, Grants: grants, SessionID: conv.SessionID, Resume: conv.Resume,
		Model: choice.Model.Value, Effort: choice.Effort.Value})
	if err != nil {
		return "", err
	}
	if cmd.Dir != "" {
		dir = cmd.Dir
	}
	argv := cmd.Argv
	// CADREI_HOME pins the member to this cadrei wherever it works.
	env := append([]string{"CADREI_HOME=" + u.Path, "CADREI_MEMBER=" + name}, cmd.Env...)
	label := u.Team
	if u.Project != "" {
		label += " on " + u.Project
	}
	look, windowLook := u.T.Look(u.Name, label, os.Getenv("NO_COLOR") != "")
	err = u.T.Start(StartSpec{Session: session, Window: role, Dir: dir, Env: env, Unset: cmd.Unset, Argv: argv,
		// The target too, recorded with the rest, so a cadrei up whose
		// output pipe closes early (cadrei up | head) still records it.
		SessionOptions: append([]Option{{"@cadrei_home", u.Path}, {"@cadrei_team", u.Team}, {"@cadrei_project", u.Project}, {"@cadrei_target", u.Target}, {"@cadrei_version", u.Version}}, look...),
		// The window stays when its command ends, until the start check
		// below, so a command that ends at once can say why (an unknown
		// model, T-M2).
		WindowOptions: append([]Option{{"@cadrei_member", name}, {"remain-on-exit", "on"}}, windowLook...)})
	if err != nil {
		return "", err
	}
	// From now on the user's own remain-on-exit applies.
	defer u.T.UnsetWindowOption(session, role, "remain-on-exit")
	state := StatePath(rt.BuildDir(u.Path), name)
	ready := func() bool {
		// Its start hook wrote a state: it got going, whatever its screen.
		if s := ReadState(state); s != nil && s.State != Starting {
			return true
		}
		ops := rt.Sessions()
		return ops != nil && ops.Ready(u.T.Screen(session, role))
	}
	if last, ok := u.T.StartCheck(session, role, u.Wait, ready); !ok {
		return "", &ExitedError{Name: name, Dir: dir, Line: Line(append(env, argv...)), Last: last}
	}
	return dir, nil
}

// writePrompt builds the member's prompt, rebuilt and swapped in whole at
// every start: the protocol, the cadrei's own protocol.md, then the member.
func (u Up) writePrompt(build, key, role, memberFile string) (string, error) {
	var b strings.Builder
	b.Write(u.Protocol)
	if own, err := os.ReadFile(filepath.Join(u.Path, "protocol.md")); err == nil {
		b.WriteString("\n")
		b.Write(own)
	}
	member, err := os.ReadFile(memberFile)
	if err != nil {
		return "", err
	}
	// The front matter is cadrei's, not the member's prompt.
	_, member = FrontMatter(member)
	b.WriteString("\n")
	b.Write(member)
	if err := EnsureBuild(build); err != nil {
		return "", err
	}
	path := filepath.Join(build, key+"-"+role+".md")
	return path, fsx.WriteFile(path, []byte(b.String()), 0o644)
}

// EnsureBuild makes the folder for generated prompts and settings copies,
// ignored by the cadrei's git through its own .gitignore.
func EnsureBuild(build string) error {
	// A cloned cadrei can carry a committed link here; cadrei writes its
	// prompts, settings copies and locks only into a folder of its own.
	for _, d := range []string{filepath.Dir(build), build} {
		if st, err := os.Lstat(d); err == nil && !st.IsDir() {
			return fmt.Errorf("%s is a link or a file, not a folder; cadrei writes its generated files only into a folder of its own, so remove it and run again", d)
		}
	}
	if err := os.MkdirAll(build, 0o755); err != nil {
		return err
	}
	ignore := filepath.Join(build, ".gitignore")
	if st, err := os.Lstat(ignore); err == nil && st.Mode().IsRegular() {
		return nil
	}
	// Written through a new file and a rename, which replaces a link
	// planted here instead of following it.
	return fsx.WriteFile(ignore, []byte("*\n"), 0o644)
}

// Line writes words for a POSIX shell, each single-quoted when it needs
// it, for a user to paste.
func Line(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = Quote(w)
	}
	return strings.Join(out, " ")
}

// Quote quotes a word for a POSIX shell when it needs it.
func Quote(w string) string {
	if w != "" && strings.Trim(w, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./=:@%+,") == "" {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}
