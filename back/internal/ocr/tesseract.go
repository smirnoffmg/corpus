package ocr

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Tesseract recognises pages with poppler's pdftoppm and Tesseract, both run as
// processes: the page is rendered to a grey image and piped straight into
// Tesseract, with no image file in between.
type Tesseract struct {
	Languages string // Tesseract's language codes, e.g. "rus+eng"
	DPI       int    // render resolution; 300 is what Tesseract is trained at
}

var pagesLine = regexp.MustCompile(`(?m)^Pages:\s+(\d+)`)

func (t Tesseract) Pages(ctx context.Context, pdf string) (int, error) {
	out, err := exec.CommandContext(ctx, "pdfinfo", pdf).Output()
	if err != nil {
		return 0, fmt.Errorf("pdfinfo %s: %w", pdf, err)
	}
	m := pagesLine.FindSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("pdfinfo %s: no page count", pdf)
	}
	return strconv.Atoi(string(m[1]))
}

func (t Tesseract) Recognize(ctx context.Context, pdf string, page int) (string, error) {
	dpi := t.DPI
	if dpi <= 0 {
		dpi = 300
	}
	n := strconv.Itoa(page)
	render := exec.CommandContext(ctx, "pdftoppm", "-f", n, "-l", n, "-r", strconv.Itoa(dpi), "-gray", "-singlefile", pdf)
	recognise := exec.CommandContext(ctx, "tesseract", "stdin", "stdout", "-l", t.Languages)
	// Pages are recognised in parallel by the caller; Tesseract's own threads
	// on top of that only fight over the same cores.
	recognise.Env = append(os.Environ(), "OMP_THREAD_LIMIT=1")

	var renderErr, recogniseErr bytes.Buffer
	render.Stderr = &renderErr
	recognise.Stderr = &recogniseErr
	pipe, err := render.StdoutPipe()
	if err != nil {
		return "", err
	}
	recognise.Stdin = pipe
	var out bytes.Buffer
	recognise.Stdout = &out

	if err := render.Start(); err != nil {
		return "", fmt.Errorf("pdftoppm: %w", err)
	}
	if err := recognise.Start(); err != nil {
		_ = render.Process.Kill()
		_ = render.Wait()
		return "", fmt.Errorf("tesseract: %w", err)
	}
	errRender := render.Wait()
	errRecognise := recognise.Wait()
	if errRender != nil {
		return "", fmt.Errorf("pdftoppm page %d: %w: %s", page, errRender, strings.TrimSpace(renderErr.String()))
	}
	if errRecognise != nil {
		return "", fmt.Errorf("tesseract page %d: %w: %s", page, errRecognise, strings.TrimSpace(recogniseErr.String()))
	}
	return out.String(), nil
}
