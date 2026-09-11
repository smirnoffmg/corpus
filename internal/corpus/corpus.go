// Package corpus holds the types the rest of the program is written in terms
// of. It depends on nothing: extraction, storage, ranking and the API all point
// at it, rather than at each other.
package corpus

// Chunk is one indexable unit: a page of a PDF or a section of a note.
type Chunk struct {
	Ord     int
	Page    int // 0 when the source has no pages
	Printed int // page number printed on the page; 0 when unknown
	Locator string
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

// Hit is one search result: enough to judge it and to cite it.
type Hit struct {
	ID      int64   `json:"id"`
	Kind    string  `json:"kind"`
	Title   string  `json:"title"`
	Path    string  `json:"path"`
	Locator string  `json:"locator"`
	Page    int     `json:"page,omitempty"`
	Rank    float32 `json:"rank"`
	Snippet string  `json:"snippet"`
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
	Body     string `json:"body"`
	Previous string `json:"previous,omitempty"`
	Next     string `json:"next,omitempty"`
}
