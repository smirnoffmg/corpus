package extract_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/smirnoffmg/corpus/internal/extract"
)

// Recognised pages go through the same path as extracted ones: the page the
// chunk cites, the number printed on it, and the filters that drop contents
// pages and running heads. A scan is a book like any other once it has text.
func TestPagesMakeChunksLikeAnExtractedBook(t *testing.T) {
	prose := strings.Repeat("Раньше химики-органики не умели анализировать смеси сложных изомеров. ", 4)
	pages := []string{
		"", // cover: nothing recognised
		"Оглавление\n\nГлава 1 . . . . . . . . 1\nГлава 2 . . . . . . . . 9\nГлава 3 . . . . . . . . 17",
	}
	for p := 3; p <= 12; p++ {
		pages = append(pages, fmt.Sprintf("Экспериментальные подходы %d\n\n%s", p-2, prose))
	}

	chunks := extract.DefaultSplitter.Pages(pages)
	if len(chunks) != 10 {
		t.Fatalf("got %d chunks, want the ten pages of prose: %+v", len(chunks), chunks)
	}
	first := chunks[0]
	if first.Page != 3 || first.Printed != 1 || first.Ord != 1 {
		t.Errorf("first chunk = page %d, printed %d, ord %d; want PDF page 3 printed as 1", first.Page, first.Printed, first.Ord)
	}
	if !strings.Contains(first.Body, "химики-органики") {
		t.Errorf("body = %q", first.Body)
	}
}
