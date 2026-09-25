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

**`back/eval/example.json` is a public set** of 52 queries — 24 of them
paraphrases, 14 in Russian — on the scikit-learn and NLTK manuals (see [manuals](manuals.md) for downloading them), so a fresh
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
  "SELECT count(*) FILTER (WHERE e.embedding IS NULL) AS queued, max(e.embedded_at)
   FROM chunks c LEFT JOIN embeddings e ON e.hash = c.embed_hash"
```

`embeddings.embedded_at` is when each vector was written, so a past run can be placed
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

**A difference is a difference only query by query.** On a few dozen queries a
mean moves on a handful of them: exact-query MRR 0.938 → 1.000 on the private
set was four queries moving up one place, p = 0.125 by a sign test — not
evidence of anything by itself. So a change is compared against a baseline
query by query:

```sh
go run ./cmd/eval -queries eval/example.json -set ef_search=200 -against default
```

which prints wins, losses and ties per mode, the mean difference, and a
two-sided sign test. On sets this size, treat p above 0.05 as no difference,
and a change worth making needs a reason beyond the judged set.

**HNSW recall was measured, and ef_search raised to 200.** The index is
approximate, and what it loses happens before ranking starts. `-recall`
compares each query's 20 nearest chunks from the index with an exact scan:

| ef_search               | recall@20, private (31) | recall@20, public (52) | full recall, public |
| ----------------------- | ----------------------- | ---------------------- | ------------------- |
| 40 (pgvector's default) | 0.831                   | 0.789                  | 19/52               |
| 100                     | 0.900                   | 0.892                  | 29/52               |
| **200**                 | **0.947**               | **0.959**              | **35/52**           |
| 400                     | 0.968                   | 0.982                  | 42/52               |

At 40 some queries got back almost none of their true neighbours. Latency did
not move — about 340 ms a vector search at 40, 200 and 400 alike, most of it
the query's embedding. On the judged answers the effect is within noise and
not even of one sign: on the public set vector search won 5 queries and lost 2
(p = 0.45), hybrid 2 and 2; on the private set hybrid lost 2 exact queries and
won none (p = 0.5), taking exact-query MRR from 1.000 back to 0.938. So the
change rests on recall at no cost, not on the judged sets — and a larger set is
what would settle it. 400 buys nothing more measurable (0 wins, 1 loss against
200).

The flag had been doing nothing. `mcpd --ef-search` was stored and never
applied to a connection, so every search ran at 40 whatever it said;
`/status` now reports `hnsw_ef_search` as the connections actually have it.

**Fusion depth was measured and left alone.** Asking each leg for 100 hits
instead of twice the page before RRF changed nothing: private set all ties,
public 3 wins and 2 losses with ΔMRR +0.001. RRF's constant was not tuned
either — Cormack et al. found k = 60 near-optimal and the choice not critical.

**Note sections are measured in characters, not bytes.** The splitter compared
`len()` against its sizes, and a Cyrillic letter is two bytes, so a Russian
section was cut at ~800 characters and an English one at 1600 (issue #2). The
0.459 → 0.478 that chose 1600 was measured that way. Counting characters made
the vault's Russian parts twice as long and left 670 fewer chunks; on the 15
vault queries of the private set vector MRR went 0.612 → 0.608 (3 wins, 2
losses, p = 1.0), hybrid 0.606 → 0.674 (4 wins, 1 loss, p = 0.38) and full text
0.300 → 0.400 (2 wins, no losses). No loss, and the whole private hybrid went
0.516 → 0.549; the public set, English manuals, did not move.

**A cross-encoder reranks when asked, 20 candidates of 1500 characters.**
Reranking the fused list with bge-reranker-v2-m3 was measured first as a
script over both sets, 83 queries, before any code:

| candidates × characters | MRR 0.526 → | wins / losses | p | median / p95 |
| --- | --- | --- | --- | --- |
| 50 × 4000 | 0.612 | 29 / 12 | 0.012 | 3.3 s / 6.6 s |
| 50 × 1500 | 0.610 | 29 / 14 | 0.032 | 3.0 s / 3.7 s |
| 20 × 4000 | 0.600 | 27 / 12 | 0.024 | 1.3 s / 2.4 s |
| 20 × 1500 | 0.593 | 28 / 13 | 0.028 | 1.2 s / 1.5 s |

The gain is real in every row and largest for paraphrases (MRR 0.47 → 0.57 at
50 × 4000) and books (6 wins, no losses). Latency is what separates them, and
the smallest keeps most of the gain, so that is what `rerank=1` does. Built in
and measured with `-set rerank=1 -against default`, hybrid: private 0.549 →
0.631 (8 wins, 3 losses), public 0.512 → 0.570 (21 wins, 10 losses); together
29 / 13, p = 0.020. Reranking took 1.16 s median and 1.54 s at p95 over 252
searches. It stays opt-in: a second a search is fine for an agent asking a
question in its own words and too much for typing into the UI.

**The vector cache migration moved the numbers, not the ranking.** Moving
vectors into their own table rebuilt the HNSW index in one pass, where it had
grown through many deletions; private vector MRR went 0.442 → 0.504 with
identical vectors, consistent with the old graph's lower recall. Figures from
before 2026-09-15 are not comparable with later ones.

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

| norm                            | fts MRR       | found@10 |
| ------------------------------- | ------------- | -------- |
| **0** (default)                 | **0.419**     | **42%**  |
| 1 — divide by 1+log(length)     | 0.403         | 42%      |
| 2 — divide by length            | 0.398         | 42%      |
| 4 — divide by extent distance   | 0.324         | 39%      |
| 8 / 16 — divide by unique words | 0.398 / 0.403 | 42%      |
| 32 — divide by itself+1         | 0.419         | 42%      |

Ignoring length wins. Book pages are near enough the same size for it not to
matter, and notes are short enough that penalising length throws away the long
ones that actually explain something. The 32 row is the harness checking itself:
dividing every rank by itself+1 is monotonic, so the order — and every metric —
must come out identical, and it does.
