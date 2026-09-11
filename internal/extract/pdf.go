package extract

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// PDF extracts one Chunk per page via poppler's pdftotext.
func PDF(ctx context.Context, path string) ([]Chunk, error) {
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", "-enc", "UTF-8", path, "-")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("pdftotext %s: %w", path, err)
	}

	// pdftotext terminates every page with a form feed, so the final split
	// element is an empty tail, not a page.
	pages := strings.Split(string(out), "\f")
	chunks := make([]Chunk, 0, len(pages))
	for i, body := range pages {
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		page := i + 1
		chunks = append(chunks, Chunk{
			Ord:     page,
			Page:    page,
			Locator: fmt.Sprintf("с. %d", page),
			Body:    body,
		})
	}
	return chunks, nil
}
