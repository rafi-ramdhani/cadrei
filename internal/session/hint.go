package session

import "strings"

// Prefix is the user's tmux prefix, written long ("Ctrl-b") and short
// ("C-b"). It reads the server's setting and never changes it.
func (t Tmux) Prefix() (long, short string) {
	out, err := t.run("show-options", "-gv", "prefix")
	short = strings.TrimSpace(out)
	if err != nil || short == "" || short == "None" {
		short = "C-b"
	}
	long = strings.Replace(short, "C-", "Ctrl-", 1)
	long = strings.Replace(long, "M-", "Alt-", 1)
	return long, short
}

// Look is the options that make a cadrei session easy to use (6.1, 6.2):
// the mouse, a status bar with the keys (shorter under 60 columns), and
// history. The bar is the brand's green with ink text, and its left side
// is the crew's name on a clay block, then rest (a team's "dev on my-app";
// "" for the orchestrator, whose window list already says so). Plain
// (NO_COLOR) leaves the colors out and keeps the name bold.
func (t Tmux) Look(crew, rest string, plain bool) (sessionOptions, windowOptions []Option) {
	long, short := t.Prefix()
	left, style := " #[bold,bg=#A9502F,fg=#FAF8F4] "+crew+" #[default] ", "bg=#3D9F60,fg=#1F1C19"
	if rest != "" {
		left += rest + " "
	}
	if plain {
		left, style = " #[bold]"+crew+"#[nobold] ", "bg=default,fg=default"
		if rest != "" {
			left += "· " + rest + " "
		}
	}
	right := "#{?#{e|<:#{client_width},60}," + short + " s · " + short + " d," + long + " s switch · " + long + " d leave} "
	sessionOptions = []Option{
		{"mouse", "on"}, {"status", "on"}, {"status-position", "bottom"}, {"status-style", style},
		{"status-left", left}, {"status-left-length", "40"}, {"status-right", right}, {"status-right-length", "60"},
		{"history-limit", "50000"},
	}
	windowOptions = []Option{
		{"window-status-format", " #W "}, {"window-status-current-format", " #[bold]#W#[nobold] "},
		{"monitor-bell", "on"}, {"aggressive-resize", "on"},
	}
	return sessionOptions, windowOptions
}

// StartServer starts the server, which reads the user's tmux.conf, when it
// isn't running.
func (t Tmux) StartServer() { t.run("start-server") }
