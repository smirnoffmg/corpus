# Reference manuals

Besides books and notes, corpus indexes reference manuals: the built HTML of a
Sphinx or docutils documentation site, such as scikit-learn or NLTK. They are
the `docs` kind — `kind=docs` on `/search`, "manuals" in the UI.

## Layout

`DOCS_DIR` in `.env` holds one directory per manual, and the directory's name is
the manual's name in every citation:

```
$DOCS_DIR/
  scikit-learn/   index.html, modules/svm.html, …
  nltk/           index.html, howto/tokenize.html, api/…, book/…
```

A page is cited as `scikit-learn · 1.4. Support Vector Machines`, with the
heading path of the section — `1.4. Support Vector Machines > 1.4.1.
Classification` — as its locator, and the section's id as its anchor, so a hit
links straight to the place in the original page.

Without `DOCS_DIR` compose mounts an empty directory and the kind simply has no
sources.

## Getting the manuals

Both ship as ready-built HTML; nothing needs Sphinx.

- **scikit-learn** — the documentation ZIP for the installed version, linked
  from <https://scikit-learn.org/dev/versions.html>. The link labelled
  *stable* was a 404 when this was written; the numbered one works:

  ```sh
  curl -L -o sklearn-docs.zip \
    https://github.com/scikit-learn/scikit-learn.github.io/raw/refs/heads/main/1.9/_downloads/scikit-learn-docs.zip
  unzip -q sklearn-docs.zip -d "$DOCS_DIR/scikit-learn"
  ```

  The archive has no top-level directory, so `index.html` lands directly in
  `$DOCS_DIR/scikit-learn/`.
- **NLTK** — the built nltk.org site is the repository
  `nltk/nltk.github.com`:

  ```sh
  git clone --depth 1 https://github.com/nltk/nltk.github.com "$DOCS_DIR/nltk"
  rm -rf "$DOCS_DIR/nltk/.git" "$DOCS_DIR/nltk/book-jp" "$DOCS_DIR/nltk/book_1ed"
  ```

  The Japanese translation would be detected as English and stemmed as
  nonsense, and the first edition of the book duplicates the second.

## Uploading

A manual can also be uploaded as a ZIP of its built HTML — from the library
page of the UI, or with curl:

```sh
curl -F file=@scikit-learn-docs.zip 'http://localhost:8080/upload?kind=docs&manual=scikit-learn'
```

`manual` names the directory; without it the name comes from the ZIP's file
name. When every entry of the archive sits in one top-level directory, that
directory is stripped, so a ZIP of a site's folder and a ZIP of its contents
unpack the same way. An archive is refused if an entry points outside the
manual or is a link, if it holds no HTML, if a manual of that name exists, or if
it unpacks to more than 100,000 entries or 4 GB. The indexer starts on it at
once; `GET /sources?prefix=scikit-learn/` shows each page's progress.

## What is read, and what is not

Only the content area of a page is indexed — `<article>`, else `role="main"`,
`<main>`, `div.body`, `<body>` — because a manual's sidebars and footers repeat
on every one of its thousands of pages and would match any query naming the
project. Navigation, header links (`¶`), `[source]` links, toctrees, docutils
build messages and the rich output of gallery examples — estimator diagrams and
data frames, which took one example page to 248 chunks — are dropped; code blocks are kept as fenced code and never cut in half.

A section is one chunk; a long one is cut at paragraph breaks with the same
sizes as a note section (`--docs-split-above`, `--docs-split-target`).

Sphinx build by-products are not walked: `_static`, `_sources` (the reST
sources, duplicating the pages), `_modules` (highlighted source code, which the
API pages already document), `_images`, `_downloads`, and the generated
`genindex.html`, `py-modindex.html` and `search.html`.

## The embedding backlog

A full manual is tens of thousands of chunks. Full-text search covers them as
soon as a pass has stored them; the vector leg only once they are embedded, and
the queue drains one source at a time in batches of 16. Watch it in
`GET /status` or the indexer log (`embedding progress`), and see
[search evaluation](search-evaluation.md) on why figures taken while the queue
drains are not comparable.
