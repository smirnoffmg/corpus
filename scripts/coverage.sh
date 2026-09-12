#!/bin/sh
# Per-package statement coverage gate. Package level, not total: a repository
# average hides a package at 30% behind two at 100%, and 30% is where the bugs
# are. cmd/ is exempt — those are composition roots whose bodies are flag
# parsing and wiring, and a test that covers them tests the test.
set -e
min=${COVERAGE_MIN:-80}

out=$(go test -cover ./...)
echo "$out"

echo "$out" | awk -v min="$min" '
	$4 == "coverage:" {
		pct = substr($5, 1, length($5) - 1) + 0
		if ($2 ~ /\/cmd\//) next
		if (pct < min) {
			printf "  %-45s %5.1f%% < %s%%\n", $2, pct, min
			bad++
		}
	}
	END {
		if (bad) {
			printf "\n%d package(s) below the %s%% gate\n", bad, min
			exit 1
		}
		printf "\nevery package is at or above %s%%\n", min
	}'
