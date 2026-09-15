# Citing sources in papers

Every book and manual in the library has a bibliographic description: a
CSL-JSON record with a citation key such as `kleppmann2017`. From it the
reader formats the passage for a paper, and the library exports the whole
bibliography for LaTeX and Pandoc. Notes have no description; they are not
cited in papers.

## Where descriptions come from

- **Drafts, offline.** After each pass the indexer drafts a description for
  every source that has none:
  - a book gets the title without its file name's shelf tags, the PDF's Author
    field when it reads as people's names, an ISBN from its opening or closing
    pages (check digit verified), and a DOI from its opening pages only — its
    closing pages are references to other works;
  - a manual gets its site title and version from `<title>`, its address from
    `<link rel="canonical">` or the site's `CNAME`, its publisher from the
    copyright line, and the date the copy was taken as the date accessed.
- **Lookup, online.** The description form fills a record from a DOI (doi.org,
  which answers in CSL-JSON) or an ISBN (Open Library, thin on Russian books).
  What would change is shown first and applied only on confirmation. Google
  Books was left out: its anonymous quota is zero.
- **By hand.** Library → the Описание column → the form. «Сохранить как
  проверенное» marks a description as checked; the indexer never overwrites
  one, and a draft never replaces anything.

## What survives what

Descriptions are the one thing in the database typed by hand, so they are
filed by what the source *is* — a book's content hash, `manual:<name>` for a
manual — without a foreign key. Renaming, moving, pruning or re-uploading a
file leaves its description in place. A draft's citation key follows its
record; once the description is checked the key is fixed, since papers cite it.

**Their system of record is a file, not the database.** Every change is written
to `bibliography/references.json` in the library directory — pretty-printed,
sorted by citation key, so a change reads as a diff — with added styles beside
it in `bibliography/styles/*.csl`. The indexer applies the file on every pass:
a description the table lacks, or holds in an older version, is taken from the
file, citation key and time included. So a lost database volume costs nothing
typed by hand, and the file can be edited, versioned or restored from a backup.
Of a description changed on both sides, the newer wins; removing one from the
file does not remove it from the table.

## Citing a passage

The reader shows, in the chosen style:

- the bibliography entry;
- the in-text reference with the page, e.g. `[1, с. 189]` or
  `(Клеппман, 2018, p. 189)`. The page is the one *printed* in the book,
  never the PDF's own count; when the printed number was not recognised there
  is no page, and it has to be added by hand;
- `\autocite[189]{kleppmann2018}` for BibLaTeX and `[@kleppmann2018, p. 189]`
  for Pandoc.

A passage from a manual is cited as a web page of that manual, at its section.

Formatting is citeproc-js — Zotero's engine — with the styles and locales
bundled into the UI, so it works offline.

## Styles

- **ГОСТ Р 7.0.100–2018** — `front/src/csl/gost-r-7-0-100-2018.csl`, written
  for corpus. The official CSL repository has only the older 7.0.5–2008, and
  the community 7.0.100 style dropped the full stop after the statement of
  responsibility, the spaces around `:` and `//` before the site of a web page.
  It covers books, articles, chapters and conference papers, theses, reports
  and web pages, and is checked by a test against the standard's book form.
  Not covered: omitting the heading for four or more authors (CSL cannot count
  names), and the genitive in «под редакцией».
- **APA 7** and **IEEE** — from the official CSL repository, unchanged.
- **A journal's style** — Library → Стили цитирования: by its file name in the
  [CSL repository](https://github.com/citation-style-language/styles) (a
  dependent style fetches its parent), or as a `.csl` file. Added styles are
  kept in the database and work offline.

## Export

- `/bibliography/export?format=biblatex` — `corpus.bib`, BibLaTeX rather than
  BibTeX: it has `location`, `pagetotal`, `urldate` and `langid`, which GOST
  needs and biblatex-gost reads.
- `/bibliography/export?format=csl-json` — `corpus.json`, for Pandoc
  (`--citeproc --bibliography corpus.json`), Zotero and Obsidian plugins.
