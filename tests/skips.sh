#!/bin/sh
# Fails when a test skipped that doesn't skip by design: a test that skips
# where it shouldn't tests nothing, and without -v nobody sees it. Reads
# `go test -v` output on stdin. Pass "race" for a run under -race.
set -eu
race=${1:-}

# expected reports whether a test may skip in this run.
expected() {
	case "$1" in
	# Helpers that other tests run as programs, and a test you opt into.
	TestHelperCadrei | TestHelperHoldLock | TestMoveOntoACoarseDisk) return 0 ;;
	# Runs only inside a Claude Code shell, which CI isn't.
	TestSessionVarsCoverAClaudeCodeShell) return 0 ;;
	# Real timings: they skip under the race detector, and CI's timing
	# step runs them without it.
	TestTheStreamEndsWhenItsReaderLeaves | TestTheStreamIdlesCheaply | TestTheStreamKeepsItsTimesForReal | TestHookTeamStatusBudget)
		[ "$race" = race ] ;;
	# macOS only: Homebrew's install hint and admin group, and a disk
	# that ignores case.
	TestFirstImpressions/no_Homebrew | TestOwnedAcceptsHomebrewsPrefix | TestResolveThroughAnotherSpelling)
		[ "$(uname -s)" != Darwin ] ;;
	# Linux only: CI's Ubuntu has tmux 3.4, whose server ends on a session
	# name with U+202E in it.
	TestStopAndAttachShowSessionsPrintable/unmarked)
		[ "$(uname -s)" = Linux ] ;;
	*) return 1 ;;
	esac
}

bad=0
skipped=$(sed -n 's/^ *--- SKIP: \([^ ]*\) .*/\1/p')
while read -r name; do
	if [ -n "$name" ] && ! expected "$name"; then
		echo "Unexpected skip: $name. Fix why it skips, or add it to tests/skips.sh with the reason."
		bad=1
	fi
done <<EOF
$skipped
EOF
exit "$bad"
