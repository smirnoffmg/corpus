#!/bin/sh
# The store tests need a database and skip without one; give them the compose
# instance when it is listening, so they actually run rather than silently pass.
set -e
if nc -z localhost 5433 2>/dev/null; then
    TEST_DATABASE_URL="postgres://corpus:corpus@localhost:5433/corpus" exec go test -race ./...
fi
echo "БД не слушает 5433 — тесты хранилища пропущены"
exec go test -race ./...
