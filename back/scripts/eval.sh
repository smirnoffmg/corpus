#!/bin/sh
# Ranking must not change unmeasured (see docs/search-evaluation.md). The numbers are shown, never
# enforced: the corpus keeps growing, so a fixed threshold would cry wolf.
set -e
if ! curl -s --max-time 2 http://localhost:8080/healthz >/dev/null 2>&1; then
    echo "корпус не поднят — оценка пропущена"
    exit 0
fi
# The judged set is private and lives outside the repository; a clone without
# it has nothing to measure against, which is not a failure.
queries="${CORPUS_EVAL_QUERIES:-$HOME/.corpus/eval/queries.json}"
if [ ! -f "$queries" ]; then
    echo "набора запросов нет ($queries) — оценка пропущена; публичный пример: go run ./cmd/eval -queries eval/example.json"
    exit 0
fi
go run ./cmd/eval -queries "$queries" 2>/dev/null | sed -n '3,12p'
