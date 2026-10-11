// Package cadrei holds the files the cadrei binary carries with it: the
// orchestrator skill, the member protocol and the template a new cadrei
// starts from. They are written out where they are needed (section O.2).
package cadrei

import "embed"

// Assets is the embedded skill, protocol, orchestrator text and template.
// The template is embedded with all: so its dot files (.gitignore,
// teams/.gitkeep) come along. orchestrator.md is the one source of the
// orchestrator's instructions, for its prompt and for the hook (K.3).
// mascot's .txt files are the mascot's half-block art (7.12) as the
// escape codes that draw it, one file for truecolor and one for 256
// colors, every row the same width, drawn from the brand's lead.pixels
// (kept beside them, not embedded) by go generate, with its transparent
// pixels left to the terminal's background.
//
//go:generate go run ./mascot/gen
//go:embed skills/cadrei/SKILL.md protocol.md orchestrator.md all:template mascot/*.txt
var Assets embed.FS
