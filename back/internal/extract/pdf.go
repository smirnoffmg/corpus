package extract

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// PDF extracts the text of each page via poppler's pdftotext. A page longer than
// the splitter allows is cut further: the citation stays the page either way, but
// the vector should describe something the model can read in one go.
func (sp Splitter) PDF(ctx context.Context, path string) ([]corpus.Chunk, error) {
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", "-enc", "UTF-8", path, "-")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("pdftotext %s: %w", path, err)
	}

	// pdftotext terminates every page with a form feed, so the final split
	// element is an empty tail, not a page.
	pages := strings.Split(string(out), "\f")
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
	return chunks, nil
}
