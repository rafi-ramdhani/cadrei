package memory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

// FromHistory brings back into to the notes the crew's memory folder held
// in git just before the last commit that removed some: another machine
// kept its memory on its own machine and pushed the removal. It merges
// them as MoveOut does. It returns that commit, and does nothing when
// it's done, the commit cadrei last brought notes back from: each
// removal is brought back once, so notes the user cleared later stay
// cleared.
func FromHistory(crew, to, machine, done string) (Result, string, error) {
	removal := LastRemoval(crew)
	if removal == "" || removal == done {
		return Result{}, "", nil
	}
	list, err := git(crew, "ls-tree", "-r", removal+"^", "--", Folder+"/")
	if err != nil {
		return Result{}, "", err
	}
	tmp, err := os.MkdirTemp("", "cadrei-memory-")
	if err != nil {
		return Result{}, "", err
	}
	defer os.RemoveAll(tmp)
	for _, line := range strings.Split(list, "\n") {
		// <mode> blob <object>\t<path>: files only, at the folder's top.
		meta, path, ok := strings.Cut(line, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 || f[1] != "blob" || (f[0] != "100644" && f[0] != "100755") || strings.Contains(strings.TrimPrefix(path, Folder+"/"), "/") {
			continue
		}
		data, err := gitx.Reading(crew, "cat-file", "blob", f[2]).Output()
		if err != nil {
			return Result{}, "", err
		}
		if err := os.WriteFile(filepath.Join(tmp, filepath.Base(path)), data, 0o600); err != nil {
			return Result{}, "", err
		}
	}
	res, _, err := merge(tmp, to, machine)
	return res, removal, err
}

// LastRemoval is the last commit that removed notes from the crew's
// memory folder, or "".
func LastRemoval(crew string) string {
	out, err := git(crew, "log", "-1", "--format=%H", "--diff-filter=D", "--", Folder)
	if err != nil {
		return ""
	}
	return out
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
}

// pullLimit is how long the pull from the backup may take.
const pullLimit = 60 * time.Second

// Pull brings in what the crew's other machines pushed: git pull --rebase
// --autostash, with the crew's hooks off. A clash inside the memory never
// produces a merged note: when both machines changed a note, the other
// machine's version keeps its name and this one's is kept whole beside it
// (<name>.from-<machine>.md, with its index line), and when one deleted a
// note the other changed, the changed one stays. A clash in any other file
// aborts the pull, so nothing changes, and names the files.
func Pull(crew, machine string) (Pulled, error) {
	var out Pulled
	if why := Busy(crew); why != "" {
		return out, fmt.Errorf("git is busy in the crew (%s)", why)
	}
	ctx, cancel := context.WithTimeout(context.Background(), pullLimit)
	defer cancel()
	pinned := gitx.Reading(crew, "pull", "-q", "--rebase", "--autostash")
	c := exec.CommandContext(ctx, pinned.Path, pinned.Args[1:]...)
	c.Env = append(pinned.Env, "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true")
	text, err := c.CombinedOutput()
	if ctx.Err() != nil {
		return out, fmt.Errorf("the pull took longer than %d seconds", int(pullLimit.Seconds()))
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
	return out, nil
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
