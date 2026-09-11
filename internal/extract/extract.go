package extract

// Chunk is one indexable unit: a page of a PDF or a section of a note.
type Chunk struct {
	Ord     int
	Page    int // 0 when the source has no pages
	Locator string
	Lang    string // Postgres text-search config, filled in by the indexer
	Tags    []string
	Body    string
}
