package extract

import (
	"strconv"
	"strings"
)

const (
	// maxFolio guards against reading a year or a figure number as a page.
	maxFolio = 9999
	// edgeLines is how many lines from each end of the page may hold the
	// running head. It has to clear a stamp: the copy of TGPL here prints
	// three lines of title, copyright and revision before the real header.
	edgeLines = 5
)

// detectFolios maps each page to the page number printed on it. Front matter is
// numbered separately, and figures and stamps are full of stray numbers, so a
// candidate counts only when it agrees with the offset holding across the whole
// book.
func detectFolios(pages []string) []int {
	candidates := make([][]int, len(pages))
	votes := map[int]int{}
	for i, page := range pages {
		candidates[i] = folioCandidates(page)
		counted := map[int]bool{}
		for _, c := range candidates[i] {
			offset := i + 1 - c
			if offset >= 0 && !counted[offset] {
				votes[offset]++
				counted[offset] = true
			}
		}
	}

	modal, best := 0, 0
	for offset, n := range votes {
		if n > best || (n == best && offset < modal) {
			modal, best = offset, n
		}
	}
	// Fewer than a fifth of the pages agreeing means no usable numbering.
	if best*5 < len(pages) {
		return make([]int, len(pages))
	}

	folios := make([]int, len(pages))
	for i := range pages {
		printed := i + 1 - modal
		if printed < 1 {
			continue // front matter, numbered separately or not at all
		}
		switch {
		case containsInt(candidates[i], printed):
			folios[i] = printed
		case len(candidates[i]) == 0:
			// A blank or full-page figure prints no number of its own; the
			// established offset still describes where it sits in the book.
			folios[i] = printed
		}
		// Numbers that contradict the offset are left alone: the section may be
		// numbered separately, and a wrong citation is worse than none.
	}
	return folios
}

// folioCandidates collects every plain number at either end of the lines near
// the top and bottom of the page, which is where a running head or foot sits.
func folioCandidates(page string) []int {
	lines := make([]string, 0, 8)
	for _, line := range strings.Split(page, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}

	var out []int
	for i, line := range lines {
		if i >= edgeLines && i < len(lines)-edgeLines {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		for _, field := range []string{fields[0], fields[len(fields)-1]} {
			if n, err := strconv.Atoi(field); err == nil && n > 0 && n <= maxFolio {
				out = append(out, n)
			}
		}
	}
	return out
}

func containsInt(haystack []int, needle int) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
