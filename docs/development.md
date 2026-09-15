# Development

Checks, linting and race testing. The Go module lives in `back/`, the web UI in `front/`.

## Checks before a commit

```sh
pre-commit install          # once per clone
pre-commit run --all-files  # the whole repository, not just what is staged
```

The Go module lives in `back/`, and every `go` and `golangci-lint` command
below runs from there. The four everyday commands have a root `Makefile`: `make up`, `make down`, `make
test`, `make lint` — and `make cover` for the gate below. `lint` formats before
it checks, because `golangci-lint run` reports formatting too and would
otherwise complain about what `golangci-lint fmt` was about to fix.

A commit is refused if it is unformatted, fails the linters, fails the tests —
run with `-race` — or leaves any package under 80% statement coverage. The
retrieval evaluation prints but never blocks: the corpus keeps growing, so a
fixed threshold would cry wolf; the numbers are there to be looked at when a
change touches ranking. `--no-verify` skips the lot, deliberately.

The coverage gate is per package rather than per repository, because an average
hides a package at 30% behind two at 100%, and 30% is where the bugs are. `back/cmd/`
is exempt: those are composition roots whose bodies are flag parsing and wiring,
and a test that covers them tests the test. `COVERAGE_MIN` moves the line.

Tests and coverage are one hook, so a single run gives both the race detector
and the numbers. The store tests start their own Postgres — the same
`pgvector/pgvector` image compose runs, since the schema needs the extension —
so the only thing they ask of the machine is a Docker daemon. Without one the
script says so and falls back to `-short` rather than reporting a figure that
describes the laptop. They used to take a database through `TEST_DATABASE_URL`
and skip when it was unset, which meant the everyday result of `go test ./...`
was a green run in which the store was never touched.

Formatting is `golangci-lint fmt`, not a separate `gofmt` hook: it applies the
`formatters` section of `.golangci.yml` — gofmt *and* goimports with this
module's local prefix — and `golangci-lint run` reports the same violations
without rewriting, so a commit forced through with `--no-verify` still trips the
linter.

The Go tools run as `repo: local` with `language: system` — the same
`golangci-lint` binary used by hand, not a copy the hook installs for itself.
That matters here: its bundled staticcheck is sensitive to the Go version, and
two versions disagreeing about the same code is a worse problem than the one the
hook solves.

## Lint

```sh
golangci-lint run
```

`.golangci.yml` pins the rule set. Tests live in `foo_test` packages and speak
only the public API, which `testpackage` enforces; the one exception is
`folio_internal_test.go`, where the logic under test — reading a page number out
of a running head — has no public surface worth exposing for it, and the
`_internal_test.go` name is what marks that deliberate choice.

## Races

```sh
go test -race ./...
```

The detector "will only find races that are contained in code that is exercised"
(Cox-Buday, *Concurrency in Go*, printed p. 214), so the extraction pool — the one
place here where goroutines share state — is driven by a test of its own in
`back/internal/index`, over a temporary directory of notes with the workers turned up.
Swapping its atomic counter for a plain one makes that test report a race and
lose updates, which is how one knows the test is worth having.

For real-world load, the way the Go team recommends, a `-race` build of the
indexer can be pointed at a scratch database and a real slice of the library.

```sh
go build -race -o /tmp/indexer-race ./cmd/indexer
DATABASE_URL=postgres://corpus:corpus@localhost:5433/corpus_race \
  /tmp/indexer-race --books <dir> --vault <dir> \
  --ollama http://127.0.0.1:1 --interval 0
```

The dead ollama address is deliberate: the run then also shows the embedding pass
failing without taking the text index down with it. Last run: 8 books, 226 notes,
8182 chunks, no races.

## The web UI

```sh
cd front
npm ci
npm run dev            # http://localhost:5173, /api proxied to mcpd on :8080
npm run test:coverage  # vitest; fails under 80% statements, like the Go gate
npm run lint && npx tsc -b
```

`make front-test` and `make front-lint` run the same, and pre-commit runs both
when a file under `front/` changes. `front/.npmrc` sets `legacy-peer-deps`:
npm 10.9 crashes resolving vitest's optional peers without it, and the lockfile
was written with it, so every install needs it too.

In development the sources are not served: "open the original" links point at
`http://localhost:8082`, where the compose stack's nginx serves PDFs and manual
pages on an origin of their own (see [architecture](architecture.md)). With
the stack running they work from the dev server too; `VITE_SOURCES_PORT`
changes the port.
