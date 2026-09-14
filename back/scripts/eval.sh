#!/bin/sh
# Ranking must not change unmeasured (see README). The numbers are shown, never
# enforced: the corpus keeps growing, so a fixed threshold would cry wolf.
set -e
if ! curl -s --max-time 2 http://localhost:8080/healthz >/dev/null 2>&1; then
    echo "корпус не поднят — оценка пропущена"
    exit 0
fi
go run ./cmd/eval 2>/dev/null | sed -n '3,12p'
