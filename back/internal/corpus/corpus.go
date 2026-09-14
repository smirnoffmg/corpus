// Package corpus holds the types the rest of the program is written in terms
// of. It depends on nothing: extraction, storage, ranking and the API all point
// at it, rather than at each other.
package corpus

import (
	"fmt"
	"time"
)

// Chunk is one indexable unit: a page of a PDF or a section of a note.
type Chunk struct {
	Ord     int
	Page    int    // 0 when the source has no pages
	Printed int    // page number printed on the page; 0 when unknown
	Heading string // path of headings inside a note; empty for a book page
	Anchor  string // id of the section in an HTML page, to link to it; empty otherwise
	Tags    []string
	Lang    string // Postgres text-search config, filled in by the indexer
	Body    string
}

// Source is a file the corpus was built from.
type Source struct {
	Kind  string
	Path  string
	Title string
	Hash  string
}

// Query is everything a search takes. As a struct rather than six positional
// arguments, because half of them are ints and the compiler cannot tell them
// apart.
type Query struct {
	Text      string
	Kind      string // "book", "vault", "docs", or empty for all
	Mode      string // "fts", "vector", or "hybrid" (default)
	Limit     int
	PerSource int // at most this many hits from one source; 0 for no limit
	// Normalization is the bit mask ts_rank_cd applies for document length:
	// 0 ignores it, 1 divides by 1+log(length), 2 by the length itself.
	Normalization int
	// TitleBoost is added to the rank of a chunk whose source title matches the
	// query. A note called "Кросс-энтропия" should beat a note that merely
	// mentions the term, and on text rank alone the two tie.
	TitleBoost float64
}

// Hit is one search result: enough to judge it and to cite it.
type Hit struct {
	ID      int64   `json:"id"`
	Kind    string  `json:"kind"`
	Title   string  `json:"title"`
	Path    string  `json:"path"`
	Locator string  `json:"locator"`
	Anchor  string  `json:"anchor,omitempty"` // section id in an HTML source
	Page    int     `json:"page,omitempty"`
	Rank    float32 `json:"rank"`
	Snippet string  `json:"snippet"`
}

// SourceStatus is how far a source has come: stored, embedded, or stuck. It is
// what a library listing shows while an upload works its way through.
type SourceStatus struct {
	Kind        string    `json:"kind"`
	Path        string    `json:"path"`
	Title       string    `json:"title"`
	IndexedAt   time.Time `json:"indexed_at"`
	Chunks      int64     `json:"chunks"`
	Embedded    int64     `json:"embedded"`
	Quarantined int64     `json:"quarantined"`
}

// Pending is a chunk that has no embedding yet.
type Pending struct {
	ID   int64
	Body string
}

// Passage is the full text behind a hit, which is what a reader needs once the
// search has pointed at a page.
type Passage struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Path     string `json:"path"`
	Locator  string `json:"locator"`
	Anchor   string `json:"anchor,omitempty"`
	Page     int    `json:"page,omitempty"` // PDF page, to open the file at the passage
	Body     string `json:"body"`
	Previous string `json:"previous,omitempty"`
	Next     string `json:"next,omitempty"`
}

// Locator names the place to cite. For a note it is the path of headings; for a
// book page the number printed on the page, which is what a reader of any copy
// can follow, with the PDF page alongside when the two differ. When the printed
// number could not be read the locator says so rather than passing a PDF page
// off as a page of the book.
//
// It is composed here rather than stored: it is a rendering of page and printed,
// and storing a rendering means re-extracting every book to change how a
// citation looks.
func Locator(heading string, page, printed int) string {
	switch {
	case heading != "":
		return heading
	case printed == 0:
		return fmt.Sprintf("PDF %d", page)
	case printed != page:
		return fmt.Sprintf("с. %d (PDF %d)", printed, page)
	default:
		return fmt.Sprintf("с. %d", page)
	}
}
