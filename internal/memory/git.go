package memory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/conf"
	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/gitx"
)

// UnionLine is the .gitattributes line that merges two machines' indexes
// by keeping both sides' lines. Notes aren't merged at all: Pull keeps
// both versions of a note both machines changed, whole.
const UnionLine = "/" + Folder + "/" + Index + " merge=union"

// oldUnion is the line a build of 0.2.2 before its review wrote, which
// merged notes too: Attributes and the memory commit replace it.
var oldUnion = []string{"# The orchestrator's memory: two machines' lines are both kept.", "/" + Folder + "/** merge=union"}

// withoutOldUnion is text without oldUnion's lines.
func withoutOldUnion(text string) string {
	var keep []string
	for _, l := range strings.SplitAfter(text, "\n") {
		if t := strings.TrimSpace(l); t == oldUnion[0] || t == oldUnion[1] {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "")
}

// attributesText is what Attributes adds.
const attributesText = "# The orchestrator's memory index: two machines' lines are both kept.\n" + UnionLine + "\n"

// Attributes adds UnionLine to the crew's .gitattributes when it isn't
// there. It reports whether it added it. The line is committed with the
// memory (Commit).
func Attributes(crew string) (bool, error) {
	file := filepath.Join(crew, ".gitattributes")
	raw, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	text := withoutOldUnion(string(raw))
	if hasUnionLine(file) {
		if text == string(raw) {
			return false, nil
		}
		return true, fsx.WriteFile(file, []byte(text), 0o644)
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += attributesText
	return true, fsx.WriteFile(file, []byte(text), 0o644)
}

// Busy says why git can't take a commit in the crew now, or "": another
// git command holds its index, or a rebase, merge or cherry-pick is under
// way.
func Busy(crew string) string {
	dir := gitDir(crew)
	if dir == "" {
		return "no git repository"
	}
	for _, f := range []string{"index.lock", "rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		if _, err := os.Lstat(filepath.Join(dir, f)); err == nil {
			return f
		}
	}
	// Files git marked as clashing (an autostash that didn't apply
	// cleanly, say): a commit would record their conflict marks.
	if out, err := git(crew, "ls-files", "-u"); err != nil || out != "" {
		return "unmerged files"
	}
	return ""
}

// gitDir is the crew's own git folder, or "".
func gitDir(crew string) string {
	dir := filepath.Join(crew, ".git")
	if st, err := os.Lstat(dir); err == nil && st.IsDir() {
		return dir
	}
	return ""
}

// git runs git in the crew without the crew's own hooks or fsmonitor.
func git(crew string, args ...string) (string, error) {
	out, err := gitx.Reading(crew, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Commit commits the changes to the crew's memory folder and its
// .gitattributes as one commit with msg, and nothing else. It does
// nothing while git is busy (Busy), without a git identity, or with
// nothing to commit, and never fails: it reports whether it committed.
func Commit(crew, msg string) bool { return commit(crew, msg, true) }

// CommitRemoval commits the removal of the crew's memory folder with msg,
// and nothing else, as Commit does.
func CommitRemoval(crew, msg string) bool {
	if _, err := os.Lstat(Dir(crew)); err == nil || !tracked(crew, Folder) {
		return false
	}
	return commit(crew, msg, false)
}

// commit builds the commit in an index of its own, from HEAD: the memory
// folder as it is, and with attributes, HEAD's .gitattributes with the
// union line added when the crew's has it. So nothing else the user
// changed or staged goes in, not even their own edits to .gitattributes,
// and their index keeps what they staged.
func commit(crew, msg string, attributes bool) bool {
	dir := gitDir(crew)
	if Busy(crew) != "" {
		return false
	}
	if name, err := git(crew, "config", "user.email"); err != nil || name == "" {
		return false
	}
	head, _ := git(crew, "rev-parse", "-q", "--verify", "HEAD")
	// An edit the user staged in .gitattributes stays staged.
	_, unstaged := git(crew, "diff", "--cached", "--quiet", "--", ".gitattributes")
	userStaged := unstaged != nil
	index := filepath.Join(dir, "cadrei-memory-index")
	defer os.Remove(index)
	in := func(stdin string, args ...string) (string, error) {
		c := gitx.Reading(crew, args...)
		c.Env = append(c.Env, "GIT_INDEX_FILE="+index)
		c.Stdin = strings.NewReader(stdin)
		out, err := c.Output()
		return strings.TrimSpace(string(out)), err
	}
	from := []string{"read-tree", "--empty"}
	if head != "" {
		from = []string{"read-tree", head}
	}
	if _, err := in("", from...); err != nil {
		return false
	}
	if _, err := os.Lstat(Dir(crew)); err == nil || tracked(crew, Folder) {
		if _, err := in("", "add", "-A", "--", Folder); err != nil {
			return false
		}
	}
	if attributes && hasUnionLine(filepath.Join(crew, ".gitattributes")) {
		was, err := git(crew, "show", head+":.gitattributes")
		if head == "" || err != nil {
			was = ""
		}
		if fixed := strings.TrimRight(withoutOldUnion(was+"\n"), "\n"); fixed != was || !strings.Contains("\n"+was+"\n", "\n"+UnionLine+"\n") {
			was = fixed
			if !strings.Contains("\n"+was+"\n", "\n"+UnionLine+"\n") {
				if was != "" {
					was += "\n"
				}
				was += attributesText
			} else {
				was += "\n"
			}
			blob, err := in(was, "hash-object", "-w", "--stdin")
			if err != nil {
				return false
			}
			if _, err := in("", "update-index", "--add", "--cacheinfo", "100644,"+blob+",.gitattributes"); err != nil {
				return false
			}
		}
	}
	tree, err := in("", "write-tree")
	if err != nil {
		return false
	}
	if head != "" {
		if was, _ := git(crew, "rev-parse", head+"^{tree}"); was == tree {
			return false // nothing to commit
		}
	}
	args := []string{"commit-tree", tree, "-m", msg}
	if head != "" {
		args = append(args, "-p", head)
	}
	next, err := in("", args...)
	if err != nil {
		return false
	}
	ref := []string{"update-ref", "-m", "commit: " + msg, "HEAD", next}
	if head != "" {
		ref = append(ref, head)
	}
	if _, err := git(crew, ref...); err != nil {
		return false
	}
	// The user's index catches up on the memory, and on .gitattributes
	// unless they staged an edit there.
	reset := []string{"reset", "-q", "--", Folder}
	if !userStaged {
		reset = append(reset, ".gitattributes")
	}
	git(crew, reset...)
	return true
}

// hasUnionLine reports whether the file holds UnionLine.
func hasUnionLine(file string) bool {
	raw, _ := os.ReadFile(file)
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == UnionLine {
			return true
		}
	}
	return false
}

// OptOut is the message of cadrei's commit that removes the memory folder
// from a crew that keeps its memory on each machine now.
const OptOut = "Keep the orchestrator's memory on this machine"

// FromHistory brings back into to the notes the crew's memory folder held
// in git just before the last commit that removed some, when that commit
// is an opt-out (LastOptOut): another machine kept its memory on its own
// machine and pushed the removal. It merges them as MoveOut does. It
// returns that commit, and does nothing when it's done, the commit cadrei
// last brought notes back from: each removal is brought back once, so
// notes the user cleared later stay cleared. A note that clashes is kept
// as <name>.from-crew.md. The notes in skip, and their index lines, stay
// out: the ones this machine's pull set aside just came in under those
// names, newer than the history's, and what the other machine changed
// came in with them (setAside).
func FromHistory(crew, to, done string, skip []string) (Result, string, error) {
	removal := LastOptOut(crew)
	if removal == "" || removal == done {
		return Result{}, "", nil
	}
	then, err := notesAt(crew, removal+"^")
	if err != nil {
		return Result{}, "", err
	}
	for _, name := range skip {
		delete(then, name)
	}
	res, err := fromGit(crew, then, to, skip)
	return res, removal, err
}

// notesAt is the notes and index the crew's memory folder holds at rev,
// each name with its git object: files only, at the folder's top.
func notesAt(crew, rev string) (map[string]string, error) {
	list, err := git(crew, "ls-tree", "-r", rev, "--", Folder+"/")
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(list, "\n") {
		// <mode> blob <object>\t<path>
		meta, path, ok := strings.Cut(line, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 || f[1] != "blob" || (f[0] != "100644" && f[0] != "100755") || strings.Contains(strings.TrimPrefix(path, Folder+"/"), "/") {
			continue
		}
		out[filepath.Base(path)] = f[2]
	}
	return out, nil
}

// fromGit merges the notes from notesAt into to, as FromHistory does,
// with the index lines that link to a note in skip left out.
func fromGit(crew string, notes map[string]string, to string, skip []string) (Result, error) {
	if len(notes) == 0 {
		return Result{}, nil
	}
	tmp, err := os.MkdirTemp("", "cadrei-memory-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)
	for name, object := range notes {
		data, err := gitx.Reading(crew, "cat-file", "blob", object).Output()
		if err != nil {
			return Result{}, err
		}
		if name == Index {
			var keep []string
			for _, l := range strings.SplitAfter(string(data), "\n") {
				if !slices.ContainsFunc(skip, func(n string) bool { return strings.Contains(l, "("+n+")") }) {
					keep = append(keep, l)
				}
			}
			data = []byte(strings.Join(keep, ""))
		}
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0o600); err != nil {
			return Result{}, err
		}
	}
	res, _, err := merge(tmp, to, FromCrew)
	return res, err
}

// LastOptOut is the last commit that removed notes from the crew's memory
// folder, when that commit took the memory out of the crew: cadrei's
// OptOut commit, or one made while the crew's cadrei.conf said
// MEMORY=machine. A crew that only emptied its memory has none: the user
// cleared those notes.
func LastOptOut(crew string) string { return lastOptOut(crew, "HEAD") }

// lastOptOut is LastOptOut in rev's history.
func lastOptOut(crew, rev string) string {
	out, err := git(crew, "log", "-1", "--format=%H %s", "--diff-filter=D", rev, "--", Folder)
	commit, subject, _ := strings.Cut(out, " ")
	if err != nil || commit == "" || (subject != OptOut && !onMachine(crew, commit)) {
		return ""
	}
	return commit
}

// onMachine reports whether the crew's cadrei.conf at rev says
// MEMORY=machine.
func onMachine(crew, rev string) bool {
	raw, err := git(crew, "show", rev+":cadrei.conf")
	if err != nil {
		return false
	}
	values, _ := conf.Parse(raw)
	return strings.EqualFold(strings.TrimSpace(values["MEMORY"]), "machine")
}

// tracked reports whether git tracks anything at p in the crew.
func tracked(crew, p string) bool {
	out, err := git(crew, "ls-files", "--", p)
	return err == nil && out != ""
}

// Unpushed is how many commits the crew's branch has that its upstream
// lacks, as git last saw it (no network): -1 when it has no remote or no
// upstream.
func Unpushed(crew string) int {
	if out, err := git(crew, "remote"); err != nil || out == "" {
		return -1
	}
	out, err := git(crew, "rev-list", "--count", "@{upstream}..HEAD")
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return -1
	}
	return n
}

// HasRemote reports whether the crew has a git remote: a backup.
func HasRemote(crew string) bool {
	out, err := git(crew, "remote")
	return err == nil && out != ""
}

// A Pulled is what Pull did.
type Pulled struct {
	// Kept are the notes both machines changed: the other machine's
	// version under the note's name, and this one's beside it under these
	// names, as the move in keeps a clash.
	Kept []string
	// Restored are notes one machine deleted and the other changed: the
	// changed one is kept.
	Restored []string
	// Conflicts are files outside the memory that clash. The pull was
	// aborted, and nothing changed.
	Conflicts []string
	// SetAside is how many notes were set aside because another machine
	// keeps the memory on its own now: they wait in the aside folder for
	// the next open.
	SetAside int
	// Stashed are files whose uncommitted changes clashed with what came
	// in: git marked them, and kept the changes in its stash.
	Stashed []string
	// Changed are the crew's files outside the memory that the other
	// machines' commits change: the playbook, members, and so on.
	Changed []string
}

// pullLimit is how long the pull from the backup may take. Tests shorten
// it.
var pullLimit = 60 * time.Second

// Pull brings in what the crew's other machines pushed: git pull --rebase
// --autostash, with the crew's hooks off. A clash inside the memory never
// produces a merged note: when both machines changed a note, the other
// machine's version keeps its name and this one's is kept whole beside it
// (<name>.from-<machine>.md, with its index line), and when one deleted a
// note the other changed, the changed one stays. A clash in any other file
// aborts the pull, so nothing changes, and names the files.
//
// When the other machine removed the whole memory folder (it keeps its
// memory on itself now), this machine's notes, committed or not, are set
// aside in aside, outside git, for the next open, with what the other
// machine changed in the memory since this one last pulled, and its
// unpushed commits lose their memory changes before they're replayed: a
// note never goes back into the backup after an opt-out. An uncommitted
// change that clashes with what came in (git's autostash) is settled like
// a commit's when it's a note, and named in Stashed when it's anything
// else.
func Pull(crew, machine, aside string) (Pulled, error) {
	var out Pulled
	if why := Busy(crew); why != "" {
		return out, fmt.Errorf("git is busy in the crew (%s)", why)
	}
	ctx, cancel := context.WithTimeout(context.Background(), pullLimit)
	defer cancel()
	run := func(args ...string) ([]byte, error) {
		pinned := gitx.Reading(crew, args...)
		c := exec.CommandContext(ctx, pinned.Path, pinned.Args[1:]...)
		c.Env = append(pinned.Env, "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true")
		// At the limit, git and everything it started (ssh, the rebase)
		// stop together.
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
		c.WaitDelay = 2 * time.Second
		return c.CombinedOutput()
	}
	// A pull stopped at the limit says where it left the crew.
	late := func() (Pulled, error) {
		if rebasing(crew) {
			git(crew, "rebase", "--abort")
		}
		if why := Busy(crew); why != "" {
			return Pulled{}, fmt.Errorf("the pull took longer than %d seconds and was stopped, and git is still busy in the crew (%s)", int(pullLimit.Seconds()), why)
		}
		return Pulled{SetAside: out.SetAside}, fmt.Errorf("the pull took longer than %d seconds and was stopped, before anything came in", int(pullLimit.Seconds()))
	}
	if text, err := run("fetch", "-q"); err != nil {
		if ctx.Err() != nil {
			return late()
		}
		return out, errors.New(firstLine(string(text)))
	}
	// Names only, from stdout: git warns on stderr (past diff.renameLimit,
	// say). A moved file is named at both paths, and a name outside ASCII
	// as it is.
	if files, err := gitx.Reading(crew, "-c", "core.quotePath=false", "diff", "--name-only", "--no-renames", "HEAD...@{u}").Output(); err == nil {
		for _, f := range strings.Split(string(files), "\n") {
			if f != "" && !strings.HasPrefix(f, Folder+"/") {
				out.Changed = append(out.Changed, f)
			}
		}
	}
	n, err := setAside(crew, machine, aside)
	if err != nil {
		return out, err
	}
	out.SetAside = n
	text, err := run("pull", "-q", "--rebase", "--autostash")
	if ctx.Err() != nil {
		return late()
	}
	// A rebase that stopped: each stop is settled, or the whole pull is
	// aborted. Never more stops than commits it can replay.
	for i := 0; err != nil && rebasing(crew) && i < 1000; i++ {
		files, ferr := git(crew, "diff", "--name-only", "--diff-filter=U")
		if ferr != nil {
			break
		}
		var clash []string
		for _, f := range strings.Split(files, "\n") {
			if f != "" && !strings.HasPrefix(f, Folder+"/") {
				clash = append(clash, f)
			}
		}
		if len(clash) > 0 || files == "" {
			git(crew, "rebase", "--abort")
			out.Conflicts = clash
			if len(clash) == 0 {
				return out, errors.New(firstLine(string(text)))
			}
			return out, nil
		}
		for _, f := range strings.Split(files, "\n") {
			if err := settle(crew, f, machine, &out); err != nil {
				git(crew, "rebase", "--abort")
				return Pulled{}, err
			}
		}
		cont := gitx.Reading(crew, "rebase", "--continue")
		cont.Env = append(cont.Env, "GIT_EDITOR=true")
		text, err = cont.CombinedOutput()
	}
	if err != nil {
		if rebasing(crew) {
			git(crew, "rebase", "--abort")
		}
		return out, errors.New(firstLine(string(text)))
	}
	// Uncommitted changes git put back after the rebase (its autostash),
	// where one clashed: the stash stays, and the files are marked.
	files, _ := git(crew, "diff", "--name-only", "--diff-filter=U")
	if files == "" {
		return out, nil
	}
	for _, f := range strings.Split(files, "\n") {
		if !strings.HasPrefix(f, Folder+"/") {
			out.Stashed = append(out.Stashed, f)
			continue
		}
		// Stage 2 is what came in, stage 3 the stashed change: a note is
		// settled as in a rebase.
		if err := settle(crew, f, machine, &out); err != nil {
			return out, err
		}
	}
	if len(out.Stashed) == 0 {
		// Every clash was a note, now settled: the stash holds nothing more.
		dropAutostash(crew)
	}
	return out, nil
}

// dropAutostash drops the newest stash entry git's autostash left.
func dropAutostash(crew string) {
	list, _ := git(crew, "stash", "list", "--format=%gd %gs")
	for _, l := range strings.Split(list, "\n") {
		if ref, what, ok := strings.Cut(l, " "); ok && strings.Contains(what, "autostash") {
			git(crew, "stash", "drop", "-q", ref)
			return
		}
	}
}

// setAside handles an opt-out that came in: when this machine's crew
// tracks the memory folder and its upstream took the whole folder out
// with its cadrei.conf saying MEMORY=machine (not a crew that only
// emptied its memory, or opted back in), it sets this machine's notes
// (as they are, committed or not) aside in aside, with the other
// machine's changes since this one last pulled, rewrites the unpushed
// commits without the folder, so none of its notes is pushed again, and
// takes the folder out of the crew. It returns how many notes it set
// aside.
func setAside(crew, machine, aside string) (int, error) {
	up, err := git(crew, "rev-parse", "-q", "--verify", "@{upstream}")
	if err != nil || up == "" || !tracked(crew, Folder) {
		return 0, nil
	}
	if has, _ := git(crew, "ls-tree", "-r", "--name-only", up, "--", Folder+"/"); has != "" {
		return 0, nil
	}
	// An emptied memory reaches this machine as an emptying. An old
	// opt-out in the history doesn't count: the crew may have opted back
	// in since, and only the crew-wide setting removes the folder.
	if !onMachine(crew, up) {
		return 0, nil
	}
	base, err := git(crew, "merge-base", "HEAD", up)
	if err != nil {
		return 0, err
	}
	if merges, _ := git(crew, "rev-list", "--merges", base+"..HEAD"); merges != "" {
		return 0, errors.New("another machine keeps the memory on its own now, and this crew's unpushed commits include a merge, so cadrei can't take the notes out of them")
	}
	_, taken, err := merge(Dir(crew), aside, machine)
	if err != nil {
		return 0, err
	}
	// What the other machine changed in the memory since this one last
	// pulled comes in beside this machine's notes: the next open then
	// skips, in the history, every note set aside (FromHistory), so a
	// note this machine changed since gets no older copy, and nothing the
	// other machine changed is lost.
	if removal := lastOptOut(crew, up); removal != "" {
		then, err := notesAt(crew, removal+"^")
		if err != nil {
			return 0, err
		}
		had, err := notesAt(crew, base)
		if err != nil {
			return 0, err
		}
		for name, object := range then {
			if had[name] == object {
				delete(then, name)
			}
		}
		// Its index lines for this machine's other notes are older than
		// this machine's own, so only the lines for what it changed come.
		var mine []string
		for _, name := range notesIn(taken) {
			if _, changed := then[name]; !changed {
				mine = append(mine, name)
			}
		}
		if _, err := fromGit(crew, then, aside, mine); err != nil {
			return 0, err
		}
	}
	head, _ := git(crew, "rev-parse", "HEAD")
	commits, _ := git(crew, "rev-list", "--reverse", base+"..HEAD")
	index := filepath.Join(gitDir(crew), "cadrei-memory-index")
	defer os.Remove(index)
	in := func(env []string, stdin string, args ...string) (string, error) {
		c := gitx.Reading(crew, args...)
		c.Env = append(append(c.Env, "GIT_INDEX_FILE="+index), env...)
		c.Stdin = strings.NewReader(stdin)
		b, err := c.Output()
		return strings.TrimSpace(string(b)), err
	}
	parent := base
	for _, c := range strings.Fields(commits) {
		if _, err := in(nil, "", "read-tree", c); err != nil {
			return 0, err
		}
		if _, err := in(nil, "", "rm", "-r", "-q", "--cached", "--ignore-unmatch", "--", Folder); err != nil {
			return 0, err
		}
		tree, err := in(nil, "", "write-tree")
		if err != nil {
			return 0, err
		}
		if was, _ := git(crew, "rev-parse", parent+"^{tree}"); was == tree {
			continue // it only changed the memory
		}
		meta, err := git(crew, "log", "-1", "--format=%an%x00%ae%x00%aI", c)
		if err != nil {
			return 0, err
		}
		who := strings.Split(meta, "\x00")
		msg, _ := gitx.Reading(crew, "log", "-1", "--format=%B", c).Output()
		var env []string
		if len(who) == 3 {
			env = []string{"GIT_AUTHOR_NAME=" + who[0], "GIT_AUTHOR_EMAIL=" + who[1], "GIT_AUTHOR_DATE=" + who[2]}
		}
		next, err := in(env, string(msg), "commit-tree", tree, "-p", parent, "-F", "-")
		if err != nil {
			return 0, err
		}
		parent = next
	}
	if _, err := git(crew, "update-ref", "-m", "cadrei: set the memory aside", "HEAD", parent, head); err != nil {
		return 0, err
	}
	git(crew, "reset", "-q", "--", Folder)
	if err := clear(Dir(crew), taken); err != nil {
		return 0, err
	}
	return len(notesIn(taken)), nil
}

// notesIn is the notes among files merge took, the index not counted.
func notesIn(taken map[string][]byte) []string {
	var out []string
	for name := range taken {
		if name != Index {
			out = append(out, name)
		}
	}
	return out
}

// Move moves the notes in from into to, merged as MoveIn merges, and
// clears from: the notes Pull set aside, into the next open's folder.
func Move(from, to, machine string) (Result, error) {
	res, taken, err := merge(from, to, machine)
	if err != nil {
		return res, err
	}
	return res, clear(from, taken)
}

// rebasing reports whether a rebase is under way in the crew.
func rebasing(crew string) bool {
	dir := gitDir(crew)
	for _, f := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Lstat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

// settle resolves one clashing file of the memory, as Pull says. In a
// rebase, stage 2 is the other machine's (the branch being rebased onto)
// and stage 3 this machine's own commit being replayed.
func settle(crew, file, machine string, out *Pulled) error {
	theirs, _ := gitx.Reading(crew, "show", ":2:"+file).Output()
	_, theirsErr := git(crew, "cat-file", "-e", ":2:"+file)
	ours, _ := gitx.Reading(crew, "show", ":3:"+file).Output()
	_, oursErr := git(crew, "cat-file", "-e", ":3:"+file)
	path := filepath.Join(crew, file)
	switch {
	case theirsErr == nil && oursErr == nil && filepath.Base(file) == Index:
		// Both indexes, whole: union keeps every line of each.
		next, _ := mergeIndex(theirs, ours, nil, machine)
		if err := fsx.WriteFile(path, next, 0o644); err != nil {
			return err
		}
	case theirsErr == nil && oursErr == nil:
		if err := fsx.WriteFile(path, theirs, 0o644); err != nil {
			return err
		}
		name, same, err := freeName(filepath.Dir(path), filepath.Base(file), machine, ours)
		if err != nil {
			return err
		}
		if !same {
			if err := fsx.WriteFile(filepath.Join(filepath.Dir(path), name), ours, 0o644); err != nil {
				return err
			}
		}
		index := filepath.Join(filepath.Dir(path), Index)
		have, _ := os.ReadFile(index)
		if next, changed := mergeIndex(have, nil, map[string]string{filepath.Base(file): name}, machine); changed {
			if err := fsx.WriteFile(index, next, 0o644); err != nil {
				return err
			}
		}
		add := []string{"add", "--", file, filepath.Join(Folder, name)}
		if _, err := os.Stat(index); err == nil {
			add = append(add, filepath.Join(Folder, Index))
		}
		if _, err := git(crew, add...); err != nil {
			return err
		}
		out.Kept = append(out.Kept, name)
		return nil
	case theirsErr == nil:
		// This machine deleted it, the other changed it: keep the change.
		if err := fsx.WriteFile(path, theirs, 0o644); err != nil {
			return err
		}
		out.Restored = append(out.Restored, filepath.Base(file))
	default:
		// The other machine deleted it, this one changed it.
		if err := fsx.WriteFile(path, ours, 0o644); err != nil {
			return err
		}
		out.Restored = append(out.Restored, filepath.Base(file))
	}
	_, err := git(crew, "add", "--", file)
	return err
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimPrefix(strings.TrimSpace(line), "fatal: ")
}
