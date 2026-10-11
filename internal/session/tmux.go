// Package session runs member sessions: Claude Code sessions in tmux,
// one tmux session per team (and project), one window per role. It names
// them, records which cadrei each belongs to, and starts, lists and stops
// them.
package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Tmux runs tmux on one server: tmux's default one, or a private one when
// CADREI_TMUX_SOCKET names a socket (tests use that). Every command names
// that server's socket with -S: without it, tmux follows $TMUX, so a
// cadrei run inside tmux on another server would start a second
// orchestrator there. Commands are always run with their arguments as they
// are, never through a shell, and every target is exact (=name), since
// tmux otherwise matches prefixes and cadre 0.1.x's down dev once stopped
// cadre-dev-app.
type Tmux struct {
	Socket string // a socket name, as tmux -L takes it
	At     string // a socket's full path, which wins over Socket and TMUX_TMPDIR
}

// At is the tmux server whose socket is path, whatever the environment
// names.
func At(path string) Tmux { return Tmux{At: path} }

// Default is the tmux cadrei uses.
func Default() Tmux { return Tmux{Socket: os.Getenv("CADREI_TMUX_SOCKET")} }

// Path is the server's socket, where tmux -L would put it:
// ${TMUX_TMPDIR:-/tmp}/tmux-<uid>/<name>, its folder resolved as tmux
// resolves it.
func (t Tmux) Path() string {
	if t.At != "" {
		return t.At
	}
	name := t.Socket
	if name == "" {
		name = "default"
	}
	base := os.Getenv("TMUX_TMPDIR")
	if base == "" {
		base = "/tmp"
	}
	dir := filepath.Join(base, fmt.Sprintf("tmux-%d", os.Getuid()))
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return filepath.Join(dir, name)
}

// Where this process runs, as InTmux says.
const (
	NotInTmux   = iota // not inside tmux
	SameServer         // inside tmux, on cadrei's server
	OtherServer        // inside tmux, on another server
)

// InTmux says whether this process runs inside tmux, and on which server:
// the part of $TMUX before the first comma is that server's socket.
func (t Tmux) InTmux() int {
	v := os.Getenv("TMUX")
	if v == "" {
		return NotInTmux
	}
	socket, _, _ := strings.Cut(v, ",")
	if samePath(socket, t.Path()) {
		return SameServer
	}
	return OtherServer
}

// samePath compares two socket paths with their folders resolved.
func samePath(a, b string) bool {
	resolve := func(p string) string {
		if d, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
			return filepath.Join(d, filepath.Base(p))
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

// command runs a tmux client for cadrei to read. -u marks the client as
// UTF-8 whatever the locale, so tmux does not turn non-ASCII characters in
// names and paths into "_" (a C locale, as on CI machines). The client
// runs without the variables kept out of tmux (KeepOut).
func (t Tmux) command(args ...string) *exec.Cmd {
	c := exec.Command("tmux", append([]string{"-u", "-S", t.socket()}, args...)...)
	c.Env = withoutKept(os.Environ())
	return c
}

// keptOut are the variables no tmux client of cadrei passes on. A tmux
// server copies the environment of the client that starts it into its
// global one, which every window, and any process in it, can read: a
// cadrei up run by the orchestrator would leave the orchestrator's marks
// (and its runtime's session, KeepOut) there for every member. Each
// window gets what it needs with its own command.
var keptOut = []string{
	"CADREI_HOME", "CADREI_MEMBER", "CADRE_PERSONA", "CADREI_ORCHESTRATOR",
	"CADREI_OPENS_IN", "CADREI_OPENED_FROM", "CADREI_OPENED_PROJECT",
}

// KeepOut adds a runtime's session variables to what tmux clients don't
// pass on.
func KeepOut(names ...string) {
	for _, n := range names {
		if !contains(keptOut, n) {
			keptOut = append(keptOut, n)
		}
	}
}

// withoutKept is env without the variables kept out of tmux.
func withoutKept(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); !contains(keptOut, name) {
			out = append(out, kv)
		}
	}
	return out
}

// socket is Path, with its folder made as tmux makes it for -L (only the
// user may enter it): tmux does not make it for -S.
func (t Tmux) socket() string {
	p := t.Path()
	os.MkdirAll(filepath.Dir(p), 0o700)
	return p
}

// run runs tmux and returns its output, trimmed.
func (t Tmux) run(args ...string) (string, error) {
	out, err := t.command(args...).CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

// target names a session exactly. The colon ends the session part, so a
// dot in the name (a project such as my.app) is not read as a pane.
func target(session string) string { return "=" + session + ":" }

// attachArgs attaches to session, or switches the client to it when this
// process already runs inside tmux on the same server.
func attachArgs(session string, inside bool) []string {
	if inside {
		return []string{"switch-client", "-t", target(session)}
	}
	return []string{"attach", "-t", target(session)}
}

// Has reports whether a session called name runs.
func (t Tmux) Has(name string) bool {
	_, err := t.run("has-session", "-t", target(name))
	return err == nil
}

// Gone reports whether no session called name runs, as tmux tells it:
// tmux can't find it, or there's no server. Any other error (a socket it
// can't open, a server of another version) isn't taken for gone.
func (t Tmux) Gone(name string) bool {
	out, err := t.run("has-session", "-t", target(name))
	return err != nil && goneText(out)
}

// goneText reports whether tmux's error says the session or its server
// isn't there.
func goneText(out string) bool {
	for _, s := range []string{"can't find session", "no server running", "(No such file or directory)", "(Connection refused)"} {
		if strings.Contains(out, s) {
			return true
		}
	}
	return false
}

// Option reads a session's user option ("" when unset or no session).
func (t Tmux) Option(session, name string) string {
	out, err := t.run("show-options", "-qv", "-t", target(session), name)
	if err != nil {
		return ""
	}
	return unescape(out)
}

// recordable refuses an option value that tmux could not give back as it
// is: Sessions and Members read one line per session with fields split by
// sep, and tmux escapes control characters and bytes that are not UTF-8 in
// its output.
func recordable(name, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("cannot record %s in tmux: %q is not valid UTF-8", name, value)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("cannot record %s in tmux: %q holds a tab, a line break or another control character", name, value)
		}
	}
	for i := 0; i+2 < len(value); i++ {
		if value[i] == '\\' && value[i+1] == '$' && dollarEscaped(value[i+2]) {
			return fmt.Errorf("cannot record %s in tmux: %q holds a backslash before $, which tmux would not give back as it is", name, value)
		}
	}
	return nil
}

// startDir passes a start folder to tmux's -c, which tmux expands as a
// format: "##" is a literal "#", so a folder holding "#{" stays itself.
func startDir(dir string) string { return strings.ReplaceAll(dir, "#", "##") }

// dollarEscaped reports whether tmux 3.4 may write "$" followed by c as
// "\$" in command output (utf8_strvis); later versions may not. tmux tests
// c with the locale's isalpha, which in a UTF-8 locale on macOS is true for
// the lead bytes of multibyte characters, so every byte from 0x80 counts.
func dollarEscaped(c byte) bool {
	return c == '_' || c == '{' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

// unescape undoes that: it drops a backslash right before such a "$".
// recordable refuses values that hold one, so this is exact for a tmux that
// escapes and for one that does not.
func unescape(s string) string {
	if !strings.Contains(s, `\$`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+2 < len(s) && s[i+1] == '$' && dollarEscaped(s[i+2]) {
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// SetOption sets a session's user option.
func (t Tmux) SetOption(session, name, value string) error {
	if err := recordable(name, value); err != nil {
		return err
	}
	_, err := t.run("set-option", "-q", "-t", target(session), name, value)
	return err
}

// SetWindowOption sets a window's user option.
func (t Tmux) SetWindowOption(session, window, name, value string) error {
	if err := recordable(name, value); err != nil {
		return err
	}
	_, err := t.run("set-option", "-w", "-q", "-t", target(session)+"="+window, name, value)
	return err
}

// Windows lists a session's window names, without a new session's
// placeholder.
func (t Tmux) Windows(session string) []string {
	var list []string
	for _, w := range t.allWindows(session) {
		if w != bootWindow {
			list = append(list, w)
		}
	}
	return list
}

func (t Tmux) allWindows(session string) []string {
	out, err := t.run("list-windows", "-t", target(session), "-F", "#W")
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// HasWindow reports whether session has a window called name.
func (t Tmux) HasWindow(session, name string) bool {
	for _, w := range t.Windows(session) {
		if w == name {
			return true
		}
	}
	return false
}

// PaneDead reports whether a window's command has ended (a dead pane is
// kept when the user's tmux sets remain-on-exit).
func (t Tmux) PaneDead(session, window string) bool {
	out, err := t.run("list-panes", "-t", target(session)+"="+window, "-F", "#{pane_dead}")
	return err == nil && strings.HasPrefix(out, "1")
}

// Screen is what a window's pane shows now.
func (t Tmux) Screen(session, window string) string {
	out, _ := t.run("capture-pane", "-p", "-t", target(session)+"="+window)
	return out
}

// LastLine is the last line a window's pane shows that isn't blank, or "".
func (t Tmux) LastLine(session, window string) string {
	// With the lines above the screen: what a command wrote just before it
	// ended can be there.
	out, err := t.run("capture-pane", "-p", "-S", "-50", "-t", target(session)+"="+window)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(out, " \n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		// tmux's own note on a dead pane isn't the command's.
		if l := strings.TrimSpace(lines[i]); l != "" && !strings.HasPrefix(l, "Pane is dead") {
			return l
		}
	}
	return ""
}

// UnsetWindowOption removes a window option set on one window, so the
// global value applies again.
func (t Tmux) UnsetWindowOption(session, window, name string) {
	t.run("set-option", "-wu", "-t", target(session)+"="+window, name)
}

// PanePID is the pid of the command running in a window.
func (t Tmux) PanePID(session, window string) (int, error) {
	out, err := t.run("display", "-p", "-t", target(session)+"="+window, "#{pane_pid}")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// Info is what cadrei records on a session.
type Info struct {
	Name    string // the tmux session
	Home    string // @cadrei_home: its cadrei's physical path; "" for a legacy session, or one without cadrei's markers
	Team    string // @cadrei_team
	Project string // @cadrei_project
	Role    string // @cadrei_role: "orchestrator", or "" for a team's session
	Target  string // @cadrei_target: the project or folder it was started for
}

// Legacy reports whether a session was started by cadre 0.1.x: a cadre-
// name, and no home.
func (i Info) Legacy() bool { return i.Home == "" && strings.HasPrefix(i.Name, legacyPrefix) }

// sep splits the fields of tmux's -F output. tmux writes command output
// through vis(3) with VIS_OCTAL|VIS_CSTYLE (3.4 does; later versions may
// not), which turns control characters into escapes such as \037, except
// tab and newline. So the separator is a tab, and recordable refuses tabs,
// line breaks and every other control character in recorded values.
const sep = "\t"

// Sessions lists the cadrei sessions on the server, with their options,
// and none when tmux can't list them.
func (t Tmux) Sessions() []Info {
	list, _ := t.List()
	return list
}

// List is Sessions for a caller that must know every session, such as a
// teardown that moves a crew only when none of its teams runs. Its error
// is tmux's own when tmux couldn't list the sessions (a socket that can't
// be opened, a server of another version), and nil when there are none to
// list: no server, or no tmux.
func (t Tmux) List() ([]Info, error) {
	format := strings.Join([]string{"#S", "#{@cadrei_home}", "#{@cadrei_team}", "#{@cadrei_project}", "#{@cadrei_role}", "#{@cadrei_target}"}, sep)
	out, err := t.run("list-sessions", "-F", format)
	switch {
	case errors.Is(err, exec.ErrNotFound), err != nil && goneText(out):
		return nil, nil
	case err != nil:
		line, _, _ := strings.Cut(out, "\n")
		if line == "" {
			line = err.Error()
		}
		return nil, errors.New(line)
	case out == "":
		return nil, nil
	}
	var list []Info
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, sep)
		if len(f) != 6 || !Started(f[0]) {
			continue
		}
		list = append(list, Info{Name: f[0], Home: unescape(f[1]), Team: unescape(f[2]), Project: unescape(f[3]), Role: unescape(f[4]), Target: unescape(f[5])})
	}
	return list, nil
}

// Window is one window of a cadrei session, as one listing of the whole
// server has it.
type Window struct {
	Session Info
	Name    string // the window
	Member  string // @cadrei_member, the member name recorded on it
	PanePID int
}

// AllWindows lists every window of every cadrei session on the server,
// with its session's markers, in one tmux call, in tmux's order (by
// session, then window). With no server, nothing runs: none.
func (t Tmux) AllWindows() []Window {
	format := strings.Join([]string{"#S", "#{@cadrei_home}", "#{@cadrei_team}", "#{@cadrei_project}", "#{@cadrei_role}", "#{@cadrei_target}",
		"#W", "#{@cadrei_member}", "#{pane_pid}"}, sep)
	out, err := t.run("list-windows", "-a", "-F", format)
	if err != nil || out == "" {
		return nil
	}
	var list []Window
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, sep)
		if len(f) != 9 || !Started(f[0]) || f[6] == bootWindow {
			continue
		}
		pid, _ := strconv.Atoi(f[8])
		list = append(list, Window{
			Session: Info{Name: f[0], Home: unescape(f[1]), Team: unescape(f[2]), Project: unescape(f[3]), Role: unescape(f[4]), Target: unescape(f[5])},
			Name:    f[6], Member: unescape(f[7]), PanePID: pid,
		})
	}
	return list
}

// WindowMembers maps a session's windows to the member names recorded on
// them (@cadrei_member), which keep a project's dots where the session
// name has "_".
func (t Tmux) WindowMembers(session string) map[string]string {
	out, err := t.run("list-windows", "-t", target(session), "-F", "#W"+sep+"#{@cadrei_member}")
	m := map[string]string{}
	if err != nil {
		return m
	}
	for _, line := range strings.Split(out, "\n") {
		if w, name, ok := strings.Cut(line, sep); ok && name != "" {
			m[w] = unescape(name)
		}
	}
	return m
}

// Option is a tmux user option to set.
type Option struct{ Name, Value string }

// StartSpec is a window to start.
type StartSpec struct {
	Session, Window, Dir string
	Env                  []string // KEY=value
	Unset                []string // variable names the command starts without
	Argv                 []string // at least the program and one argument
	SessionOptions       []Option // set on a new session, in the same tmux command
	SessionHooks         []Option // set-hook on a new session: hook name and its tmux command
	WindowOptions        []Option
}

// word passes one argument to tmux as it is. tmux splits its command line
// at an argument that is or ends with ";", and reads a trailing "\;" as a
// literal ";", so a trailing ";" is escaped; an argument that already ends
// in "\;" is refused, since tmux would change it.
func word(w string) (string, error) {
	switch {
	case strings.HasSuffix(w, `\;`):
		return "", fmt.Errorf("cannot pass %q to tmux: it ends in a backslash and a semicolon", w)
	case strings.HasSuffix(w, ";"):
		return w[:len(w)-1] + `\;`, nil
	}
	return w, nil
}

// bootWindow is the placeholder a new session starts with, so its options
// (history-limit above all, which tmux 3.2 applies only to windows made
// after it is set) are in place before the real window exists. No member
// can have this name: a role is a file name, and cadrei- is cadrei's own.
const bootWindow = "cadrei-starting"

// Start runs argv in a new window of the session (a new session when it
// does not run yet), in dir, with env added, and sets the options in the
// same tmux command, so no other cadrei command sees the session without
// them. A new session starts with a placeholder window, which is removed
// once the real one runs. The command runs as it is: with several
// arguments, tmux execs it without a shell, so no value needs quoting.
func (t Tmux) Start(s StartSpec) error {
	if len(s.Argv) < 2 {
		// With one argument, tmux hands it to a shell.
		return fmt.Errorf("tmux: a command needs at least two arguments to run without a shell")
	}
	for _, o := range append(append([]Option{}, s.SessionOptions...), s.WindowOptions...) {
		if err := recordable(o.Name, o.Value); err != nil {
			return err
		}
	}
	var args []string
	var bad error
	// add escapes each word as it goes in; only the separators added below
	// are bare ";".
	add := func(words ...string) {
		for _, w := range words {
			e, err := word(w)
			if err != nil && bad == nil {
				bad = err
			}
			args = append(args, e)
		}
	}
	set := func(flag, target string, o Option) {
		args = append(args, ";")
		add("set-option", flag, "-t", target, o.Name, o.Value)
	}
	isNew := !t.Has(s.Session)
	if isNew {
		add("new-session", "-d", "-s", s.Session, "-n", bootWindow, "-c", startDir(s.Dir), "--", "sleep", "30")
		for _, o := range s.SessionOptions {
			set("-q", target(s.Session), o)
		}
		for _, h := range s.SessionHooks {
			args = append(args, ";")
			add("set-hook", "-t", target(s.Session), h.Name, h.Value)
		}
		args = append(args, ";")
	}
	// A server started before cadrei kept them out (an older cadrei, or
	// tmux run by hand) may hold them still.
	for _, name := range keptOut {
		add("set-environment", "-gu", name)
		args = append(args, ";")
	}
	add("new-window", "-d", "-t", target(s.Session), "-n", s.Window, "-c", startDir(s.Dir))
	for _, e := range s.Env {
		add("-e", e)
	}
	add("--")
	// tmux can set a variable but not remove one from the server's, so
	// env removes them as the command starts.
	if len(s.Unset) > 0 {
		add("env")
		for _, u := range s.Unset {
			add("-u", u)
		}
	}
	add(s.Argv...)
	for _, o := range s.WindowOptions {
		set("-wq", target(s.Session)+"="+s.Window, o)
	}
	if isNew {
		// Renumbered, so the real window takes the placeholder's index and
		// the next one starts after it.
		args = append(args, ";")
		add("kill-window", "-t", target(s.Session)+"="+bootWindow)
		args = append(args, ";")
		add("move-window", "-r", "-t", target(s.Session))
	}
	if bad != nil {
		return bad
	}
	out, err := t.run(args...)
	if isNew && contains(t.allWindows(s.Session), bootWindow) {
		// A command that failed part way left the placeholder.
		t.KillWindow(s.Session, bootWindow)
		t.run("move-window", "-r", "-t", target(s.Session))
	}
	if err != nil && t.Has(s.Session) {
		return fmt.Errorf("tmux: %s", out)
	}
	// A command that ended at once took its window with it before the
	// options were set; the caller's start check reports that.
	return nil
}

// KillSession stops a session.
func (t Tmux) KillSession(name string) error {
	_, err := t.run("kill-session", "-t", target(name))
	return err
}

// KillWindow stops one window of a session.
func (t Tmux) KillWindow(session, window string) error {
	_, err := t.run("kill-window", "-t", target(session)+"="+window)
	return err
}

// VersionText is tmux's version as it says it: 3.8, 3.5a, next-3.6.
func (t Tmux) VersionText() (string, error) {
	out, err := exec.Command("tmux", "-V").Output()
	if err != nil {
		return "", errors.New("tmux is not installed")
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "tmux")), nil
}

// Version returns tmux's major and minor version, or an error when tmux
// is missing.
func (t Tmux) Version() (int, int, error) {
	v, err := t.VersionText()
	if err != nil {
		return 0, 0, err
	}
	out := []byte(v)
	v = strings.TrimPrefix(v, "next-")
	majorText, rest, _ := strings.Cut(v, ".")
	major, err := strconv.Atoi(majorText)
	if err != nil {
		return 0, 0, fmt.Errorf("cannot read the tmux version %q", out)
	}
	minor := 0
	for i := 0; i < len(rest) && rest[i] >= '0' && rest[i] <= '9'; i++ {
		minor = minor*10 + int(rest[i]-'0')
	}
	return major, minor, nil
}

// Own returns the cadrei session this process runs in, if any: $TMUX names
// a session on the same tmux server.
func (t Tmux) Own() string {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" || t.InTmux() != SameServer {
		return ""
	}
	name, err := t.run("display", "-p", "-t", pane, "#S")
	if err != nil || !Started(name) {
		return ""
	}
	return name
}

// KeepOnly stops every window of session but the one holding pane. It
// reports whether it did: false when a window wouldn't stop, or tmux
// couldn't list them, so a member may still run.
func (t Tmux) KeepOnly(session, pane string) bool {
	own, err := t.run("display", "-p", "-t", pane, "#{window_id}")
	if err != nil || own == "" {
		return false
	}
	ids, err := t.run("list-windows", "-t", target(session), "-F", "#{window_id}")
	if err != nil {
		return false
	}
	ok := true
	for _, id := range strings.Fields(ids) {
		if id == own {
			continue
		}
		// A window that ended on its own meanwhile is gone too.
		if out, err := t.run("kill-window", "-t", id); err != nil && !strings.Contains(out, "can't find window") {
			ok = false
		}
	}
	return ok
}

// ErrOtherServer refuses to show a session from inside tmux on another
// server: switch-client there would move another terminal's client.
var ErrOtherServer = errors.New("this terminal is in tmux on another server")

// Attach shows session in this terminal. Inside tmux on cadrei's server it
// switches the client to it and returns at once, reporting switched.
// Outside tmux it runs a tmux client as a child, with this terminal, and
// returns once the user leaves or the session ends (tmux exits 0 for
// both, so the caller checks Has). Meanwhile Ctrl-C and the like are the
// client's: cadrei catches them and only waits. Caught, not ignored: an
// ignored signal would stay ignored in tmux across exec.
func (t Tmux) Attach(session string) (switched bool, err error) {
	path, err := exec.LookPath("tmux")
	if err != nil {
		return false, errors.New("tmux is not installed")
	}
	switch t.InTmux() {
	case OtherServer:
		return false, ErrOtherServer
	case SameServer:
		if out, err := t.run(attachArgs(session, true)...); err != nil {
			return false, fmt.Errorf("tmux: %s", out)
		}
		return true, nil
	}
	cmd := exec.Command(path, append([]string{"-S", t.socket()}, attachArgs(session, false)...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	quiet := make(chan os.Signal, 1)
	signal.Notify(quiet, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTSTP)
	go func() {
		for range quiet {
		}
	}()
	defer func() {
		signal.Stop(quiet)
		close(quiet)
	}()
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return false, fmt.Errorf("tmux attach ended with exit code %d", exit.ExitCode())
		}
		return false, err
	}
	return false, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// StartCheck watches a window whose command just started, with
// remain-on-exit on: a command that cannot run ends soon, and its pane is
// dead. It looks every 100 ms, for up to limit (3 seconds when 0), and
// stops early once ready says so, after half a second. A runtime that
// can't say (ready nil) is watched for the whole limit. When the command
// ended, the window goes, and last is its last line.
func (t Tmux) StartCheck(session, window string, limit time.Duration, ready func() bool) (last string, ok bool) {
	if limit == 0 {
		limit = 3 * time.Second
	}
	settle := min(500*time.Millisecond, limit)
	for start := time.Now(); ; {
		time.Sleep(100 * time.Millisecond)
		if !t.HasWindow(session, window) || t.PaneDead(session, window) {
			last := t.LastLine(session, window)
			t.KillWindow(session, window)
			return last, false
		}
		took := time.Since(start)
		if took >= limit || (took >= settle && ready != nil && ready()) {
			return "", true
		}
	}
}
