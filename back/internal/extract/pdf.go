package extract

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// PDF extracts the text of each page via poppler's pdftotext. A page longer than
// the splitter allows is cut further: the citation stays the page either way, but
// the vector should describe something the model can read in one go.
func (sp Splitter) PDF(ctx context.Context, path string) ([]corpus.Chunk, error) {
	pages, err := pdfPages(ctx, path, "-layout")
	if err != nil {
		return nil, err
	}
	return sp.Pages(pages), nil
}

// Paper is a publication: its pages, without its list of references. A
// bibliography matches every query that names a term the paper cites anything
// about and answers none of them — the same reason contents pages and subject
// indexes are dropped. What is in it is not lost: it is read into citations of
// its own.
func (sp Splitter) Paper(ctx context.Context, path string) ([]corpus.Chunk, error) {
	layout, err := pdfPages(ctx, path, "-layout")
	if err != nil {
		return nil, err
	}
	reading, err := PaperText(ctx, path)
	if err != nil {
		return nil, err
	}
	return sp.Pages(withoutBibliography(layout, reading)), nil
}

// PaperPages is Pages for a publication recognised from a scan, whose one text
// is both the layout and the reading order.
func (sp Splitter) PaperPages(pages []string) []corpus.Chunk {
	return sp.Pages(withoutBibliography(pages, pages))
}

var columnGap = regexp.MustCompile(`\s{3,}`)

// PaperText is the text of a publication in reading order, for parsing its
// references. Deliberately without -layout: on a two-column paper the layout
// mode sets both columns side by side on one line, and a reference list read
// that way comes out interleaved.
func PaperText(ctx context.Context, path string) ([]string, error) {
	return pdfPages(ctx, path, "-raw")
}

func pdfPages(ctx context.Context, path, mode string) ([]string, error) {
	out, err := exec.CommandContext(ctx, "pdftotext", mode, "-enc", "UTF-8", path, "-").Output()
	if err != nil {
		return nil, fmt.Errorf("pdftotext %s: %w", path, err)
	}
	// pdftotext terminates every page with a form feed, so the final split
	// element is an empty tail, not a page.
	return strings.Split(string(out), "\f"), nil
}

// withoutBibliography blanks a publication's reference list out of its layout
// text, keeping what the heading's page holds above it — on most papers the end
// of the conclusion — and whatever follows the list, such as an appendix. Pages
// are blanked rather than removed, so every page keeps its number.
//
// Where the list is comes from the reading-order text, the one the references
// are read from: on a two-column page the layout text sets the heading beside
// the other column, and there is no line in it that is the heading alone. The
// cut is then made in the layout text by page, and within the first and last
// page by the first line that begins as the heading, or as whatever follows the
// list, does. A page where neither can be found is kept whole — indexing a few
// references is a lesser fault than losing a page of text.
func withoutBibliography(layout, reading []string) []string {
	if len(layout) != len(reading) {
		return layout
	}
	kept := make([]string, len(layout))
	copy(kept, layout)
	for _, kind := range listKinds {
		span, _, ok := locate(reading, kind)
		if !ok {
			continue
		}
		blankSpan(kept, reading, span, kind)
	}
	return kept
}

func blankSpan(kept, reading []string, span listSpan, kind listKind) {
	for i := span.fromPage; i < len(kept) && i <= span.toPage; i++ {
		lines := strings.Split(kept[i], "\n")
		from, to := 0, len(lines)
		if i == span.fromPage {
			if from = headingLine(lines, kind.heading); from < 0 {
				continue
			}
		}
		if i == span.toPage {
			end := strings.TrimSpace(strings.Split(reading[i], "\n")[span.toLine])
			if to = lineBeginning(lines, from, end); to < 0 {
				continue
			}
		}
		kept[i] = strings.Join(append(lines[:from:from], lines[to:]...), "\n")
	}
}

func headingLine(lines []string, heading func(string) bool) int {
	for i, line := range lines {
		if heading(line) || heading(leftColumn(line)) {
			return i
		}
	}
	return -1
}

func lineBeginning(lines []string, from int, text string) int {
	for i := from; i < len(lines); i++ {
		if left := leftColumn(lines[i]); left != "" && strings.HasPrefix(text, left) {
			return i
		}
	}
	return -1
}

// leftColumn is a layout line up to the gap that separates it from the other
// column.
func leftColumn(line string) string {
	line = strings.TrimSpace(line)
	if at := columnGap.FindStringIndex(line); at != nil {
		return line[:at[0]]
	}
	return line
}

// Pages turns the text of a book's pages, in order, into chunks: each cited by
// its PDF page and the number printed on it, with contents, index and
// unreadable pages dropped. Text extracted from a PDF and text recognised from
// a scan both come this way.
func (sp Splitter) Pages(pages []string) []corpus.Chunk {
	folios := detectFolios(pages)

	chunks := make([]corpus.Chunk, 0, len(pages))
	for i, body := range pages {
		body = strings.TrimSpace(body)
		if body == "" || frontOrBackMatter(body) || unreadable(body) {
			continue
		}
		page := i + 1
		for _, part := range sp.splitSection(body) {
			if tooShort(part) || unreadable(part) {
				continue
			}
			chunks = append(chunks, corpus.Chunk{
				Ord:     len(chunks) + 1,
				Page:    page,
				Printed: folios[i],
				Body:    part,
			})
		}
	}
	return chunks
}
