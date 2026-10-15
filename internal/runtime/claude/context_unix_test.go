//go:build !windows

package claude

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Review L3: a transcript path that names a pipe gives nothing, at once,
// instead of waiting for a writer.
func TestContextOfAPipe(t *testing.T) {
	pipe := filepath.Join(t.TempDir(), "chat.jsonl")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() {
		_, ok := sessions{}.Context(pipe, "")
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Error("a pipe gave a context")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reading a pipe hung")
	}
}
