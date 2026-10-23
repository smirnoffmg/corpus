# Is it used

The [search evaluation](search-evaluation.md) says whether the right page comes
first. It does not say whether a search reaches the reader: whether a book
copied in is searchable yet, whether the agents' queries find anything, whether
a quote ends up in a note. Three things answer that, cheapest first.

## Files not indexed yet

`GET /status` lists under `unindexed` every book and publication on disk that
has no source in the index, and the system page shows them. A file copied into
the library rather than uploaded sends no reindex request, so it waits for the
next pass; a file still listed after a pass is one the pass could not read.

The pass interval is counted by the wall clock. It used to be a timer, and a
timer runs on the monotonic clock, which stands still in Docker's VM while the
Mac sleeps: twelve minutes asleep delayed a pass by twelve minutes, with four
new books already on disk and nothing to say so.

## The search journal

Every search and every full read is written to `search_log` and `read_log`,
with where it came from: `mcp` is the agents, `http` the UI and `cmd/eval`.
`/status` sums up the last week per origin: searches, empty ones, failed ones,
and those with a hit that was then read in full within half an hour — the step
before a quote. `GET /searches` lists the journal, the latest first:

```sh
curl -s 'localhost:8080/searches?origin=mcp&empty=1&limit=20'
```

An empty `fts` search worded as a sentence is the failure to look for: `fts`
joins words with AND, and an agent reads an empty list as the library holding
nothing on the subject.

The journal keeps the query text. It lives in the database alone, which the
library backup does not include, and for 180 days: each write drops the rows of
its table that have aged out, so the journal drains as fast as it fills — the
Steady State pattern (*Release It!*, p. 124), without a job of its own.

## Citations in the vault

What the library is for is a quote in a note, so the closest measure of it the
vault gives is how many of its citations name a book or paper the library
holds. `cmd/citecount` counts them week by week, reading the vault as it stood
at the end of each week from its git history:

```sh
cd back
go run ./cmd/citecount -weeks 12      # VAULT_DIR, and the server for the titles
```

A citation is an italic title followed by a locator, the way
[citing sources](citing.md) writes it — `*Title*, с. 189 (PDF 203)`, `PDF 18`,
or a chapter for an e-book. It counts as the library's when the cited title
begins a library title and ends on a word, since notes cite a book by its
short title. Templates and dot directories are left out: their example
citations cite nothing. A citation in bold, or in a chat answer rather than a
note, is not counted.
