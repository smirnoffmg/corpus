// Package corpus holds the types the rest of the program is written in terms
// of. It depends on nothing: extraction, storage, ranking and the API all point
// at it, rather than at each other.
package corpus

import (
	"errors"
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
	// Recognised marks text read from a scan by OCR rather than taken from the
	// PDF's text layer: close, but a reading, and worth checking before quoting.
	Recognised bool
}

// Scan is a PDF with no text layer, waiting for its pages to be recognised.
// Kind is "book" or "paper": it says which library root the path is relative to.
type Scan struct {
	Kind       string
	Hash       string
	Path       string
	Pages      int
	Recognised int
}

// Query is everything a search takes. As a struct rather than six positional
// arguments, because half of them are ints and the compiler cannot tell them
// apart.
type Query struct {
	Text      string
	Kind      string // "book", "paper", "vault", "docs", or empty for all
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
	// Depth is how many hits each leg of a hybrid search is asked for before
	// fusion; 0 leaves the default of twice the limit.
	Depth int
	// EfSearch is hnsw.ef_search for this search; 0 leaves the server's.
	EfSearch int
	// Exact scans every vector instead of walking the index: slow, and the
	// reference an approximate search's recall is measured against.
	Exact bool
	// Rerank has a cross-encoder reorder the first candidates. It costs about a
	// second a search, so it is asked for rather than on by default.
	Rerank bool
}

// Hit is one search result: enough to judge it and to cite it.
type Hit struct {
	ID      int64   `json:"id"`
	Kind    string  `json:"kind"`
	Title   string  `json:"title"`
	Path    string  `json:"path"`
	Locator string  `json:"locator"`
	Anchor  string  `json:"anchor,omitempty"` // section id in an HTML source
	OCR     bool    `json:"ocr,omitempty"`    // text recognised from a scan
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
	Description string    `json:"description"`   // "", "draft" or "checked"
	OCR         bool      `json:"ocr,omitempty"` // built from recognised pages
	// A scan still being recognised: it has pages and no chunks yet.
	ScanPages      int    `json:"scan_pages,omitempty"`
	ScanRecognised int    `json:"scan_recognised,omitempty"`
	ScanFailed     bool   `json:"scan_failed,omitempty"`
	ScanError      string `json:"scan_error,omitempty"`
}

// CSL is a bibliographic record in CSL-JSON, the format citeproc, Pandoc and
// Zotero share. It is kept as a map: the schema has dozens of optional fields,
// and the service only reads a handful of them.
type CSL = map[string]any

// ErrNoReference is a source that has no bibliographic description, or no
// key to file one under.
var ErrNoReference = errors.New("no bibliographic description")

// Reference is a source's bibliographic description. Key is the source's
// content hash for a book, "manual:<name>" for a manual, so a description
// outlives the file being renamed, moved or re-uploaded.
type Reference struct {
	Key       string    `json:"key"`
	CiteKey   string    `json:"citekey"`
	CSL       CSL       `json:"csl"`
	Status    string    `json:"status"` // "draft" until someone has checked it, then "checked"
	UpdatedAt time.Time `json:"updated_at"`
}

// Style is a citation style added beyond the bundled ones.
type Style struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// StyleXML is an added citation style with its CSL source.
type StyleXML struct {
	ID    string
	Title string
	XML   string
}

// Undescribed is a book or a publication without a bibliographic description,
// with the text of its opening pages (the copyright page) and closing ones (the
// imprint of a Russian book, and the references of any book).
type Undescribed struct {
	Kind              string
	Path, Hash, Title string
	Head, Tail        string
}

// Citation is one entry of a list a publication prints: the line as it
// is printed, and what could be read out of it. Raw is the source of truth —
// every other field is a reading, and a reading can be wrong.
type Citation struct {
	// List is the list the entry is printed in: "references", or "primary" for
	// the studies a systematic review reviewed.
	List      string `json:"list"`
	Ord       int    `json:"ord"`
	Raw       string `json:"raw"`
	Label     string `json:"label,omitempty"` // "[12]" or "12." as printed; empty when the list is unnumbered
	DOI       string `json:"doi,omitempty"`
	ArXiv     string `json:"arxiv,omitempty"`
	ISBN      string `json:"isbn,omitempty"`
	URL       string `json:"url,omitempty"`
	Authors   string `json:"authors,omitempty"` // the author part as printed, not split into names
	Title     string `json:"title,omitempty"`
	Container string `json:"container,omitempty"` // the journal or proceedings it appeared in
	Year      int    `json:"year,omitempty"`
	// Fingerprint is what this entry points at, as one comparable string: the
	// identifier when there is one, else the title and year, else the line
	// itself. Two papers citing the same work agree on it, which is what makes
	// the reverse lookup and the shared-references query a join.
	Fingerprint string `json:"-"`
	// Resolved is the bibliography key of the work when the library holds it,
	// with how it was matched; empty rather than a guess.
	Resolved  string `json:"resolved,omitempty"`
	MatchedBy string `json:"matched_by,omitempty"`
	// Where the library keeps the work, when it keeps it: what makes "and which
	// of these do I already have" answerable in one call.
	ResolvedKind  string `json:"resolved_kind,omitempty"`
	ResolvedPath  string `json:"resolved_path,omitempty"`
	ResolvedTitle string `json:"resolved_title,omitempty"`
}

// Unparsed is a publication whose list of references has not been read yet.
type Unparsed struct {
	Path, Hash string
	Recognised bool // its text came from OCR, so the pages come from the cache
}

// CitingPaper is a publication that cites a given work, and the entry it cites
// it by.
type CitingPaper struct {
	Path     string   `json:"path"`
	Title    string   `json:"title"`
	Citation Citation `json:"citation"`
}

// Pending is a text waiting for its vector: what the embedder is handed, and
// the key its vector is filed under — the text's SHA-256, shared by every chunk
// with the same window.
type Pending struct {
	Key  string
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
	OCR      bool   `json:"ocr,omitempty"`
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
	case page == 0:
		// Text above a note's first heading: there is no finer place to cite
		// than the note itself.
		return ""
	case printed == 0:
		return fmt.Sprintf("PDF %d", page)
	case printed != page:
		return fmt.Sprintf("с. %d (PDF %d)", printed, page)
	default:
		return fmt.Sprintf("с. %d", page)
	}
}
