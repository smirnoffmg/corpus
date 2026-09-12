#!/bin/sh
# Per-package statement coverage gate. Package level, not total: a repository
# average hides a package at 30% behind two at 100%, and 30% is where the bugs
# are. cmd/ is exempt — those are composition roots whose bodies are flag
# parsing and wiring, and a test that covers them tests the test.
set -e
min=${COVERAGE_MIN:-80}

# The store tests start a container, so without Docker the number would say
# more about the laptop than about the tests.
if ! docker info >/dev/null 2>&1; then
    echo "Docker не запущен — покрытие не измеряется, тесты идут в -short"
    exec go test -race -short ./...
fi

out=$(go test -race -cover ./...)
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
