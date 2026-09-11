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

func TestFolioCandidateIgnoresOversizedNumbers(t *testing.T) {
	if got := folioCandidate("Copyright 20250\n\ntext\n"); got != 0 {
		t.Errorf("candidate = %d, want 0", got)
	}
}
