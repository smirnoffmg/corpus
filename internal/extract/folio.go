package extract

import (
	"strconv"
	"strings"
)

// maxFolio guards against reading a year or a figure number as a page number.
const maxFolio = 9999

// detectFolios maps each page to the page number printed on it. A book's front
// matter is numbered separately (or not at all), and figures and running heads
// are full of stray numbers, so a candidate is trusted only when it agrees with
// the offset that holds across the whole book.
func detectFolios(pages []string) []int {
	candidates := make([]int, len(pages))
	offsets := map[int]int{}
	for i, page := range pages {
		candidates[i] = folioCandidate(page)
		if candidates[i] > 0 {
			offsets[i+1-candidates[i]]++
		}
	}

	modal, best := 0, 0
	for offset, n := range offsets {
		if n > best {
			modal, best = offset, n
		}
	}
	// Fewer than a fifth of the pages agreeing means no usable numbering.
	if best*5 < len(pages) {
		return make([]int, len(pages))
	}

	folios := make([]int, len(pages))
	for i, c := range candidates {
		if c > 0 && i+1-c == modal {
			folios[i] = c
		}
	}
	return folios
}

// folioCandidate looks for a bare number at either end of the first or last
// text line, which is where a running head or foot puts it.
func folioCandidate(page string) int {
	lines := make([]string, 0, 4)
	for _, line := range strings.Split(page, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) == 0 {
		return 0
	}

	edges := []string{lines[0], lines[len(lines)-1]}
	for _, line := range edges {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		for _, field := range []string{fields[0], fields[len(fields)-1]} {
			if n, err := strconv.Atoi(field); err == nil && n > 0 && n <= maxFolio {
				return n
			}
		}
	}
	return 0
}
