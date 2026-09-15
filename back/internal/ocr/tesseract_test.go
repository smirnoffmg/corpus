package ocr_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/ocr"
)

// onePagePDF writes a PDF with one line of large type. It has a text layer, but
// Recognize renders the page to pixels first, exactly as it does a scan, so the
// text layer plays no part.
func onePagePDF(t *testing.T, line string) string {
	t.Helper()
	content := fmt.Sprintf("BT /F1 36 Tf 72 700 Td (%s) Tj ET", line)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, o := range objects {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	path := filepath.Join(t.TempDir(), "page.pdf")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	return path
}

func TestTesseractRecognisesARenderedPage(t *testing.T) {
	for _, tool := range []string{"pdfinfo", "pdftoppm", "tesseract"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed: the container has it, this machine does not", tool)
		}
	}
	pdf := onePagePDF(t, "Retrieval quality measured honestly")
	engine := ocr.Tesseract{Languages: "eng", DPI: 150}

	pages, err := engine.Pages(context.Background(), pdf)
	require.NoError(t, err)
	require.Equal(t, 1, pages)

	text, err := engine.Recognize(context.Background(), pdf, 1)
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(text), "retrieval quality")

	_, err = engine.Recognize(context.Background(), filepath.Join(t.TempDir(), "absent.pdf"), 1)
	require.Error(t, err)
}
