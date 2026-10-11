//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"
)

// keyEnter is what readKey returns for Enter.
const keyEnter = '\n'

// readKey shows prompt (marked text, already wrapped by the caller) and
// waits for one of keys, or Enter. In a real terminal it reads single key
// presses in raw mode: a read that holds more than one key (typed ahead,
// pasted, an arrow key) is ignored as a whole, and Ctrl-C or Ctrl-D is q.
// Anywhere else (tests, pipes) it reads a line: an empty one is Enter, a
// one-key line is that key, and the rest is ignored. The end of the input
// is q, with ok false.
func (e *env) readKey(prompt []string, keys string) (key byte, ok bool) {
	f, isFile := e.stdin.(*os.File)
	if !isFile || !term.IsTerminal(int(f.Fd())) || testTTY() {
		return e.readKeyLine(prompt, keys)
	}
	fd := int(f.Fd())
	// Raw before the prompt shows, so a key pressed early isn't echoed.
	saved, err := term.MakeRaw(fd)
	if err != nil {
		e.lines = nil
		return e.readKeyLine(prompt, keys)
	}
	restore := func() { term.Restore(fd, saved) }
	defer restore()
	stop := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGHUP)
	defer func() {
		signal.Stop(stop)
		close(done) // the goroutine below ends with this call
	}()
	go func() {
		select {
		case <-stop:
			restore()
			fmt.Fprintln(e.stdout)
			os.Exit(1)
		case <-done:
		}
	}()
	styled := streamStyled(e.stdout)
	for _, l := range prompt {
		fmt.Fprint(e.stdout, render(l, styled)+"\r\n")
	}
	buf := make([]byte, 16)
	for {
		n, err := f.Read(buf)
		if err != nil || n == 0 {
			return 'q', false
		}
		if key, ok := keyOf(buf[:n], keys); ok {
			return key, true
		}
	}
}

// keyOf reads one raw read: exactly one byte, Enter, Ctrl-C or Ctrl-D (as
// q), or one of keys. Anything else, several keys at once or an escape
// sequence such as an arrow, is ignored as a whole.
func keyOf(read []byte, keys string) (byte, bool) {
	if len(read) != 1 {
		return 0, false
	}
	// Caps Lock on: P is p.
	switch c := toLower(read[0]); {
	case c == '\r' || c == '\n':
		return keyEnter, true
	case c == 0x03 || c == 0x04:
		return 'q', true
	case strings.IndexByte(keys, c) >= 0:
		return c, true
	}
	return 0, false
}

// readKeyLine is readKey's line form, when raw mode isn't available.
func (e *env) readKeyLine(prompt []string, keys string) (byte, bool) {
	styled := streamStyled(e.stdout)
	for _, l := range prompt {
		fmt.Fprintln(e.stdout, render(l, styled))
	}
	for {
		line, ok := e.answer("")
		if !ok {
			return 'q', false
		}
		if line == "" {
			return keyEnter, true
		}
		if len(line) == 1 && strings.IndexByte(keys, toLower(line[0])) >= 0 {
			return toLower(line[0]), true
		}
	}
}

// toLower lowercases an ASCII letter.
func toLower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
