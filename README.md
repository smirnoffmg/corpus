# corpus

Full-text search over the PDF library in `~/Desktop/books`, the Obsidian vault
and reference manuals such as scikit-learn and NLTK. Exposed as an MCP server and as plain HTTP.

The unit of a hit is a **citable location** — a page for a book, a heading path
for a note — because the point is to quote a source in a note, not to learn that
some book mentions the word. Book pages are cited by the number *printed on the
page*, which is what a reader of any copy can follow; the PDF page follows in
brackets when the two differ, since that is what opens this particular file.
The locator is composed on the way out, from the heading, the page and the
printed number — storing the rendered string once meant re-extracting every book
to change how a citation looks, which happened twice before it was fixed.
Where the printed number cannot be read the locator says `PDF 256` rather than
passing a PDF page off as a page of the book — 40 of the 52 books here have
readable numbering, and the rest are papers and slides that print none.

Granularity is split on purpose: the text index answers per page, while the
vector sees a window that carries ~500 characters of the neighbouring pages.
Two thirds of the pages in this corpus end mid-sentence, so a page on its own is
routinely half a thought — Manning's *Introduction to Information Retrieval*
(printed p. 22) puts it as a precision/recall tradeoff and argues that a system
should offer choices of granularity rather than pick one.

**One chunk, one thought — not "smaller is better".** Cutting long note sections
into parts lifted vector MRR from 0.459 to 0.478, because a section often argued
six things under one heading. Applying the same to book pages was measured too,
and it lost: hybrid found@10 fell from 74% to 68% across the judged set. A page
is already one thought — the author laid it out that way — and half a page is
half an argument. So book pages stay whole and note sections are cut at 1600
characters, and those are the defaults in code, with the numbers beside them.

## Run

```sh
cp .env.example .env   # point BOOKS_DIR / VAULT_DIR at the real directories
docker compose up -d --build
docker compose logs -f indexer
```

The indexer re-scans every 15 minutes and skips files whose SHA-256 is unchanged,
so the vault stays current while obsidian-git commits into it.

Contents pages, subject indexes and near-empty dividers are dropped too: they
match any query that names a term the book covers — which is every query — and
answer none. A file that yields no text is not a source either: scans without an
OCR layer and empty notes are dropped rather than kept as rows nothing can ever
return. Templater sources under `99 - templates/` are code, not knowledge, and
are not walked. Re-parsing
them each pass costs nothing — there is no text to pull, so poppler returns in
milliseconds — and the drop is reported only when it actually removes something.

## Search

```sh
curl -s 'http://localhost:8080/search?q=агрегат+DDD&kind=book&limit=5' | jq
```

As an MCP server:

```sh
claude mcp add --transport http corpus http://localhost:8080/mcp
```

`kind` is `book`, `vault`, `docs` (reference manuals), or empty for all. `mode` picks the retrieval method:

| mode               | finds                         | good for                        |
| ------------------ | ----------------------------- | ------------------------------- |
| `fts`              | the words you typed, stemmed  | exact terms, names, quotes      |
| `vector`           | passages about the same thing | a topic you cannot name exactly |
| `hybrid` (default) | both lists fused by rank      | most questions                  |

The `fts` query goes through `websearch_to_tsquery`, so `"точная фраза"` and
`-исключение` work — and it joins your words with AND, so a question phrased as a
sentence usually returns nothing while its one real term returns plenty.

**Full-text search does not cross languages.** Each chunk is stemmed with the
Postgres configuration for its own language, and a Russian query is stemmed as
Russian: it can never match an English page, because stemming is not translation.
Half this corpus is English, so that is half the library invisible to `fts` for a
Russian question. The vector leg is what bridges it — "как ограничить число
одновременно работающих горутин" returns nothing in `fts` and Go in Practice
p. 63 in `vector`. This is why `hybrid` is the default. `per_source=N` keeps one book or note from filling the page
with N+1 paragraphs of the same chapter; it is off by default, because the
question "where is this discussed" and the question "does a note about this
already exist" want different answers. `GET /compare?q=…` runs all three and returns them side by
side.

## Adding to the library

```sh
curl -F file=@book.pdf 'http://localhost:8080/upload?kind=book'
curl -F file=@site.zip 'http://localhost:8080/upload?kind=docs&manual=nltk'
curl -s 'http://localhost:8080/sources?kind=book' | jq
```

A PDF is saved to `uploads/` in the book library, a ZIP of a manual's built HTML
is unpacked into the manuals directory, and the indexer starts on it at once.
`/sources` lists every source with how many of its chunks are stored, embedded
and quarantined; `kind` and `prefix` narrow it. Uploads are limited to 300 MB
(`mcpd --upload-max`).

## Naming books

A citation shows the source's title, and a title comes from the filename, so the
filenames in the library are the names. Rename freely: a source is matched by the
hash of its contents, so a renamed file is recognised as the book it already was
and keeps its chunks and their embeddings — renaming costs nothing.

Where a filename is opaque — `1476.pdf`, `ТЧА.pdf` — the title is recognised from
the document instead: PDF metadata first, then the cover page, with the obvious
traps rejected (authoring-tool artifacts, the author line, letter-spaced series
headings, mojibake from a mis-decoded font). That is right about three times in
four, which is why it never overrules a filename that already reads as a title.
The remaining quarter is fixed by renaming the file.

## Documentation

- [Architecture](docs/architecture.md) — embeddings, Postgres, containers, design notes
- [Search evaluation](docs/search-evaluation.md) — the judged set and what it decided
- [Reference manuals](docs/manuals.md) — indexing scikit-learn, NLTK and other Sphinx sites
- [Development](docs/development.md) — pre-commit checks, lint, races
