// Package fsx holds the file operations every writer in cadrei shares:
// atomic writes, locks, and copying a folder tree with a check that the
// copy is complete.
package fsx

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// WriteFile replaces path with data atomically: a temporary file in the
// same folder is written, synced and given perm, then renamed over path. A
// reader sees the old file or the new one, never a part.
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cadrei-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := syncFile(tmp); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// ErrBusy is returned when another cadrei command holds a lock for longer
// than the wait.
var ErrBusy = errors.New("another cadrei command is using it")

// Lock is an exclusive lock held by this process.
type Lock struct {
	f   *os.File // flock on this file
	dir string   // or, where flock is not supported, this mkdir lock
}

// staleAfter is how old a mkdir lock must be before it is taken over. It
// only applies on filesystems without flock; a flock is released by the
// kernel when its holder exits.
const staleAfter = 60 * time.Second

// forceDirLock makes Acquire use the mkdir lock, for tests.
var forceDirLock = false

// Acquire takes the lock at path (a file it creates if needed), waiting up
// to wait for another holder. On a filesystem without flock (some network
// filesystems), it falls back to a mkdir lock at path + ".d", taken over
// when older than a minute.
//
// The lock file is never followed through a symlink and never written:
// the flock is the lock. A file that is a symlink, not a regular file, not
// the user's own, or has another hard link is refused, so a link planted
// where cadrei keeps a lock cannot get another file changed.
func Acquire(path string, wait time.Duration) (*Lock, error) {
	if !forceDirLock {
		f, err := openLock(path)
		if err != nil {
			return nil, err
		}
		deadline := time.Now().Add(wait)
		for {
			err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
			if err == nil {
				return &Lock{f: f}, nil
			}
			if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
				f.Close()
				if unsupported(err) {
					break
				}
				return nil, fmt.Errorf("lock %s: %w", path, err)
			}
			if time.Now().After(deadline) {
				f.Close()
				return nil, ErrBusy
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return acquireDir(path+".d", wait)
}

// openLock opens (or creates) a lock file without following a symlink and
// checks it is a plain file of this user's with one link.
func openLock(path string) (*os.File, error) {
	return openOwn(path, "lock", unix.O_CREAT)
}

// openOwn opens path for reading, refusing anything but a regular file of
// the user's with one link; what names it in errors ("lock"). O_NONBLOCK:
// a FIFO planted here would otherwise block the open, and every cadrei
// command with it; this way the check below refuses it.
func openOwn(path, what string, flags int) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK|flags, 0o600)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("%s %s is a symlink; remove it", what, path)
		}
		if errors.Is(err, unix.ENOENT) {
			return nil, fmt.Errorf("%s %s: %w", what, path, fs.ErrNotExist)
		}
		return nil, fmt.Errorf("%s %s: %w", what, path, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	switch {
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		err = fmt.Errorf("%s %s is not a plain file; remove it", what, path)
	case int(st.Uid) != os.Geteuid():
		err = fmt.Errorf("%s %s belongs to another user; remove it", what, path)
	case st.Nlink != 1:
		err = fmt.Errorf("%s %s has another hard link; remove it", what, path)
	}
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

// ReadOwn reads a small file cadrei wrote: never through a symlink, never
// blocking on a FIFO, only a regular file of the user's with one link, and
// at most max bytes. A missing file gives an error wrapping
// fs.ErrNotExist.
func ReadOwn(path string, max int64) ([]byte, error) {
	f, err := openOwn(path, "file", 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("file %s is larger than %d bytes", path, max)
	}
	return data, nil
}

func unsupported(err error) bool {
	return errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOLCK)
}

func acquireDir(dir string, wait time.Duration) (*Lock, error) {
	deadline := time.Now().Add(wait)
	for {
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return &Lock{dir: dir}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("lock %s: %w", dir, err)
		}
		if st, err := os.Stat(dir); err == nil && time.Since(st.ModTime()) > staleAfter {
			// Rename it aside under a unique name, which only one process
			// can do, then retry; it is never renamed back.
			aside := fmt.Sprintf("%s.stale-%d-%d", dir, os.Getpid(), time.Now().UnixNano())
			if os.Rename(dir, aside) == nil {
				os.Remove(aside)
			}
			continue
		} else if err != nil && errors.Is(err, fs.ErrNotExist) {
			continue // released between the mkdir and the stat
		}
		if time.Now().After(deadline) {
			return nil, ErrBusy
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Release gives the lock up. The flock file stays (an empty lock is
// harmless and avoids a race between removing it and another open).
func (l *Lock) Release() error {
	if l.f != nil {
		unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
		return l.f.Close()
	}
	if err := os.Remove(l.dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// CopyTree copies the folder src to dst, which must not exist: folders,
// links as links (not followed), and files with their modes and
// modification times. Sockets and the like aren't copied. Every file and
// folder of the copy is flushed to disk before it returns, so a caller
// may remove src once SameTree agrees.
func CopyTree(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	var dirs []string
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, to)
		case info.IsDir():
			dirs = append(dirs, to)
			return os.MkdirAll(to, info.Mode().Perm()|0o700)
		case info.Mode().IsRegular():
			if err := copyFile(p, to, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chtimes(to, info.ModTime(), info.ModTime())
		}
		return nil // sockets and the like aren't copied
	})
	if err != nil {
		return err
	}
	// The folders last, deepest first, so each names what's in it.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := syncDir(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

// copyFile copies a file's bytes and flushes them to disk.
func copyFile(from, to string, perm fs.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := syncFile(out); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// syncDir flushes a folder's entries to disk. A filesystem that can't
// flush a folder (some network and FAT ones say so) isn't an error: the
// files in it were flushed one by one.
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syncFile(f); err != nil && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOTSUP) {
		return err
	}
	return nil
}

// syncs counts the flushes, for tests.
var syncs atomic.Int64

// Syncs is how many files and folders were flushed to disk so far.
func Syncs() int64 { return syncs.Load() }

// syncFile flushes a file or folder to disk (F_FULLFSYNC on macOS).
func syncFile(f *os.File) error {
	syncs.Add(1)
	return flush(f)
}

// flush is (*os.File).Sync; a test makes it fail as a disk would.
var flush = (*os.File).Sync

// SyncDir flushes a folder's entries to disk: after a rename into it,
// say, before the only other copy goes.
func SyncDir(dir string) error { return syncDir(dir) }

// SameTree checks that dst holds every file, folder and link of src, each
// file with its size and modification time (within what the disk keeps,
// SameTime), each link with its target. Sockets and the like, which
// CopyTree skips, are skipped here too.
func SameTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		a, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !a.Mode().IsRegular() && !a.IsDir() && a.Mode()&fs.ModeSymlink == 0 {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		b, err := os.Lstat(filepath.Join(dst, rel))
		if err != nil || a.Mode().Type() != b.Mode().Type() ||
			(a.Mode().IsRegular() && (a.Size() != b.Size() || !SameTime(a.ModTime(), b.ModTime()))) {
			return fmt.Errorf("the copy of %s doesn't match", p)
		}
		if a.Mode()&fs.ModeSymlink != 0 {
			at, _ := os.Readlink(p)
			if bt, _ := os.Readlink(filepath.Join(dst, rel)); at != bt {
				return fmt.Errorf("the copy of %s doesn't match", p)
			}
		}
		return nil
	})
}

// SameTime reports whether two modification times are the same as far as
// both disks can tell: HFS+ keeps whole seconds, exFAT 10 ms and FAT 2 s,
// so a copy's time can be rounded by up to 2 seconds.
func SameTime(a, b time.Time) bool {
	d := a.Sub(b)
	return d > -2*time.Second && d < 2*time.Second
}

// WriteFileNoSync is WriteFile without the sync: still atomic (a temp file
// and a rename), for data that's cheap to lose in a crash.
func WriteFileNoSync(path string, data []byte, perm fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cadrei-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
