package upload_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smirnoffmg/corpus/internal/upload"
)

const pdf = "%PDF-1.7\n%âãÏÓ\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n"

func library(t *testing.T, limits upload.Limits) (lib *upload.Library, books, docs, papers string) {
	t.Helper()
	books, docs, papers = t.TempDir(), t.TempDir(), t.TempDir()
	lib, err := upload.Open(books, docs, papers, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	return lib, books, docs, papers
}

type entry struct {
	name, body string
	mode       fs.FileMode
}

func archive(t *testing.T, entries ...entry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

// tree lists every file under dir, so a test can say "nothing else was written".
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestAddBookSavesUnderUploads(t *testing.T) {
	lib, books, _, _ := library(t, upload.Limits{})

	path, err := lib.AddBook("Мартин Клеппман — Высоконагруженные приложения.pdf", strings.NewReader(pdf))
	if err != nil {
		t.Fatal(err)
	}
	if want := "uploads/Мартин Клеппман — Высоконагруженные приложения.pdf"; path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	got, err := os.ReadFile(filepath.Join(books, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != pdf {
		t.Error("saved bytes differ from the upload")
	}
	if files := tree(t, books); len(files) != 1 {
		t.Errorf("books holds %v, want only the book", files)
	}
}

func TestAddPaperSavesUnderUploadsOfItsOwnRoot(t *testing.T) {
	lib, books, _, papers := library(t, upload.Limits{})

	path, err := lib.AddPaper("Dean & Ghemawat — MapReduce.pdf", strings.NewReader(pdf))
	if err != nil {
		t.Fatal(err)
	}
	if want := "uploads/Dean & Ghemawat — MapReduce.pdf"; path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(filepath.Join(papers, filepath.FromSlash(path))); err != nil {
		t.Fatal(err)
	}
	if files := tree(t, books); len(files) != 0 {
		t.Errorf("books holds %v, want a paper to stay out of the shelf", files)
	}
}

func TestAddPaperRejectsWhatIsNotAPDF(t *testing.T) {
	lib, _, _, _ := library(t, upload.Limits{})

	if _, err := lib.AddPaper("paper.txt", strings.NewReader(pdf)); !errors.Is(err, upload.ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
	if _, err := lib.AddPaper("paper.pdf", strings.NewReader("not a pdf at all")); !errors.Is(err, upload.ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

func TestAddBookTakesEbooks(t *testing.T) {
	lib, books, _, _ := library(t, upload.Limits{})

	epub := archive(t, entry{name: "mimetype", body: "application/epub+zip"}).String()
	fb2 := "\ufeff<?xml version=\"1.0\" encoding=\"windows-1251\"?><FictionBook/>"
	for name, body := range map[string]string{"Зов предков.epub": epub, "Детство Никиты.fb2": fb2} {
		path, err := lib.AddBook(name, strings.NewReader(body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, err := os.ReadFile(filepath.Join(books, filepath.FromSlash(path))); err != nil || string(got) != body {
			t.Errorf("%s: saved %q, %v", name, got, err)
		}
	}
}

func TestAddPaperTakesOnlyAPDF(t *testing.T) {
	lib, _, _, _ := library(t, upload.Limits{})

	epub := archive(t, entry{name: "mimetype", body: "application/epub+zip"}).String()
	if _, err := lib.AddPaper("paper.epub", strings.NewReader(epub)); !errors.Is(err, upload.ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

func TestAddBookKeepsOnlyTheBaseName(t *testing.T) {
	lib, books, _, _ := library(t, upload.Limits{})

	for _, name := range []string{"../../etc/evil.pdf", `C:\Users\me\evil.pdf`} {
		path, err := lib.AddBook(name, strings.NewReader(pdf))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if path != "uploads/evil.pdf" {
			t.Errorf("%s saved as %q, want uploads/evil.pdf", name, path)
		}
		if err := os.Remove(filepath.Join(books, "uploads", "evil.pdf")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAddBookRejects(t *testing.T) {
	cases := map[string]struct {
		name, body string
		want       error
	}{
		"not a pdf by content": {"book.pdf", "<html>not a pdf</html>", upload.ErrInvalid},
		"not a book by name":   {"book.txt", pdf, upload.ErrInvalid},
		"an epub by name only": {"book.epub", pdf, upload.ErrInvalid},
		"an fb2 by name only":  {"book.fb2", pdf, upload.ErrInvalid},
		"hidden name":          {".book.pdf", pdf, upload.ErrInvalid},
		"no name":              {".pdf", pdf, upload.ErrInvalid},
		"empty":                {"book.pdf", "", upload.ErrInvalid},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			lib, books, _, _ := library(t, upload.Limits{})
			if _, err := lib.AddBook(c.name, strings.NewReader(c.body)); !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
			if files := tree(t, books); len(files) != 0 {
				t.Errorf("a refused upload left %v behind", files)
			}
		})
	}
}

func TestAddBookRefusesToOverwrite(t *testing.T) {
	lib, books, _, _ := library(t, upload.Limits{})
	if _, err := lib.AddBook("book.pdf", strings.NewReader(pdf)); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddBook("book.pdf", strings.NewReader(pdf+"changed")); !errors.Is(err, upload.ErrExists) {
		t.Errorf("err = %v, want ErrExists", err)
	}
	got, _ := os.ReadFile(filepath.Join(books, "uploads", "book.pdf"))
	if string(got) != pdf {
		t.Error("the existing book was overwritten")
	}
}

type failingReader struct {
	r     io.Reader
	after int
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.after <= 0 {
		return 0, errors.New("connection reset")
	}
	if len(p) > f.after {
		p = p[:f.after]
	}
	n, err := f.r.Read(p)
	f.after -= n
	return n, err
}

// The indexer walks the library while uploads arrive, so a book must appear
// whole or not at all.
func TestAnInterruptedBookUploadLeavesNothing(t *testing.T) {
	lib, books, _, _ := library(t, upload.Limits{})
	body := &failingReader{r: strings.NewReader(pdf + strings.Repeat("x", 1<<16)), after: 1 << 12}

	if _, err := lib.AddBook("book.pdf", body); err == nil {
		t.Fatal("want the read error")
	}
	if files := tree(t, books); len(files) != 0 {
		t.Errorf("an interrupted upload left %v", files)
	}
}

func TestAddManualUnpacksIntoItsOwnDirectory(t *testing.T) {
	lib, _, docs, _ := library(t, upload.Limits{})
	zipped := archive(t,
		entry{name: "index.html", body: "<h1>NLTK</h1>"},
		entry{name: "howto/"},
		entry{name: "howto/tokenize.html", body: "<h1>Tokenize</h1>"},
		entry{name: "_static/basic.css", body: "body{}"},
	)

	manual, err := lib.AddManual("nltk", zipped)
	if err != nil {
		t.Fatal(err)
	}
	if manual != "nltk" {
		t.Errorf("manual = %q, want nltk", manual)
	}
	got := tree(t, docs)
	want := []string{"nltk/_static/basic.css", "nltk/howto/tokenize.html", "nltk/index.html"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("docs holds %v, want %v — and nothing left over from unpacking", got, want)
	}
}

// A ZIP of a site is often the site's directory itself; the manual is its
// contents, not a directory holding it.
func TestAddManualStripsASingleTopLevelDirectory(t *testing.T) {
	lib, _, docs, _ := library(t, upload.Limits{})
	zipped := archive(t,
		entry{name: "nltk.github.com-main/index.html", body: "<h1>NLTK</h1>"},
		entry{name: "nltk.github.com-main/howto/tokenize.html", body: "<h1>Tokenize</h1>"},
	)

	if _, err := lib.AddManual("nltk", zipped); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(docs, "nltk", "index.html")); err != nil {
		t.Errorf("index.html is not at the manual's root: %v (tree %v)", err, tree(t, docs))
	}
}

func TestAddManualRejects(t *testing.T) {
	cases := map[string]struct {
		manual  string
		entries []entry
		limits  upload.Limits
		want    error
	}{
		"escaping path":      {"m", []entry{{name: "index.html", body: "x"}, {name: "../../outside.html", body: "x"}}, upload.Limits{}, upload.ErrInvalid},
		"absolute path":      {"m", []entry{{name: "/etc/passwd.html", body: "x"}}, upload.Limits{}, upload.ErrInvalid},
		"symlink":            {"m", []entry{{name: "index.html", body: "x"}, {name: "link.html", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777}}, upload.Limits{}, upload.ErrInvalid},
		"no html":            {"m", []entry{{name: "readme.txt", body: "x"}}, upload.Limits{}, upload.ErrInvalid},
		"too many entries":   {"m", []entry{{name: "a.html", body: "x"}, {name: "b.html", body: "x"}, {name: "c.html", body: "x"}}, upload.Limits{Entries: 2}, upload.ErrTooLarge},
		"too large unpacked": {"m", []entry{{name: "a.html", body: strings.Repeat("x", 4096)}}, upload.Limits{Bytes: 1024}, upload.ErrTooLarge},
		"bad manual name":    {"../books", []entry{{name: "index.html", body: "x"}}, upload.Limits{}, upload.ErrInvalid},
		"hidden manual":      {".upload-tmp", []entry{{name: "index.html", body: "x"}}, upload.Limits{}, upload.ErrInvalid},
		"sphinx dir name":    {"_static", []entry{{name: "index.html", body: "x"}}, upload.Limits{}, upload.ErrInvalid},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			lib, _, docs, _ := library(t, c.limits)
			if _, err := lib.AddManual(c.manual, archive(t, c.entries...)); !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
			if files := tree(t, docs); len(files) != 0 {
				t.Errorf("a refused manual left %v behind", files)
			}
		})
	}
}

func TestAddManualRejectsWhatIsNotAZip(t *testing.T) {
	lib, _, docs, _ := library(t, upload.Limits{})
	if _, err := lib.AddManual("m", strings.NewReader("definitely not a zip")); !errors.Is(err, upload.ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
	if files := tree(t, docs); len(files) != 0 {
		t.Errorf("left %v behind", files)
	}
}

func TestAddManualRefusesAnExistingManual(t *testing.T) {
	lib, _, docs, _ := library(t, upload.Limits{})
	if err := os.MkdirAll(filepath.Join(docs, "nltk"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddManual("nltk", archive(t, entry{name: "index.html", body: "x"})); !errors.Is(err, upload.ErrExists) {
		t.Errorf("err = %v, want ErrExists", err)
	}
}

func TestManualName(t *testing.T) {
	cases := map[string]string{
		"scikit-learn-docs.zip":  "scikit-learn-docs",
		"NLTK Book (2nd ed).zip": "NLTK-Book-2nd-ed",
		"numpy_1.26":             "numpy_1.26",
		"":                       "",
		"…":                      "",
	}
	for in, want := range cases {
		if got := upload.ManualName(in); got != want {
			t.Errorf("ManualName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpenRequiresEveryRoot(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent")
	for name, roots := range map[string][3]string{
		"books":  {absent, t.TempDir(), t.TempDir()},
		"docs":   {t.TempDir(), absent, t.TempDir()},
		"papers": {t.TempDir(), t.TempDir(), absent},
	} {
		if _, err := upload.Open(roots[0], roots[1], roots[2], upload.Limits{}); err == nil {
			t.Errorf("want an error for a missing %s root", name)
		}
	}
}
