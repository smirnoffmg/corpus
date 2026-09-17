# Publications

Besides books, notes and manuals, corpus indexes publications: papers, preprints
and reports, as PDFs. They are the `paper` kind — `kind=paper` on `/search`,
"статьи" in the UI.

A paper is a book in most respects — a PDF, a page per chunk, a citation by
page — and different in two that earn it a kind of its own:

- it is cited by DOI rather than ISBN, and usually prints no page numbers at
  all, so its locator is `PDF 4`;
- it carries a list of the works it cites, and that list is data. It is read out
  of the PDF into entries of its own, matched against what the library already
  holds, and answerable in both directions.

## Layout

`papers/` in the library (`LIBRARY_DIR`, by default `~/.corpus/data`), beside
`books/` and `manuals/`. Uploads land in `papers/uploads/`. An empty `papers/`
simply means the kind has no sources.

```sh
curl -F file=@mapreduce.pdf 'http://localhost:8080/upload?kind=paper'
```

In the UI the upload panel asks, for every PDF, whether it is a book or a
paper — the file itself does not say, and putting a paper among the books would
make it prune against the wrong shelf.

## The list of references

**It leaves the text index.** A bibliography matches every query that names a
term the paper cites anything about, and answers none of them — the same reason
contents pages and subject indexes are dropped (see the noise filter in
`back/internal/extract/noise.go`). Searching this corpus for *citation analysis*
before the feature existed returned five hits and all five were bibliography
pages of books. So the list itself does not become chunks. What the heading's
page holds above it — usually the end of the conclusion — stays, and so does
whatever follows the list, such as an appendix; pages are blanked rather than
removed, so every page keeps its number.

**It is read once per file, not once per pass.** The list is filed under the
paper's content hash, the same key its bibliographic description is filed under,
so renaming, moving or re-uploading the file keeps it, and a re-index that
rewrites every chunk does not touch it. A paper that turned out to have no list
gets a record saying so, or it would be read again every fifteen minutes.

**It is read offline.** Poppler, a heading, and regular expressions — nothing is
sent anywhere. The reader runs `pdftotext -raw` rather than `-layout`: on a
two-column paper the layout mode sets both columns side by side on one line, and
a reference list read that way comes out interleaved. The price is that raw mode
drops every indent, so a hanging indent — the usual shape of an unnumbered list —
is gone by the time the list is cut, and the cut has to go by what a line says
instead.

**It is read again when the reader improves.** Each list records the version of
the parser that read it (`extract.ReferenceParser`); raise it, and every paper
read by an older version is read again on the next pass. Enrichment from doi.org
is part of the stored list and is lost on such a re-read — run it again.

The first runs over 43 real papers in the library, on software engineering and
machine learning, are what shaped the rules below: the first read 1821 entries, and
after the fixes it found, 2200-odd. The unnumbered lists had come out as a single
entry each, author lists written family-name-first were cut at the first
initial, a zero-width space after a label hid the label, and what a paper prints
after its list — an appendix, the authors' biographies — was glued to the last
entry. The graph showed the rest: Sculley et al. 2015 appeared under two
fingerprints, one of them the "title" `Crespo, and D.`; five Elsevier papers
shared the DOI `10.1016/j`; and two papers matched themselves through an ISBN.

### What is recognised

- **The heading** — `References`, `Bibliography`, `Works Cited`, `Список
  литературы`, `Литература`, `Библиография`, `Источники` and a few more, as a
  line of its own, optionally numbered, and in small capitals, which a PDF gives
  back letter-spaced (`R EFERENCES`). The last one in the document wins, and
  the word inside a sentence is not one. The list ends at the next section —
  `Appendix`, `Приложение`, `Index`, `Об авторах` — or at the end.
- **The shape**, whichever of four explains the lines, in this order:
  - numbered entries (`[12]`, `12.`, `(12)`), when the first label is 1 — a
    volume number opening a continuation line is not entry twelve;
  - a hanging indent (an entry at the left margin, its continuations under
    it), in text that still has indents, such as a recognised scan;
  - an author–year list: a line opening with a name — `Alves, N.S.`,
    `Arisholm E`, `Breiman L (1996)`, `Marsden, Peter V., and`, `Клеппман, М.` —
    after a line that closed an entry. A line ending in a comma, an ampersand, a
    hyphen or mid-word is an author list that wraps, and a name on the next line
    continues it;
  - a blank line between entries.
- **The end**, at the next section — `Appendix`, `Приложение`, `Index`,
  `Об авторах` — and, after a line that closed an entry, at an appendix lettered
  without the word (`A Author contributions`, `G.3 Onboarding call`), a heading
  in capitals, or a biography (`Valentina Lenarduzzi is a postdoctoral…`).
- **Furniture**: zero-width spaces and soft hyphens, and headers and footers that
  recur on most pages of a long list, page number aside.
- **The fields** — DOI, arXiv id, ISBN with its check digit verified, URL, year,
  and the author and title segments. A word broken across lines is made whole
  again, and the run of bare page numbers a book's bibliography ends with — the
  pages the work is cited on, which belong to that book — is cut off.
  A title in quotes — IEEE, Chicago — is taken as the title, and what precedes
  it as the authors: IEEE puts only a comma between the two. A DOI broken
  across lines is joined back, and one cut short to its publisher's prefix
  (`10.1016/j`) is dropped rather than shared by every paper of that publisher.
  An initial written with a hyphen (`J.-F.`) is an initial.
  Where the year follows the authors — APA, Harvard, Springer, Elsevier — the
  year is where the author block ends, provided what precedes it reads as
  names; otherwise the entry is cut at its full stops, with initials kept
  whole.

### Primary studies

A systematic review prints a second list: the studies it reviewed, labelled
`[P1]`, `[PS1]` or `[SP1]`, or unnumbered in a multivocal review. It is read
like the references, kept as a list of its own (`list: primary`), numbered from
one, and left out of the text index with them. On the card it is a section of
its own, and a work a review included says so in the list of what cites it.

- **The heading** is what finds it — `PRIMARY STUDIES`, `Appendix B. The
  selected papers (Ps)`, `Appendix A: The Primary Studies (PSs)`, `Selected
  papers`, `Первичные исследования` — never the labels: `P1` also labels the
  rows of a table of products, and `S1` the statements of a survey.
- **The list has to read as citations.** The same words are a caption
  (`Primary Studies` over a figure of the selection steps) or the heading of a
  numbered list of inclusion criteria. So it is taken only when it is cut by
  labels, indents or names, holds three entries at least, and at least half of
  them carry a year; headings are tried from the last one back.
- **Each list ends where the other begins**, before or after it.

On the five reviews in the library every count agrees with the review's own
labels or tables: 21, 27, 44 and 198 studies, and 60 of the 62 a multivocal
review reports — two of its grey-literature entries open with a title rather
than a name and join the entry before them.

An entry whose first line opens with a title or an organisation is no longer
dropped when it comes before the first recognised one: that had been losing
the first reference of two papers as well.

### What is not

- Anything the shapes above do not explain: the section is then kept as one
  entry, to be read by a person rather than lost quietly.
- The container — journal or proceedings — is left to enrichment.
- Names are kept as the author segment reads, not split into people. A
  bibliography writes them in half a dozen orders, and a wrong split reads worse
  than none.
- A citation in the body (`[12]` in a paragraph) is not linked to entry 12.
- On a two-column page a few lines of the list can stay in the text index: the
  layout text puts the other column's references beside the conclusion above
  the heading, or beside the first line after the list, and a page whose cut
  cannot be placed is kept whole rather than lost.
- A recognised scan whose columns ran together (Naur 1985) yields its real
  entries and loses the lines the recognition mangled.

**The line as printed is the source of truth.** Everything parsed out of it is a
reading and can be wrong, so the entry travels with its own text, and the UI
shows that text rather than the parse.

## Matching against the library

Each pass points every entry that names a work the library holds at that work's
description. An identifier is a match: a DOI, an arXiv id, and an ISBN when the
description is a book's — an article's ISBN is its proceedings', and matching by
it made one paper of a volume every other. An entry never matches the paper it
is printed in. A title is a
match only when it is the same title in the same year, reduced the same way on
both sides — lower case, `ё` folded to `е`, punctuation dropped, spaces
collapsed. Anything softer is left unresolved rather than guessed:
"discrepancies can occur for many reasons, such as misspellings … 'Susan
Williams' may appear as 'Susan B. Williams' … 'Chen Li' and 'Li Chen' may or may
not turn out to be the same person" (Ullman, *Database Systems: The Complete
Book*, printed p. 1079) — and to that list this corpus adds transliteration:
«Клеппман, М.» against "Kleppmann, Martin".

**The file itself is the second, and in practice the main, source.** A paper
or a book prints its title at the top of its first page, so an entry whose
title stands in the first 600 reduced characters of a first page names that
file — with no description, no year and no network. Only titles of four words
and more count: `Natural language processing` is a book's title and also a
phrase any abstract holds, and it matched three papers until the limit came in.
An automatic draft rarely knows a paper's year and often not its title, so on
the first 43 papers matching by description found 12 citations and matching by
first page 111 more — Sculley et al. 2015 is now cited by 16 papers in the
library, Cunningham 1992 by 13. Where several files match, an identifier wins
over a title, a title over a page, and a publication over a book-shelf copy of
the same file.

What the top of a first page says can be wider than its title. A review of 2025
states that it "is a revised and expanded version of a conference paper
entitled …", so a citation of that conference paper is matched to the
expanded version — which is the file the library holds of it. A journal's name,
which heads every first page of that journal, is never a title: when a parse
lands on `Journal of …`, `Proceedings of …` or `… Transactions on …`, the entry
is left without one. The years a first page prints were tried as a check and
dropped: an arXiv id reads as a year (`1706.03762`), a preprint is cited years
before its journal version, and on these papers the check lost eight true
matches to stop one false one.

Matching runs on every pass, not once when the list is read, because **the
library grows**: an entry that found nothing today should find the book uploaded
tomorrow.

## Asking

```sh
curl -s 'http://localhost:8080/citations?path=uploads/mapreduce.pdf' | jq
curl -s 'http://localhost:8080/citations/citing?path=uploads/kleppmann.pdf' | jq
curl -s 'http://localhost:8080/citations/citing?doi=10.1145/359545.359563' | jq
curl -s 'http://localhost:8080/citations/shared?a=a.pdf&b=b.pdf' | jq
```

- `/citations` — what a publication cites, in the order its list prints them,
  each entry with the source it was matched to when the library holds it.
- `/citations/citing` — the other direction. By `path` it uses the match; by
  `doi` it uses what two bibliographies would agree on, which works for a work
  the library does not hold at all.
- `/citations/shared` — what two publications both point at.

Over MCP it is one tool, `corpus_citations`, with `direction` of `cited` (the
default) or `citing`. One tool rather than three: three nearly identical tools
over one table are a thing to confuse rather than a thing to choose between.

## Enrichment

```sh
curl -X POST 'http://localhost:8080/citations/enrich?path=uploads/mapreduce.pdf' | jq
```

For the entries that named a DOI and little else, this asks doi.org for the
record — content negotiation, CSL-JSON, from Crossref or DataCite — and fills in
only what is missing. It never overwrites the line as printed, and it never runs
during an indexing pass: the corpus holds a diary, and it reads nothing to
anyone unless it is asked. At most fifty entries a request.

Not built, and worth knowing about: when the *paper itself* has a DOI, Crossref
often returns its whole reference list in the record's `reference` field, already
structured. That would be a more accurate list than anything parsed out of a
PDF — an alternative to offer, not a replacement for reading the file.
