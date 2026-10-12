// Package runtime is the boundary between cadrei and the agent CLI its
// sessions run. Cadrei's own code (sessions, ls, allow, trust, health)
// talks to a Runtime; only an adapter, internal/runtime/claude, knows a
// CLI's flags, files, hooks and rule grammar. Claude Code is the only
// runtime, with no user-facing choice; the boundary keeps its specifics in
// one place, and lets tests run a fake.
package runtime

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// MessagingKind is how a runtime's sessions send each other messages.
type MessagingKind int

const (
	NoMessaging MessagingKind = iota // cannot be a member or the orchestrator
	Native                           // the CLI's own cross-session messaging
)

// Capabilities say what a runtime supports, so a runtime that cannot run
// cadrei's sessions safely is refused instead of failing in odd ways.
type Capabilities struct {
	FixedDenies      bool          // it enforces cadrei's fixed denies; without it, members are refused
	PermissionModes  []string      // the PERMISSION_MODE values it understands
	Resume           bool          // resuming a conversation
	AssignSessionID  bool          // starting with a session id cadrei chose
	Trust            bool          // marking project folders as trusted
	Instructions     bool          // can hold the orchestrator's instructions
	OrchestratorHook bool          // can start sessions as the orchestrator
	Messaging        MessagingKind // how its sessions reach each other
}

// Role is what a session is for.
type Role int

const (
	Member Role = iota
	Orchestrator
)

// LaunchSpec is what cadrei knows about a session it starts.
type LaunchSpec struct {
	Role       Role
	Name       string // the session's name, its messaging address
	Cadrei     string // the cadrei's physical folder
	WorkDir    string
	Mode       string // cadrei's PERMISSION_MODE
	PromptFile string // instructions to add to the session's own
	Grants     string // the runtime's grants artifact (from Permissions().Prepare), or ""
	// Settings is a settings file of cadrei's own for a session with no
	// grants (the orchestrator's, with its snapshot hook), or "".
	Settings string
	// Allowed are commands the session runs without asking, each exactly
	// as written (the orchestrator's read-only cadrei commands).
	Allowed []string
	// Prompt is the session's first message, sent as the user's would be
	// (the orchestrator's seed), or "".
	Prompt string
	// Model and Effort are the session's model and effort level (7.11), or
	// "" for the runtime's own default.
	Model, Effort string
	// SessionID starts a new conversation with this id (cadrei chose it, to
	// resume it later); Resume continues the conversation with this id.
	// At most one is set, and only for a runtime with those capabilities.
	SessionID string
	Resume    string
	// ConfigHere has the session read the runtime's own config (its login)
	// as this process's environment says, not as a tmux server's does:
	// cadrei just logged the user in from here.
	ConfigHere bool
}

// Command is what cadrei runs: in tmux for a member, or as a child process.
type Command struct {
	Argv []string
	Env  []string // KEY=value, added to cadrei's environment
	// Unset names variables of cadrei's environment the command starts
	// without: the marks of the session cadrei itself may run in.
	Unset []string
	Dir   string
}

// Install is a runtime found on this machine.
type Install struct {
	Path    string
	Version string
}

// Folder is a project folder: its name and its path as cadrei spells it.
type Folder struct {
	Name string
	Dir  string
}

// TrustResult is what happened to one folder's trust.
type TrustResult struct {
	Name   string
	State  string // "trusted", "already", "untrusted", "refused" or "skipped"
	Reason string
}

// Places are the physical folders a runtime's fixed denies must name:
// ~/.cadrei as resolved, and every known cadrei. Binary is the cadrei program
// a member's session hooks run (to keep its conversation id current).
type Places struct {
	Root    string
	Archive string // the archive folder's physical path (~/.cadrei-archive)
	Cadreis []string
	Binary  string
}

// Prepared is what Permissions().Prepare made for a start.
type Prepared struct {
	Grants   string   // the artifact for LaunchSpec.Grants, or "" when the member starts without
	Notes    []string // printed on stdout (a file was created)
	Warnings []string // printed on stderr
}

// Grant is one entry of a cadrei's grants, as cadrei allow list shows it.
type Grant struct {
	Kind  string // "rule", or "auto" for a plain-English allowance
	Entry string
	Once  bool      // a one-time grant, to be removed when its task is done
	Added time.Time // when a one-time grant was added
	Wide  bool      // the entry has a wildcard
}

// ErrGranted is returned when a grant is already there.
var ErrGranted = errors.New("already granted")

// ErrNoOnce is returned when there are no one-time grants to remove.
var ErrNoOnce = errors.New("there are no one-time grants")

// GrantStore is a cadrei's grants, opened for reading and changing.
type GrantStore interface {
	List() []Grant
	Stale() []string // one-time records whose grant is gone
	HasOnce() bool
	Add(kind, entry string, once bool) error
	// Remove takes out a grant by entry or number, or every one-time grant
	// for "--once". It returns what it removed and the stale records it
	// dropped.
	Remove(target string) (removed, stale []string, err error)
	Files() []string // the files to commit, inside the cadrei
}

// PermissionOps turn cadrei's grants into what the runtime enforces.
type PermissionOps interface {
	// Open reads the cadrei's grants, creating the file when create is set;
	// an error says why the file cannot be used.
	Open(cadrei string, create bool) (GrantStore, error)
	// Unchanged reports whether the grants file is as cadrei last wrote it,
	// recording it when it is what cadrei last committed.
	Unchanged(cadrei string) bool
	// Record remembers the grants file as cadrei wrote it.
	Record(cadrei string)
	// BuiltIn says what every member gets that cadrei allow list does not show.
	BuiltIn() string
	// Validate checks a grant before it is stored: a rule, or a
	// plain-English entry when auto is set. It returns a warning, or an
	// error saying why it is refused.
	Validate(cadrei string, known []string, rule string, auto bool) (warning string, err error)
	// Prepare makes sure the cadrei's grants file exists and writes what a
	// member starts with, with the fixed denies added.
	Prepare(cadrei string, places Places) Prepared
	// GrantsFile is where the cadrei's grants are stored.
	GrantsFile(cadrei string) string
}

// TrustOps mark project folders as trusted in the runtime.
type TrustOps interface {
	Mark(folders []Folder) (results []TrustResult, note string)
	Unmark(folders []Folder) (results []TrustResult, note string)
	// Protected lists folders the runtime loads code or settings from;
	// projects must never live inside them.
	Protected() []string
	// Loaded lists what in dir the runtime loads into a session started
	// there once dir is trusted (settings, hooks, commands, instructions),
	// as paths relative to dir, in a fixed order.
	Loaded(dir string) []string
	// Risky lists what can run commands or add tools for every member
	// once dir is trusted (settings with hooks, MCP servers, and skills,
	// agents or commands at any depth), as paths relative to dir: a
	// folder that has any is never trusted by a bare yes.
	Risky(dir string) []string
}

// Problem is one finding of the health check (M.4): what is wrong and the
// exact fix. A fatal problem stops plain cadrei before the runtime starts.
type Problem struct {
	What  string
	Fix   string
	Fatal bool
}

// InstructionOps put the orchestrator's instructions (cadrei's skill) where
// the runtime reads them: a link to the folder cadrei writes them to.
type InstructionOps interface {
	// Path is where the runtime looks for them.
	Path() string
	// Target is where Path links to. It returns an error wrapping
	// fs.ErrNotExist when there is nothing at Path, and ErrNotLink when
	// something other than a link is there.
	Target() (string, error)
	// Link makes Path a link to dir. It replaces a link, never anything else.
	Link(dir string) error
	// Unlink removes Path when it is a link to dir, and nothing else. It
	// reports whether it removed it.
	Unlink(dir string) (bool, error)
	// OldClone reports whether dir is a clone of cadre 0.1.x, which
	// installed itself as a git checkout holding its command, its skill and
	// its hook script.
	OldClone(dir string) bool
}

// ErrNotLink is returned for a path cadrei would link that holds something
// else.
var ErrNotLink = errors.New("not a link")

// HookOps manage the hook that makes every new session the orchestrator.
type HookOps interface {
	// File is the settings file that holds the hook.
	File() string
	// Find returns the program each orchestrator hook in File runs: a cadrei
	// binary, or a 0.1.x hook script.
	Find() ([]string, error)
	// Set makes `<binary> hook orchestrator` the only orchestrator hook,
	// replacing others. It returns false when it already was.
	Set(binary string) (bool, error)
	// Remove takes out the orchestrator hooks that run binary, and leaves
	// every other hook. It reports whether it removed any.
	Remove(binary string) (bool, error)
	// Outdated reports whether the hook for binary is written in an older
	// form, which Set rewrites.
	Outdated(binary string) (bool, error)
	// Output is what the hook prints to give a new session text.
	Output(text string) []byte
	// SnapshotSettings writes, in dir, the orchestrator's settings file:
	// a session start hook that runs `<binary> hook snapshot`, so the
	// orchestrator gets a fresh snapshot on startup and on resume. It
	// returns the file, for LaunchSpec.Settings.
	SnapshotSettings(dir, binary string) (string, error)
}

// StateEvent is one of a member's hook events, as cadrei's member states
// read it.
type StateEvent struct {
	Name       string // one of the Ev names, or "" for an event that changes nothing
	Question   bool   // a permission request that is a question to the user
	Session    string // the conversation id
	Transcript string // the conversation's transcript
}

// State event names.
const (
	EvPrompt       = "prompt"        // a prompt arrived: working
	EvToolUse      = "tool"          // a tool runs: working
	EvPermission   = "permission"    // it asks the user: waiting
	EvStop         = "stop"          // its turn ended: idle
	EvStopFailure  = "stop-failure"  // its turn ended on an error: idle
	EvStart        = "start"         // its session started: idle
	EvIdleReminder = "idle-reminder" // the runtime reminds the user it is idle
	EvEnd          = "end"           // its session ended
)

// SessionOps are a runtime's conversations, for resuming them.
type SessionOps interface {
	// NewID is a new conversation id, for LaunchSpec.SessionID.
	NewID() string
	// Exists reports whether the conversation with this id can still be
	// resumed in dir, the folder it ran in (its transcript is where the
	// runtime looks for that folder).
	Exists(id, dir string) bool
	// Transcript describes the conversation's transcript for dir: when it
	// was last written and its size, or ok false when there is none. A
	// resumed run that changed neither did not get going.
	Transcript(id, dir string) (mod time.Time, size int64, ok bool)
	// FromHook reads the conversation id from what a session start hook
	// receives, or "".
	FromHook(input io.Reader) string
	// StateEvent reads what a member's hook receives.
	StateEvent(input io.Reader) StateEvent
	// TranscriptFile is the conversation's transcript, or "".
	TranscriptFile(id, dir string) string
	// UnansweredPrompt reports whether the last prompt in a transcript is
	// text, with no reply after it.
	UnansweredPrompt(transcript, text string) bool
	// SafeguardStop reports whether the runtime's safeguards stopped the
	// last reply in a transcript, with no normal reply since, and when.
	// It reads only the end of the file; anything it can't read is no stop.
	SafeguardStop(transcript string) (at time.Time, stopped bool)
	// Ready reports whether a session's screen shows its prompt, waiting
	// for input: it got going.
	Ready(screen string) bool
	// Waiting reports whether a session's screen is one the runtime shows
	// before its prompt and waits on the user (a login, trust or first-run
	// screen): it got going. Only screens it knows count, so a command that
	// printed a line and is about to end isn't taken for one.
	Waiting(screen string) bool
	// ShellCommand reads what a member's guard hook receives before a
	// tool runs: the command line when the tool runs one in a shell, with
	// shell set. An error says the input couldn't be read, which the
	// guard counts as a command it can't let run.
	ShellCommand(input io.Reader) (command string, shell bool, err error)
	// Sent reads what the orchestrator's notify hook receives after a
	// message it sent: the message's target, whether it asked to hear when
	// that session goes idle, and whether a subagent sent it. ok is false
	// for input that isn't one sent message.
	Sent(input io.Reader) (s Sent, ok bool)
	// SentContext is the notify hook's answer: text added to the
	// orchestrator's context after the send.
	SentContext(text string) []byte
}

// Sent is a message the orchestrator sent, as its notify hook reads it.
type Sent struct {
	To       string // the target as written, a name or an address
	Notify   bool   // it asked to hear when the target goes idle
	Subagent bool   // a subagent sent it, not the main conversation
}

// Probe is what the first run checks about the runtime.
type Probe struct {
	Installed bool   // its command is on PATH
	Runs      bool   // it answers --version
	Version   string // as it says it
	Login     string // "yes", "no", or "unknown" (too old to ask, or no answer in time)
}

// Says is what the checks say about the runtime, called title: that it
// isn't installed, won't start, or how its login stands. The first run's
// screen and the full health check both say it this way.
func (p Probe) Says(title string) string {
	switch {
	case !p.Installed:
		return title + " isn't installed."
	case !p.Runs:
		return title + " is installed but won't start."
	case p.Login == LoggedIn:
		return title + " " + p.Version + ", logged in"
	case p.Login == LoggedOut:
		return title + " " + p.Version + ", not logged in yet"
	}
	return title + " " + p.Version + ". Couldn't check your login. If it asks, log in."
}

// InstallHelp is how to install the runtime: its Homebrew commands and its
// web page. Login is the command that logs the user in.
type InstallHelp struct{ Install, Reinstall, URL, Login string }

// Login states.
const (
	LoggedIn    = "yes"
	LoggedOut   = "no"
	LoginUnsure = "unknown"
)

// Runtime is an agent CLI cadrei can run sessions with.
type Runtime interface {
	Name() string
	Title() string // the product's name, for messages: "Claude Code"
	Model() string // the model's name, whose safeguards can stop a reply: "Claude"
	// ModelHint says what a model name looks like, for an error: "an alias
	// such as ..., or a full name such as ...".
	ModelHint() string
	// SessionVars are the variables the runtime sets about its own session
	// for the programs it runs. A session cadrei starts from inside one
	// (the orchestrator's cadrei up) must not take them as its own, and no
	// tmux server may keep them.
	SessionVars() []string
	// SetupVars are the variables that point the runtime at a setup of
	// its own, whatever HOME is (its config folder): a development build
	// refuses one outside a temporary folder.
	SetupVars() []string
	Caps() Capabilities
	Detect() (Install, error)
	// Health is the runtime's part of the health check: it is installed,
	// and, when full, it runs and is logged in; and nothing in the cadreis'
	// folders changes what their sessions load.
	Health(full bool, cadreis []string) []Problem
	// Probe runs the runtime to say whether it works, its version and
	// whether the user is logged in.
	Probe() Probe
	// Help is how to install or reinstall it.
	Help() InstallHelp
	// Login builds the command that logs the user in, run in their own
	// terminal.
	Login() (Command, error)
	// Launch builds the command for a session; the caller runs it.
	Launch(LaunchSpec) (Command, error)
	// BuildDir is where cadrei writes a cadrei's generated prompts and
	// grants artifacts for this runtime.
	BuildDir(cadrei string) string
	Permissions() PermissionOps
	Trust() TrustOps
	Instructions() InstructionOps
	Hooks() HookOps
	Sessions() SessionOps
}

var (
	registry    = map[string]Runtime{}
	defaultName string
)

// Register adds a runtime. Adapters call it from init.
func Register(r Runtime) { registry[r.Name()] = r }

// RegisterDefault adds the runtime used when nothing names one.
func RegisterDefault(r Runtime) {
	Register(r)
	defaultName = r.Name()
}

// Default is the name of the runtime used when nothing names one.
func Default() string { return defaultName }

// Supported lists the runtimes this build accepts, sorted.
func Supported() []string {
	var out []string
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Get returns a runtime by name, refusing one this build does not have.
func Get(name string) (Runtime, error) {
	if r, ok := registry[name]; ok {
		return r, nil
	}
	return nil, fmt.Errorf("runtime %s is not supported yet (supported: %s)", name, strings.Join(Supported(), ", "))
}

// CanOrchestrate refuses a runtime that cannot be the orchestrator:
// it needs a place for the orchestrator's instructions, a way to start a
// session as the orchestrator, and messaging to reach the members.
func CanOrchestrate(r Runtime) error {
	caps := r.Caps()
	if !caps.Instructions || !caps.OrchestratorHook || caps.Messaging == NoMessaging {
		return fmt.Errorf("runtime %s cannot be the orchestrator (it needs instructions, an orchestrator start and messaging)", r.Name())
	}
	return nil
}

// Usable refuses a runtime that cannot run a member safely or reach the
// orchestrator: one without the fixed denies, or without messaging.
func Usable(r Runtime, mode string) error {
	caps := r.Caps()
	if !caps.FixedDenies {
		return fmt.Errorf("runtime %s cannot enforce cadrei's fixed denies, so its members are refused", r.Name())
	}
	if caps.Messaging == NoMessaging {
		return fmt.Errorf("runtime %s has no way to message the orchestrator, so it cannot run members", r.Name())
	}
	for _, m := range caps.PermissionModes {
		if m == mode {
			return nil
		}
	}
	return fmt.Errorf("runtime %s has no permission mode %s (it has: %s)", r.Name(), mode, strings.Join(caps.PermissionModes, ", "))
}
