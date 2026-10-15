package shrink_test

import (
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/shrink"
)

func needTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed: the container has it, this machine does not", tool)
		}
	}
}

// writePDF writes a PDF of the given pages. A page has a line of text unless
// it is empty, and a large photo-like image when withImage is set: 1500 pixels
// on a three-inch page is 500 dpi, three times what a book needs.
func writePDF(t *testing.T, name string, lines []string, withImage bool) string {
	t.Helper()
	return writePDFWith(t, name, lines, withImage, nil)
}

// writePDFWith adds extra, when given, as a second image on every page: a raw
// image XObject dictionary and its stream.
func writePDFWith(t *testing.T, name string, lines []string, withImage bool, extra *rawImage) string {
	t.Helper()
	var objects []string
	add := func(o string) int { objects = append(objects, o); return len(objects) }

	add("<< /Type /Catalog /Pages 2 0 R >>")
	add("") // pages, filled in below
	font := add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	image := 0
	if withImage {
		const side = 1500
		raw := make([]byte, 0, side*side*3)
		for y := range side {
			for x := range side {
				raw = append(raw, byte(x*7+y*3), byte(x*x/side+y), byte((x^y)*5))
			}
		}
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		_, _ = w.Write(raw)
		_ = w.Close()
		image = add(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream",
			side, side, z.Len(), z.String()))
	}
	second := 0
	if extra != nil {
		second = add(fmt.Sprintf("<< /Type /XObject /Subtype /Image %s /Length %d >>\nstream\n%s\nendstream",
			extra.dict, len(extra.data), extra.data))
	}
	var kids []string
	for _, line := range lines {
		// Five lines of it: a page under 150 characters reads as a scan's.
		var text strings.Builder
		for i := range 5 {
			if line != "" {
				fmt.Fprintf(&text, "BT /F1 9 Tf 10 %d Td (%s) Tj ET\n", 200-12*i, line)
			}
		}
		content := text.String()
		resources := fmt.Sprintf("/Font << /F1 %d 0 R >>", font)
		if withImage {
			content += "q 200 0 0 150 8 8 cm /Im1 Do Q\n"
			resources += fmt.Sprintf(" /XObject << /Im1 %d 0 R", image)
			if second != 0 {
				content += "q 50 0 0 50 150 150 cm /Im2 Do Q\n"
				resources += fmt.Sprintf(" /Im2 %d 0 R", second)
			}
			resources += " >>"
		}
		stream := add(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
		page := add(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 216 216] /Contents %d 0 R /Resources << %s >> >>", stream, resources))
		kids = append(kids, fmt.Sprintf("%d 0 R", page))
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Kids %v /Count %d >>", kids, len(kids))

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
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	return path
}

var book = []string{
	"Retrieval quality is measured honestly here",
	"Chapter two begins with a judged set of queries",
	"An index of terms closes the book at the back",
}

func TestKeepsAShrunkPDFWhoseTextIsTheSame(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo")
	src := writePDF(t, "book.pdf", book, true)
	smaller := writePDF(t, "smaller.pdf", book, false)
	dst := filepath.Join(t.TempDir(), "out.pdf")

	s := shrink.Shrinker{Recode: func(_ context.Context, _, out string, _ int) error {
		return copyFile(smaller, out)
	}}
	res, err := s.Shrink(context.Background(), src, dst)
	require.NoError(t, err)
	require.True(t, res.Kept, res.Reason)
	require.Less(t, res.After, res.Before)
	require.FileExists(t, dst)
}

func TestRefusesAShrunkPDFWhoseTextChanged(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo")
	src := writePDF(t, "book.pdf", book, true)
	// Ghostscript did exactly this to figure labels: the file shrank and some
	// words were gone.
	lossy := writePDF(t, "lossy.pdf", []string{"Retrieval quality is measured here", book[1], book[2]}, false)
	dst := filepath.Join(t.TempDir(), "out.pdf")

	s := shrink.Shrinker{Recode: func(_ context.Context, _, out string, _ int) error {
		return copyFile(lossy, out)
	}}
	res, err := s.Shrink(context.Background(), src, dst)
	require.NoError(t, err)
	require.False(t, res.Kept)
	require.Contains(t, res.Reason, "text")
	require.NoFileExists(t, dst)
}

func TestRefusesAGainTooSmallToBeWorthANewHash(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo")
	src := writePDF(t, "book.pdf", book, false)
	dst := filepath.Join(t.TempDir(), "out.pdf")

	s := shrink.Shrinker{Recode: func(_ context.Context, in, out string, _ int) error {
		return copyFile(in, out)
	}}
	res, err := s.Shrink(context.Background(), src, dst)
	require.NoError(t, err)
	require.False(t, res.Kept)
	require.Contains(t, res.Reason, "smaller")
	require.NoFileExists(t, dst)
}

func TestScansKeepTheResolutionRecognitionNeeds(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo")
	scan := writePDF(t, "scan.pdf", []string{"", "", ""}, true)
	text := writePDF(t, "book.pdf", book, true)

	for _, tc := range []struct {
		name, src string
		dpi       int
	}{{"scan", scan, 300}, {"text", text, 150}} {
		t.Run(tc.name, func(t *testing.T) {
			var got int
			s := shrink.Shrinker{Recode: func(_ context.Context, in, out string, dpi int) error {
				got = dpi
				return copyFile(in, out)
			}}
			_, err := s.Shrink(context.Background(), tc.src, filepath.Join(t.TempDir(), "out.pdf"))
			require.NoError(t, err)
			require.Equal(t, tc.dpi, got)
		})
	}
}

func TestAFailedRecodeLeavesNothingBehind(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo")
	src := writePDF(t, "book.pdf", book, true)
	dst := filepath.Join(t.TempDir(), "out.pdf")

	s := shrink.Shrinker{Recode: func(_ context.Context, _, out string, _ int) error {
		_ = os.WriteFile(out, []byte("half a PDF"), 0o600)
		return errors.New("pikepdf: cannot open")
	}}
	_, err := s.Shrink(context.Background(), src, dst)
	require.Error(t, err)
	require.NoFileExists(t, dst)
}

// The real recoder, where the machine has it: the image shrinks and the text
// layer is untouched.
func TestPythonRecoderShrinksImagesAndKeepsText(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo", "python3")
	if exec.Command("python3", "-c", "import pikepdf, PIL").Run() != nil {
		t.Skip("python3 has no pikepdf and Pillow: the container has them, this machine does not")
	}
	src := writePDF(t, "book.pdf", book, true)
	dst := filepath.Join(t.TempDir(), "out.pdf")

	res, err := shrink.Shrinker{}.Shrink(context.Background(), src, dst)
	require.NoError(t, err)
	require.True(t, res.Kept, res.Reason)
	require.Less(t, res.After*2, res.Before)
}

type rawImage struct{ dict, data string }

// A book in the library carried a JPEG 2000 image whose data broke off; Pillow
// reads image data only when it is first used, so the error came from the
// resize, outside the guard around opening, and took the whole book down with it.
func TestPythonRecoderSkipsAnImageItCannotRead(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo", "python3")
	if exec.Command("python3", "-c", "import pikepdf, PIL").Run() != nil {
		t.Skip("python3 has no pikepdf and Pillow: the container has them, this machine does not")
	}
	var jpg bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 400, 400))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 31)
	}
	require.NoError(t, jpeg.Encode(&jpg, img, &jpeg.Options{Quality: 95}))
	broken := jpg.String()[:jpg.Len()/3]
	src := writePDFWith(t, "book.pdf", book, true, &rawImage{
		dict: "/Width 400 /Height 400 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode",
		data: broken,
	})
	dst := filepath.Join(t.TempDir(), "out.pdf")

	res, err := shrink.Shrinker{}.Shrink(context.Background(), src, dst)
	require.NoError(t, err)
	require.True(t, res.Kept, res.Reason)
}

// Whatever python3 has installed, the recoder's failure — no pikepdf, or a
// destination it cannot write — reaches the caller with Python's last line.
func TestTheRecodersFailureCarriesPythonsLastLine(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo", "python3")
	src := writePDF(t, "book.pdf", book, true)
	dst := filepath.Join(t.TempDir(), "absent", "out.pdf")

	_, err := shrink.Shrinker{}.Shrink(context.Background(), src, dst)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Error")
}

func TestAShrunkFileThatDoesNotReadIsRefused(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo")
	src := writePDF(t, "book.pdf", book, true)
	dst := filepath.Join(t.TempDir(), "out.pdf")

	s := shrink.Shrinker{Recode: func(_ context.Context, _, out string, _ int) error {
		return os.WriteFile(out, []byte("not a PDF"), 0o600)
	}}
	res, err := s.Shrink(context.Background(), src, dst)
	require.NoError(t, err)
	require.False(t, res.Kept)
	require.Contains(t, res.Reason, "does not read")
	require.NoFileExists(t, dst)
}

func TestAnUnreadableOriginalIsAnError(t *testing.T) {
	needTools(t, "pdftotext", "pdfinfo")
	src := filepath.Join(t.TempDir(), "absent.pdf")
	_, err := shrink.Shrinker{}.Shrink(context.Background(), src, filepath.Join(t.TempDir(), "out.pdf"))
	require.Error(t, err)
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o600)
}
