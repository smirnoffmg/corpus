# Search evaluation

How retrieval quality is measured, and the decisions the numbers settled.

## Evaluation

A judged set is a list of queries, each with the pages or notes that answer
it. Ground truth is located by exact phrase in the text, never by this system's
own ranking — a set built from the tool's own output would only measure its
agreement with itself.

**The set is private, and lives outside the repository.** Its answers are notes
of one person's vault and pages of the books in one library, so it says what is
in them, and nobody else could reproduce its numbers anyway. `cmd/eval` reads
`~/.corpus/eval/queries.json` by default; the commit hook skips the evaluation
when the file is absent. Every figure quoted below was measured on that set and
that corpus.

**`back/eval/example.json` is a public set** of 14 queries on the scikit-learn
and NLTK manuals (see [manuals](manuals.md) for downloading them), so a fresh
clone has something to measure, and the manuals are measured at all:

```sh
cd back
go run ./cmd/eval -v                               # the private set
go run ./cmd/eval -queries eval/example.json -v    # the public one
```

Paths in a judgement are matched as substrings of a hit's path, pages against
the PDF page; a judgement without pages matches any chunk of the source, which
is how notes and manual pages are judged.

The queries are split between exact terminology and paraphrases — questions in
the reader's own words, sometimes in the other language from the book — because
that is the split that separates the retrieval modes. Books and the vault get
roughly half each: a set weighted to one of them cannot see a change to the
other, which is how a chunking change once measured as no change at all.

The set is small and I wrote it alone, so treat it as a regression guard for
changes to ranking, not as an absolute measure of quality. Numbers are comparable
only across runs on the same set and the same corpus — adding queries or books
moves them without anything in the code changing.

**The same corpus also means a drained embedding queue.** Repeated runs are
exactly reproducible: four in a row, one of them against a rebuilt binary,
matched to the digit. Across a working day they did not. `vector` found@10 read
61% and later 65%, `hybrid` 71% and later 74%, with no change to ranking and
with `fts` identical to the digit throughout — the text index answers as soon as
a chunk is stored, while the vector leg only sees chunks that already have an
embedding, and the indexer fills those in behind it. So a figure taken while
chunks are still queued measures how far the queue got, not how well retrieval
works. Check first:

```sh
docker compose exec db psql -U corpus -d corpus -c \
  "SELECT count(*) FILTER (WHERE embedding IS NULL) AS queued, max(embedded_at) FROM chunks"
```

`chunks.embedded_at` is when each vector was written, so a past run can be placed
against the state of the corpus rather than guessed at — which it had to be the
first time this happened, since the column did not exist yet. Rows embedded
before it was added stay NULL: that time is genuinely unknown, and a row stamped
with the migration's `now()` would read as a measurement.

A query may be repaired when it is *ambiguous* — "split the data into groups and
take from each in proportion" describes SQL grouping as well as stratified
sampling, and the corpus has both. A query that is merely *hard* stays as it is;
rewriting those until they pass turns the set blind.

Nothing about ranking — length normalisation, `hnsw.ef`, the RRF constant —
should be changed without running this before and after.

**A title that matches the query is worth 0.3 of rank.** A note called
"Кросс-энтропия" and a note that merely mentions the term score identically on
text rank alone, and which one came first was decided by the tiebreaker. Adding
a constant when the source title matches takes exact-query MRR from 0.938 to
1.000 and P@5 from 0.300 to 0.400, with the paraphrased half unmoved. The effect
saturates at 0.3, so that is the value. `title_boost=0` on `/search` turns it off,
which is how the alternative was measured.

**Length normalisation was measured and left alone.** `ts_rank_cd` takes a bit
mask for document length; `go run ./cmd/eval -norm N` sweeps it, and `norm=N` on
`/search` tries one without a restart:

| norm | fts MRR | found@10 |
| --- | --- | --- |
| **0** (default) | **0.419** | **42%** |
| 1 — divide by 1+log(length) | 0.403 | 42% |
| 2 — divide by length | 0.398 | 42% |
| 4 — divide by extent distance | 0.324 | 39% |
| 8 / 16 — divide by unique words | 0.398 / 0.403 | 42% |
| 32 — divide by itself+1 | 0.419 | 42% |

Ignoring length wins. Book pages are near enough the same size for it not to
matter, and notes are short enough that penalising length throws away the long
ones that actually explain something. The 32 row is the harness checking itself:
dividing every rank by itself+1 is monotonic, so the order — and every metric —
must come out identical, and it does.
