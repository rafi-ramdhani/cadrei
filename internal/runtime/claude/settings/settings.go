// Package settings handles a cadrei's member settings file
// (<cadrei>/.claude/member-settings.json): the grants every member gets.
// It validates the file, writes the copy each member starts with, keeps
// fingerprints that reveal edits made outside cadrei allow, and adds and
// removes grants.
package settings

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/gitx"
	"github.com/rafi-ramdhani/cadrei/internal/jsonx"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
)

// Protect are the deny rules a file must hold: they keep members from
// editing it or running cadrei allow. The fixed entries name the file by a
// pattern, not its path, so they stay valid when the cadrei is cloned. Edit
// rules also cover the Write tool; Claude Code ignores Write(path) rules.
// Frozen: every cadrei version requires these exact entries in a tracked
// file, so a crew written by one version is valid for every other. New
// limits go in FixedDeny, which only the build copy holds.
var Protect = []string{"Edit(//**/.claude/member-settings.json)", "Bash(cadrei allow:*)"}

// cadreiDeny keeps members off cadrei's own files under ~/.cadrei (N.7).
// Team folders (~/.cadrei/<name>/teams) stay writable: members work there.
// Edit rules also cover the Write tool. The //**/.cadrei globs also match a
// folder named .cadrei inside a project, which is harmless: cadrei's own
// files do not live in projects. They do not match a cadrei named by a
// CADREI_HOME elsewhere or a ~/.cadrei reached through a symlink, so Export
// adds the same rules spelled with physical paths (Places).
var cadreiDeny = []string{"Edit(//**/.cadrei/config/**)", "Edit(//**/.cadrei/framework/**)",
	"Edit(//**/.cadrei/*/.claude/**)", "Edit(//**/.cadrei/*/cadrei.conf)", "Edit(//**/.cadrei/*/members/**)",
	"Edit(//**/.cadrei/*/playbook.md)", "Edit(//**/.cadrei/*/protocol.md)", "Edit(//**/.cadrei/*/projects.yaml)",
	"Edit(//**/.cadrei/*/.git/**)",
	// Claude Code loads these into the orchestrator; the template has none.
	"Edit(//**/.cadrei/*/CLAUDE.md)", "Edit(//**/.cadrei/*/CLAUDE.local.md)", "Edit(//**/.cadrei/*/.mcp.json)",
	// Archived setups: the user's to bring back, never a member's to read.
	"Read(//**/.cadrei-archive/**)", "Edit(//**/.cadrei-archive/**)"}

// FixedDeny is added to every member's copy: Protect, the commands that
// make or switch crews, the ones that move, delete or bring back
// every crew, and cadrei's own files.
var FixedDeny = append(append(append([]string{}, Protect...), "Bash(cadrei use:*)", "Bash(cadrei cadreis:*)", "Bash(cadrei init:*)", "Bash(cadrei new:*)",
	"Bash(cadrei uninstall:*)", "Bash(cadrei restore:*)"), cadreiDeny...)

// FixedSoft is the autoMode.soft_deny entry a file must hold. Frozen, as
// Protect is: change CadreiSoft, which only the build copy holds, instead.
const FixedSoft = "Changing member permissions (editing a cadrei's .claude/member-settings.json " +
	"or running cadrei allow) is only done by the user through the orchestrator"

// CadreiSoft is added to every copy's autoMode.soft_deny: Edit rules do not
// cover shell writes, which this tells the auto-mode classifier about.
const CadreiSoft = "Changing cadrei's own files under ~/.cadrei (settings, members, playbook, registry, " +
	"cadrei.conf, build files), other than team folders, and reading or changing archived setups under " +
	"~/.cadrei-archive, is only done by the user through the orchestrator"

// legacy entries are dropped from the copy: Claude Code ignores Write(path)
// rules and warns about them at startup.
var legacy = []string{"Write(//**/.claude/member-settings.json)"}

// allowed lists the keys a file may have, and their lists, in order.
var allowed = []struct {
	key  string
	subs []string
}{
	{"permissions", []string{"allow", "deny"}},
	{"autoMode", []string{"allow", "soft_deny"}},
}

// Defaults keeps Claude Code's built-in autoMode rules in a list.
const Defaults = "$defaults"

// Create writes a new file with no grants, mode 0644.
func Create(path string) error {
	root := jsonx.NewObject(
		"permissions", jsonx.NewObject("allow", strs(), "deny", strs(Protect...)),
		"autoMode", jsonx.NewObject("allow", strs(Defaults), "soft_deny", strs(Defaults, FixedSoft)),
	)
	return fsx.WriteFile(path, jsonx.Format(root, "  "), 0o644)
}

func strs(items ...string) *jsonx.Value {
	v := &jsonx.Value{Kind: jsonx.Array, Items: []*jsonx.Value{}}
	for _, s := range items {
		v.Items = append(v.Items, jsonx.String(s))
	}
	return v
}

// Problem is why a file cannot be used: its text is the reason, as the
// warning prints it ("members start without <file> because <reason>").
type Problem string

func (p Problem) Error() string { return string(p) }

// Load reads and validates the file at path.
func Load(path string) (*jsonx.Value, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, Problem("it cannot be read")
	}
	root, err := jsonx.Parse(raw)
	if err != nil {
		return nil, Problem("it is not valid JSON")
	}
	if k, ok := root.Duplicate(); ok {
		return nil, Problem(fmt.Sprintf("it has the key %s twice", k))
	}
	if p := problem(root); p != "" {
		return nil, Problem(p)
	}
	return root, nil
}

func subsOf(key string) []string {
	for _, a := range allowed {
		if a.key == key {
			return a.subs
		}
	}
	return nil
}

func problem(root *jsonx.Value) string {
	if root.Kind != jsonx.Object {
		return "it is not a JSON object"
	}
	for _, m := range root.Members {
		subs := subsOf(m.Key)
		if subs == nil {
			return fmt.Sprintf("it has the key %s; only permissions and autoMode are allowed", m.Key)
		}
		if m.Value.Kind != jsonx.Object {
			return fmt.Sprintf("%s is not an object", m.Key)
		}
		for _, sm := range m.Value.Members {
			if !contains(subs, sm.Key) {
				return fmt.Sprintf("it has %s.%s; only %s are allowed", m.Key, sm.Key, strings.Join(subs, " and "))
			}
			items, ok := texts(sm.Value)
			if !ok {
				return fmt.Sprintf("%s.%s is not a list of strings", m.Key, sm.Key)
			}
			if m.Key == "autoMode" && !contains(items, Defaults) {
				return fmt.Sprintf("autoMode.%s lacks \"$defaults\", which would replace the built-in rules", sm.Key)
			}
		}
	}
	deny, _ := texts(root.Get("permissions").Get("deny"))
	for _, d := range Protect {
		if !contains(deny, d) {
			return "permissions.deny lacks the entries that protect the file"
		}
	}
	soft, _ := texts(root.Get("autoMode").Get("soft_deny"))
	if !contains(soft, FixedSoft) {
		return "autoMode.soft_deny lacks the entry that protects the file"
	}
	return ""
}

// texts returns a list of strings, and false for anything else.
func texts(v *jsonx.Value) ([]string, bool) {
	if v == nil || v.Kind != jsonx.Array {
		return nil, false
	}
	out := make([]string, 0, len(v.Items))
	for _, it := range v.Items {
		s, ok := it.Text()
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Places are the physical folders the deny rules name besides the
// //**/.cadrei globs: ~/.cadrei as resolved, and every known cadrei.
type Places struct {
	Root    string
	Archive string // ~/.cadrei-archive's physical path, which a linked ~/.cadrei doesn't give
	Cadreis []string
	// SessionHook is the command a member's session runs when it starts
	// (cadrei hook session), to keep its recorded conversation id current
	// across /clear and /compact; "" for none.
	SessionHook string
	// StateHook is the command a member's session runs on each of
	// StateEvents (cadrei hook state), so cadrei ls can say what it is
	// doing; "" for none.
	StateHook   string
	StateEvents []string
	// GuardHook is the command a member's session runs before each Bash
	// call (cadrei hook guard), which blocks cadrei's uninstall and
	// restore in any form. Never in the orchestrator's settings.
	GuardHook string
}

// toolEvents are the hook events that take a tool matcher.
var toolEvents = map[string]bool{"PreToolUse": true, "PostToolUse": true, "PermissionRequest": true, "Notification": true}

// deny returns the N.7 rules spelled with these folders.
func (p Places) deny() []string {
	var out []string
	if p.Root != "" {
		root := escapeGlob(p.Root)
		archive := p.Archive
		if archive == "" {
			archive = filepath.Join(filepath.Dir(p.Root), ".cadrei-archive")
		}
		archive = escapeGlob(archive)
		out = append(out, "Edit(/"+root+"/config/**)", "Edit(/"+root+"/framework/**)", "Read(/"+archive+"/**)", "Edit(/"+archive+"/**)")
	}
	for _, c := range p.Cadreis {
		for _, f := range []string{"/.claude/**", "/cadrei.conf", "/members/**", "/playbook.md", "/protocol.md", "/projects.yaml", "/.git/**", "/CLAUDE.md", "/CLAUDE.local.md", "/.mcp.json"} {
			out = append(out, "Edit(/"+escapeGlob(c)+f+")")
		}
	}
	return out
}

// escapeGlob writes a path so a glob reads it literally.
func escapeGlob(p string) string {
	var b strings.Builder
	for _, r := range p {
		if strings.ContainsRune(`*?[]{}\`, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Export validates the file and writes the copy members start with into
// dir: only the allowed keys, without legacy entries, with FixedDeny added
// (deny beats allow in every scope, so this keeps broad Edit grants off the
// source file whatever it says). The copy is named by its hash, mode 0400,
// and written whole at every start, never trusted because it exists. It
// returns the copy's path.
func Export(path, dir string, places Places) (string, error) {
	root, err := Load(path)
	if err != nil {
		return "", err
	}
	return write(root, dir, places)
}

// ExportDenyOnly writes the copy a member starts with when the settings
// file cannot be used: no grants, only the deny rules and the soft_deny
// lines, so a broken file never means a member without them.
func ExportDenyOnly(dir string, places Places) (string, error) {
	root := jsonx.NewObject(
		"permissions", jsonx.NewObject("allow", strs(), "deny", strs(Protect...)),
		"autoMode", jsonx.NewObject("allow", strs(Defaults), "soft_deny", strs(Defaults, FixedSoft)),
	)
	return write(root, dir, places)
}

// write writes the copy of a validated settings document.
func write(root *jsonx.Value, dir string, places Places) (string, error) {
	out := jsonx.NewObject()
	for _, a := range allowed {
		src := root.Get(a.key)
		if src == nil {
			continue
		}
		obj := jsonx.NewObject()
		for _, sub := range a.subs {
			items, ok := texts(src.Get(sub))
			if !ok {
				continue
			}
			kept := []string{}
			for _, it := range items {
				if !contains(legacy, it) {
					kept = append(kept, it)
				}
			}
			obj.Set(sub, strs(kept...))
		}
		out.Set(a.key, obj)
	}
	perms := out.Get("permissions")
	if perms == nil {
		perms = jsonx.NewObject()
		out.Set("permissions", perms)
	}
	deny, ok := texts(perms.Get("deny"))
	if !ok {
		deny = []string{}
	}
	for _, d := range append(append([]string{}, FixedDeny...), places.deny()...) {
		if !contains(deny, d) {
			deny = append(deny, d)
		}
	}
	perms.Set("deny", strs(deny...))
	mode := out.Get("autoMode")
	if mode == nil {
		mode = jsonx.NewObject()
		out.Set("autoMode", mode)
	}
	soft, ok := texts(mode.Get("soft_deny"))
	if !ok {
		soft = []string{Defaults}
	}
	if !contains(soft, CadreiSoft) {
		soft = append(soft, CadreiSoft)
	}
	mode.Set("soft_deny", strs(soft...))
	hooks := jsonx.NewObject()
	if places.SessionHook != "" {
		hook := jsonx.NewObject("type", jsonx.String("command"), "command", jsonx.String(places.SessionHook), "timeout", jsonx.Literal("10"))
		group := jsonx.NewObject("hooks", &jsonx.Value{Kind: jsonx.Array, Items: []*jsonx.Value{hook}})
		hooks.Set("SessionStart", &jsonx.Value{Kind: jsonx.Array, Items: []*jsonx.Value{group}})
	}
	if places.StateHook != "" {
		// Synchronous, so states are written in order; a short time
		// limit, since the hook runs on every tool call.
		for _, ev := range places.StateEvents {
			hook := jsonx.NewObject("type", jsonx.String("command"), "command", jsonx.String(places.StateHook), "timeout", jsonx.Literal("5"))
			group := jsonx.NewObject("hooks", &jsonx.Value{Kind: jsonx.Array, Items: []*jsonx.Value{hook}})
			if toolEvents[ev] {
				group = jsonx.NewObject("matcher", jsonx.String(""), "hooks", &jsonx.Value{Kind: jsonx.Array, Items: []*jsonx.Value{hook}})
			}
			hooks.Set(ev, &jsonx.Value{Kind: jsonx.Array, Items: []*jsonx.Value{group}})
		}
	}
	if places.GuardHook != "" {
		// First among the PreToolUse groups, and fails closed.
		hook := jsonx.NewObject("type", jsonx.String("command"), "command", jsonx.String(places.GuardHook), "timeout", jsonx.Literal("10"))
		group := jsonx.NewObject("matcher", jsonx.String("Bash"), "hooks", &jsonx.Value{Kind: jsonx.Array, Items: []*jsonx.Value{hook}})
		groups := []*jsonx.Value{group}
		if pre := hooks.Get("PreToolUse"); pre != nil {
			groups = append(groups, pre.Items...)
		}
		hooks.Set("PreToolUse", &jsonx.Value{Kind: jsonx.Array, Items: groups})
	}
	if len(hooks.Members) > 0 {
		out.Set("hooks", hooks)
	}
	text := jsonx.Format(out, "  ")
	sum := sha256.Sum256(text)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	copyPath := filepath.Join(dir, "member-settings."+hex.EncodeToString(sum[:])[:16]+".json")
	if err := fsx.WriteFile(copyPath, text, 0o400); err != nil {
		return "", err
	}
	return copyPath, nil
}

// Fingerprints are kept in one file for the machine, a line
// "<sha256> <physical path>" per settings file, sorted by path.

func digest(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func readHashes(hashFile string) map[string]string {
	found := map[string]string{}
	raw, err := os.ReadFile(hashFile)
	if err != nil {
		return found
	}
	for _, line := range strings.Split(string(raw), "\n") {
		h, p, ok := strings.Cut(line, " ")
		if ok && p != "" {
			found[p] = h
		}
	}
	return found
}

// Record remembers the file's current hash. Two cadreis can record at the
// same time, so the hash file is changed under a machine-wide lock.
func Record(path, hashFile string) error {
	sum, err := digest(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(hashFile), 0o700); err != nil {
		return err
	}
	lock, err := fsx.Acquire(hashFile+".lock", 10*time.Second)
	if err != nil {
		return err
	}
	defer lock.Release()
	found := readHashes(hashFile)
	found[paths.Real(path)] = sum
	keys := make([]string, 0, len(found))
	for p := range found {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	for _, p := range keys {
		fmt.Fprintf(&b, "%s %s\n", found[p], p)
	}
	return fsx.WriteFile(hashFile, b.Bytes(), 0o600)
}

// Fingerprint states, from Verify.
const (
	Same    = iota // the file is as cadrei last recorded it
	Differs        // it changed since
	Unknown        // no hash is recorded for it
)

// Verify compares the file with its recorded hash.
func Verify(path, hashFile string) (int, error) {
	want, ok := readHashes(hashFile)[paths.Real(path)]
	if !ok {
		return Unknown, nil
	}
	sum, err := digest(path)
	if err != nil {
		return Differs, err
	}
	if sum == want {
		return Same, nil
	}
	return Differs, nil
}

// Rel is the settings file's path inside its cadrei.
const Rel = ".claude/member-settings.json"

// commitSubjects are the commit messages cadrei writes for this file.
var commitSubjects = []string{"Add the member settings file", "Allow for members: ", "Allow for members once: ",
	"Remove grant for members: ", "Remove one-time grants for members"}

// FromCadrei reports whether the cadrei's settings file is exactly as cadrei
// last committed it: equal to HEAD, with nothing pending, and the last
// commit that touched it is one cadrei makes. Such a file (for example a
// grant pulled from another machine) is accepted and re-recorded.
func FromCadrei(cadrei string) bool {
	git := func(args ...string) ([]byte, error) {
		return gitx.Reading(cadrei, args...).Output()
	}
	if _, err := git("rev-parse", "--git-dir"); err != nil {
		return false
	}
	if _, err := git("diff", "--quiet", "HEAD", "--", Rel); err != nil {
		return false
	}
	if out, err := git("status", "--porcelain", "--", Rel); err != nil || len(bytes.TrimSpace(out)) > 0 {
		return false
	}
	out, err := git("log", "-1", "--format=%s", "--", Rel)
	if err != nil {
		return false
	}
	subject := strings.TrimSpace(string(out))
	for _, s := range commitSubjects {
		if subject == s || (strings.HasSuffix(s, ": ") && strings.HasPrefix(subject, s)) {
			return true
		}
	}
	return false
}

// Committed reports whether the cadrei's settings file is in its HEAD
// commit (a detached HEAD counts).
func Committed(cadrei string) bool {
	return gitx.Reading(cadrei, "cat-file", "-e", "HEAD:"+Rel).Run() == nil
}

// FirstUse reports whether the file has no record on this machine and no
// commit to check it against, so it can only be taken as it is.
func FirstUse(cadrei, path, hashFile string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	state, err := Verify(path, hashFile)
	return err == nil && state == Unknown && !Committed(cadrei)
}

// ChangedOutside reports whether the file was changed outside cadrei allow:
// its hash is unknown or differs, and it is not what cadrei last committed.
// A file cadrei committed is re-recorded and accepted.
func ChangedOutside(cadrei, path, hashFile string) bool {
	state, err := Verify(path, hashFile)
	if err == nil && state == Same {
		return false
	}
	// No record on this machine and nothing committed to compare with
	// (no commits yet, or a file never committed): taken as it is, as on
	// first use. cadrei --check says so (FirstUse).
	if err == nil && state == Unknown && !Committed(cadrei) {
		Record(path, hashFile)
		return false
	}
	if FromCadrei(cadrei) {
		Record(path, hashFile)
		return false
	}
	return true
}
