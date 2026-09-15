package extract

import (
	"fmt"
	"testing"
)

// book builds pages whose running head carries the printed number, offset by
// the given amount of front matter.
func book(pages, frontMatter int) []string {
	out := make([]string, 0, pages)
	for i := range frontMatter {
		out = append(out, fmt.Sprintf("Preface\n\nfront matter %d\n", i))
	}
	for i := 1; i <= pages; i++ {
		if i%2 == 0 {
			out = append(out, fmt.Sprintf("%d   Chapter 2\n\nbody text\n", i))
		} else {
			out = append(out, fmt.Sprintf("Chapter 2   %d\n\nbody text\n", i))
		}
	}
	return out
}

func TestDetectFoliosFindsTheOffset(t *testing.T) {
	pages := book(40, 6)
	folios := detectFolios(pages)

	if got := folios[6]; got != 1 {
		t.Errorf("first body page printed number = %d, want 1", got)
	}
	if got := folios[45]; got != 40 {
		t.Errorf("last body page printed number = %d, want 40", got)
	}
	for i := range 6 {
		if folios[i] != 0 {
			t.Errorf("front matter page %d got printed number %d, want none", i, folios[i])
		}
	}
}

func TestDetectFoliosRejectsBooksWithoutNumbering(t *testing.T) {
	pages := []string{"Figure 12 shows\n", "see 1998 for details\n", "plain text\n", "more text\n"}
	for i, got := range detectFolios(pages) {
		if got != 0 {
			t.Errorf("page %d got printed number %d, want none", i, got)
		}
	}
}

func TestFolioCandidatesIgnoreOversizedNumbers(t *testing.T) {
	if got := folioCandidates("Copyright 20250\n\ntext\n"); len(got) != 0 {
		t.Errorf("candidates = %v, want none", got)
	}
}

// stamped reproduces a library copy: every page carries two stamped lines above
// the running head that holds the real page number.
func stamped(pages, frontMatter int) []string {
	out := make([]string, 0, pages)
	for i := range frontMatter {
		out = append(out, fmt.Sprintf(
			"The Go Programming Language\n© 2016 Donovan & Kernighan\nrevision 3b600c, date 29 Sep 2015\n\nfront %d\n", i))
	}
	for i := 1; i <= pages; i++ {
		// The stamp takes three lines before the running head, and the number
		// sits at the outer edge: left on even pages, right on odd ones.
		head := fmt.Sprintf("SECTION 8.5. LOOPING IN PARALLEL   %d", i)
		if i%2 == 0 {
			head = fmt.Sprintf("%d   CHAPTER 8. GOROUTINES", i)
		}
		out = append(out, fmt.Sprintf(
			"The Go Programming Language\n© 2016 Donovan & Kernighan\nrevision 3b600c, date 29 Sep 2015\n%s\n\nbody\n", head))
	}
	return out
}

func TestDetectFoliosSeesPastStampedHeaders(t *testing.T) {
	folios := detectFolios(stamped(40, 19))
	if got := folios[19]; got != 1 {
		t.Errorf("first body page printed number = %d, want 1", got)
	}
	if got := folios[55]; got != 37 {
		t.Errorf("page 56 of the PDF printed number = %d, want 37", got)
	}
}

// Packt prints the page number alone at the foot, in brackets: "[ 327 ]".
func TestDetectFoliosReadsBracketedFolios(t *testing.T) {
	pages := make([]string, 0, 45)
	for i := range 5 {
		pages = append(pages, fmt.Sprintf("Table of Contents\n\n[ %s ]\n", []string{"i", "ii", "iii", "iv", "v"}[i]))
	}
	for i := 1; i <= 40; i++ {
		pages = append(pages, fmt.Sprintf("Chapter 18\n\nThe EAI Siebel Adapter business service\n\n[ %d ]\n", i))
	}
	folios := detectFolios(pages)
	if got := folios[5]; got != 1 {
		t.Errorf("first body page printed number = %d, want 1", got)
	}
	if got := folios[44]; got != 40 {
		t.Errorf("last body page printed number = %d, want 40", got)
	}
}

func TestFolioCandidatesReadBracketsOnlyAroundANumber(t *testing.T) {
	if got := folioCandidates("[ 327 ]\n"); len(got) != 1 || got[0] != 327 {
		t.Errorf("candidates = %v, want [327]", got)
	}
	if got := folioCandidates("[12]\n"); len(got) != 1 || got[0] != 12 {
		t.Errorf("candidates = %v, want [12]", got)
	}
	if got := folioCandidates("see [ 3 ] and [ 4 ] above\n"); len(got) != 0 {
		t.Errorf("candidates = %v, want none: citation marks inside a sentence are not folios", got)
	}
}
