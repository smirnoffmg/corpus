// Package shrink makes a PDF smaller by re-encoding its images, and keeps the
// result only when everything the indexer reads from the file stayed the same.
//
// Most of a large book is its pictures: a biology textbook was 383 MB, of which
// the text layer is a few. Ghostscript shrank such books by four fifths, but it
// rewrites the fonts, and on this library it dropped figure labels (1.7% of a
// paper's characters) and fused "fi" into a ligature a query does not match.
// Re-encoding the images alone shrank the same textbook by three quarters with
// its text byte for byte intact, and that is the condition here: a file whose
// text is unchanged keeps its chunks, its vectors and its description, and only
// its content hash moves.
package shrink

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/smirnoffmg/corpus/internal/extract"
)

//go:embed images.py
var recoder string

// The defaults. 150 dpi is what an e-book reader shows a figure at; a scan is
// rendered at 300 dpi for recognition, so its pages are not taken below that.
const (
	defaultTextDPI = 150
	defaultScanDPI = 300
	// A tenth: below it the saving does not pay for a new content hash.
	defaultMinGain = 0.1
)

// Result is what a Shrink did. Kept is false when the shrunk file was not good
// enough to replace the original, and Reason says why.
type Result struct {
	Before, After int64
	Kept          bool
	Reason        string
}

// Shrinker re-encodes the images of PDFs. The zero value is ready to use.
type Shrinker struct {
	TextDPI int     // images of a PDF with a text layer
	ScanDPI int     // images of a scan
	MinGain float64 // the share of the size a shrunk file must save to be kept
	// Recode writes src with its images re-encoded to dst. Nil runs images.py
	// with python3, which needs pikepdf and Pillow.
	Recode func(ctx context.Context, src, dst string, dpi int) error
}

// Shrink writes a smaller copy of the PDF src to dst. dst exists afterwards
// only when Result.Kept is true.
func (s Shrinker) Shrink(ctx context.Context, src, dst string) (res Result, err error) {
	defer func() {
		if err != nil || !res.Kept {
			_ = os.Remove(dst)
		}
	}()

	before, err := read(ctx, src)
	if err != nil {
		return res, err
	}
	dpi := or(s.TextDPI, defaultTextDPI)
	if extract.Scan(before.pages()) {
		dpi = or(s.ScanDPI, defaultScanDPI)
	}
	recode := s.Recode
	if recode == nil {
		recode = python
	}
	if err = recode(ctx, src, dst, dpi); err != nil {
		return res, fmt.Errorf("re-encode images of %s: %w", src, err)
	}

	if res.Before, err = size(src); err != nil {
		return res, err
	}
	if res.After, err = size(dst); err != nil {
		return res, err
	}
	gain := or(s.MinGain, defaultMinGain)
	if float64(res.After) > float64(res.Before)*(1-gain) {
		res.Reason = fmt.Sprintf("not smaller by %.0f%%", gain*100)
		return res, nil
	}

	after, err := read(ctx, dst)
	if err != nil {
		res.Reason = "the shrunk file does not read: " + err.Error()
		return res, nil //nolint:nilerr // a recoder's broken output is a refusal, not a failure
	}
	if what := before.differs(after); what != "" {
		res.Reason = "the shrunk file's " + what + " differs"
		return res, nil
	}
	res.Kept = true
	return res, nil
}

// reading is everything the indexer takes from a PDF: the layout text it
// splits into chunks, the reading-order text references are parsed from, and
// the title and author a description is drafted from.
type reading struct {
	layout, raw   []string
	title, author string
}

func read(ctx context.Context, path string) (reading, error) {
	layout, err := extract.PDFPages(ctx, path)
	if err != nil {
		return reading{}, err
	}
	raw, err := extract.PaperText(ctx, path)
	if err != nil {
		return reading{}, err
	}
	return reading{
		layout: layout,
		raw:    raw,
		title:  extract.PDFTitle(ctx, path, ""),
		author: extract.PDFAuthor(ctx, path),
	}, nil
}

// pages drops the empty tail pdftotext leaves after the last page.
func (r reading) pages() []string {
	if n := len(r.layout); n > 0 {
		return r.layout[:n-1]
	}
	return nil
}

func (r reading) differs(o reading) string {
	switch {
	case !slices.Equal(r.layout, o.layout):
		return "text"
	case !slices.Equal(r.raw, o.raw):
		return "reading-order text"
	case r.title != o.title:
		return "title"
	case r.author != o.author:
		return "author"
	}
	return ""
}

func python(ctx context.Context, src, dst string, dpi int) error {
	// -I keeps the interpreter from importing anything out of the working
	// directory, which may be the library, where anyone's upload lands.
	cmd := exec.CommandContext(ctx, "python3", "-I", "-", src, dst, strconv.Itoa(dpi))
	cmd.Stdin = strings.NewReader(recoder)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.Join(err, errors.New(lastLine(stderr.String())))
	}
	return nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	return s[strings.LastIndexByte(s, '\n')+1:]
}

func size(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func or[T int | float64](v, def T) T {
	if v <= 0 {
		return def
	}
	return v
}
