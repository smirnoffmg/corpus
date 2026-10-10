package extract_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/smirnoffmg/corpus/internal/corpus"
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

func readablePage(n int) string {
	return fmt.Sprintf("Глава %d\n\n%s", n, strings.Repeat("Случайная величина принимает значения с заданными вероятностями. ", 3))
}

// hoeffding1948 in miniature: a JSTOR cover with a text layer in front of a
// scan. The cover is not what makes it a book with text.
func TestAScanWithATextCoverIsStillAScan(t *testing.T) {
	pages := []string{readablePage(1)}
	for range 32 {
		pages = append(pages, "")
	}

	text := extract.TextPages(pages)
	if len(text) != 33 || !text[0] {
		t.Fatalf("text pages = %v, want page 1 marked as text", text)
	}
	for i, has := range text[1:] {
		if has {
			t.Errorf("page %d has no text but was marked as having it", i+2)
		}
	}
	if !extract.Scan(pages) {
		t.Error("1 page of text in 33 is a scan")
	}
}

// A blank page, a plate, a figure: an ordinary book has a few, and none of
// them makes it a scan.
func TestABookWithAFewImagePagesIsNotAScan(t *testing.T) {
	var pages []string
	for p := 1; p <= 20; p++ {
		switch p {
		case 2, 9, 15:
			pages = append(pages, "")
		default:
			pages = append(pages, readablePage(p))
		}
	}
	if extract.Scan(pages) {
		t.Error("a book with prose on 17 of 20 pages was taken for a scan")
	}
}

func TestMojibakeAndRunningHeadsAreNotText(t *testing.T) {
	pages := []string{
		strings.Repeat("Î÷åâèäíî, ÷òî ìíîæåñòâî ", 10),
		"12    ГЛАВА 1. ВЕРОЯТНОСТНОЕ ПРОСТРАНСТВО",
	}
	for i, has := range extract.TextPages(pages) {
		if has {
			t.Errorf("page %d (%.30q) counted as text", i+1, pages[i])
		}
	}
}

// Комбинаторика (issue #8): a text layer on every page, in fonts that decode
// to punctuation and to letters swapped one for one. It is a scan.
func TestAGarbledTextLayerIsAScan(t *testing.T) {
	garbage := []string{
		strings.Repeat(":#' 7 !;  7 !; %3\n'     ' $     )    ) ! !\n  ( ! )  >   + !  +;'\n", 4),
		strings.Repeat("wnénxzjtvkrq x wvkzvéntq¹uq ©zª tj ©jª Íéq ëzvu tnrvzvén qo tj¡q} wnénxzjtvkvr\n", 3),
	}
	var pages []string
	for p := range 20 {
		pages = append(pages, garbage[p%2])
	}

	for i, has := range extract.TextPages(pages) {
		if has {
			t.Errorf("page %d (%.30q) counted as text", i+1, pages[i])
		}
	}
	if !extract.Scan(pages) {
		t.Error("a book whose layer is garbage on every page is not taken for a scan")
	}
	if chunks := extract.DefaultSplitter.Pages(pages); len(chunks) != 0 {
		t.Errorf("got %d chunks of garbage, want none", len(chunks))
	}
}

// go-design-patterns.pdf (issue #10): every ASCII character of its layer reads
// 29 code points down, and code on a page lifts its vowel share above the rule.
func TestAShiftedTextLayerIsAScan(t *testing.T) {
	page, err := os.ReadFile("testdata/go-design-patterns-p263.txt")
	if err != nil {
		t.Fatal(err)
	}
	pages := make([]string, 20)
	for i := range pages {
		pages[i] = string(page)
	}

	for i, has := range extract.TextPages(pages) {
		if has {
			t.Errorf("page %d counted as text", i+1)
		}
	}
	if !extract.Scan(pages) {
		t.Error("a book whose layer is shifted on every page is not taken for a scan")
	}
	if chunks := extract.DefaultSplitter.Pages(pages); len(chunks) != 0 {
		t.Errorf("got %d chunks of shifted text, want none", len(chunks))
	}
}

func TestThinChunksCoverTooFewPages(t *testing.T) {
	chunks := []corpus.Chunk{{Page: 1}, {Page: 1}, {Page: 3}}
	if !extract.Thin(chunks, 33) {
		t.Error("2 pages of chunks out of 33 are thin")
	}
	if extract.Thin(chunks, 4) {
		t.Error("2 pages of chunks out of 4 cover half the book")
	}
}
