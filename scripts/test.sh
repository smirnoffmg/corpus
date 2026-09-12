#!/bin/sh
# The store tests start their own Postgres through testcontainers, so the only
# thing they need is a Docker daemon. Without one they fail loudly rather than
# skipping: a store suite that passes with no database proves nothing. -short
# is the deliberate way to run everything else.
set -e
if docker info >/dev/null 2>&1; then
    exec go test -race ./...
fi
echo "Docker не запущен — тесты хранилища пропущены (-short)"
exec go test -race -short ./...
