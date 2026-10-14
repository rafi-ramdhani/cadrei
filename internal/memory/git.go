package memory

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/gitx"
)

// UnionLine is the .gitattributes line that merges two machines' notes,
// the index and each note, by keeping both sides' lines: a note both
// machines wrote never stops a pull, and nothing of either is lost.
const UnionLine = "/" + Folder + "/** merge=union"

// attributesText is what Attributes adds.
const attributesText = "# The orchestrator's memory: two machines' lines are both kept.\n" + UnionLine + "\n"

// Attributes adds UnionLine to the crew's .gitattributes when it isn't
// there. It reports whether it added it. The line is committed with the
// memory (Commit).
func Attributes(crew string) (bool, error) {
	file := filepath.Join(crew, ".gitattributes")
	raw, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if hasUnionLine(file) {
		return false, nil
	}
	text := string(raw)
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
		if !strings.Contains("\n"+was+"\n", "\n"+UnionLine+"\n") {
			if was != "" {
				was += "\n"
			}
			blob, err := in(was+attributesText, "hash-object", "-w", "--stdin")
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
	// The user's index catches up on those two paths only.
	git(crew, "reset", "-q", "--", Folder, ".gitattributes")
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
// them as MoveOut does.
func FromHistory(crew, to, machine string) (Result, error) {
	removal, err := git(crew, "log", "-1", "--format=%H", "--diff-filter=D", "--", Folder)
	if err != nil || removal == "" {
		return Result{}, err
	}
	list, err := git(crew, "ls-tree", "-r", removal+"^", "--", Folder+"/")
	if err != nil {
		return Result{}, err
	}
	tmp, err := os.MkdirTemp("", "cadrei-memory-")
	if err != nil {
		return Result{}, err
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
			return Result{}, err
		}
		if err := os.WriteFile(filepath.Join(tmp, filepath.Base(path)), data, 0o600); err != nil {
			return Result{}, err
		}
	}
	res, _, err := merge(tmp, to, machine)
	return res, err
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
