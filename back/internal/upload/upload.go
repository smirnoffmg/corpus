// Package upload adds books, publications and manuals to the library
// directories the indexer walks. Files land in the existing roots rather than in a place of their own:
// the indexer prunes every source of a kind that is missing from that kind's
// root, so a second root for books would be wiped on every pass.
//
// Everything is written through os.Root, which confines each path to its root
// even when a name in an archive says otherwise, and appears in place only once
// complete, because the indexer may walk the directory at any moment.
package upload

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

var (
	ErrInvalid  = errors.New("invalid upload")
	ErrExists   = errors.New("already in the library")
	ErrTooLarge = errors.New("archive too large")
)

// Limits bound what a manual may unpack to. They guard against an archive that
// is small on the wire and enormous on disk; the size of the upload itself is
// the HTTP layer's to limit. Zero values take the defaults.
type Limits struct {
	Entries int   // files and directories in the archive
	Bytes   int64 // total unpacked size
}

// The scikit-learn documentation is 6,495 entries and 265 MB unpacked; the
// defaults leave room for a manual several times that.
const (
	defaultEntries = 100_000
	defaultBytes   = 4 << 30
)

const (
	booksDir = "uploads"
	// tmpDir sits inside the docs root so that renaming out of it is atomic;
	// the indexer does not walk it.
	tmpDir = ".upload-tmp"
)

type Library struct {
	books, docs, papers *os.Root
	limits              Limits
}

func Open(books, docs, papers string, limits Limits) (*Library, error) {
	if limits.Entries <= 0 {
		limits.Entries = defaultEntries
	}
	if limits.Bytes <= 0 {
		limits.Bytes = defaultBytes
	}
	b, err := os.OpenRoot(books)
	if err != nil {
		return nil, fmt.Errorf("books root: %w", err)
	}
	d, err := os.OpenRoot(docs)
	if err != nil {
		b.Close()
		return nil, fmt.Errorf("docs root: %w", err)
	}
	p, err := os.OpenRoot(papers)
	if err != nil {
		b.Close()
		d.Close()
		return nil, fmt.Errorf("papers root: %w", err)
	}
	return &Library{books: b, docs: d, papers: p, limits: limits}, nil
}

func (l *Library) Close() error {
	return errors.Join(l.books.Close(), l.docs.Close(), l.papers.Close())
}

// AddBook saves a PDF, EPUB or FB2 as uploads/<name> under the books root and
// returns that path, which is the path the indexer will give the source.
func (l *Library) AddBook(name string, r io.Reader) (string, error) {
	return addFile(l.books, name, r, pdfFormat, epubFormat, fb2Format)
}

// AddPaper saves a publication the same way, under the papers root. A paper is
// kept apart from the books rather than tagged among them because a pass prunes
// every source of a kind missing from that kind's root.
func (l *Library) AddPaper(name string, r io.Reader) (string, error) {
	return addFile(l.papers, name, r, pdfFormat)
}

// format is a kind of file a shelf takes, known by its extension and by how
// it begins.
type format struct {
	ext    string
	starts func(head []byte) bool
}

var (
	pdfFormat  = format{".pdf", func(h []byte) bool { return string(h) == "%PDF-" }}
	epubFormat = format{".epub", func(h []byte) bool { return string(h[:4]) == "PK\x03\x04" }}
	// FictionBook is XML, often after a byte-order mark.
	fb2Format = format{".fb2", func(h []byte) bool {
		return h[0] == '<' || string(h[:3]) == "\xef\xbb\xbf"
	}}
)

func addFile(root *os.Root, name string, r io.Reader, formats ...format) (string, error) {
	name = baseName(name)
	i := slices.IndexFunc(formats, func(f format) bool { return strings.EqualFold(filepath.Ext(name), f.ext) })
	if i < 0 || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("%w: %q is not a file this shelf takes", ErrInvalid, name)
	}

	// The name says what the file is; the first bytes have to agree, or the
	// reader fails on every pass for as long as the file sits there.
	head := make([]byte, 5)
	if _, err := io.ReadFull(r, head); err != nil || !formats[i].starts(head) {
		return "", fmt.Errorf("%w: %s is not a %s file", ErrInvalid, name, formats[i].ext)
	}

	final := path.Join(booksDir, name)
	if _, err := root.Stat(final); err == nil {
		return "", fmt.Errorf("%w: %s", ErrExists, final)
	}
	if err := root.MkdirAll(booksDir, 0o755); err != nil {
		return "", err
	}

	// A dot name without the book's extension: neither the walker nor a listing
	// in Finder shows it while it is being written.
	part := path.Join(booksDir, "."+name+".part")
	f, err := root.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, io.MultiReader(strings.NewReader(string(head)), r))
	err = errors.Join(err, f.Close())
	if err == nil {
		err = root.Rename(part, final)
	}
	if err != nil {
		_ = root.Remove(part)
		return "", err
	}
	return final, nil
}

// AddManual unpacks a ZIP of built HTML into <name>/ under the docs root and
// returns the manual's directory name.
func (l *Library) AddManual(name string, r io.Reader) (string, error) {
	if name == "" || name != ManualName(name) || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return "", fmt.Errorf("%w: %q is not a manual name", ErrInvalid, name)
	}
	if _, err := l.docs.Stat(name); err == nil {
		return "", fmt.Errorf("%w: %s", ErrExists, name)
	}
	if err := l.docs.MkdirAll(tmpDir, 0o755); err != nil {
		return "", err
	}

	id, err := randomID()
	if err != nil {
		return "", err
	}
	// zip needs random access, so the archive is spooled to disk first — next
	// to where it unpacks, not in memory.
	spool := path.Join(tmpDir, id+".zip")
	defer func() { _ = l.docs.Remove(spool) }()
	size, err := l.spool(spool, r)
	if err != nil {
		return "", err
	}

	staging := path.Join(tmpDir, id)
	if err := l.unpack(spool, size, staging); err != nil {
		_ = l.docs.RemoveAll(staging)
		return "", err
	}
	if err := l.docs.Rename(staging, name); err != nil {
		_ = l.docs.RemoveAll(staging)
		return "", err
	}
	return name, nil
}

func (l *Library) spool(name string, r io.Reader) (int64, error) {
	f, err := l.docs.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	return n, errors.Join(err, f.Close())
}

func (l *Library) unpack(spool string, size int64, staging string) error {
	f, err := l.docs.Open(spool)
	if err != nil {
		return err
	}
	defer f.Close()

	zr, err := zip.NewReader(f, size)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return fmt.Errorf("%w: not a ZIP archive: %w", ErrInvalid, err)
	}
	if len(zr.File) > l.limits.Entries {
		return fmt.Errorf("%w: %d entries, the limit is %d", ErrTooLarge, len(zr.File), l.limits.Entries)
	}

	prefix := commonDir(zr.File)
	budget := l.limits.Bytes
	pages := 0
	for _, zf := range zr.File {
		rel := strings.TrimPrefix(zf.Name, prefix)
		if rel == "" {
			continue
		}
		if strings.Contains(rel, `\`) || !filepath.IsLocal(rel) {
			return fmt.Errorf("%w: %q points outside the manual", ErrInvalid, zf.Name)
		}
		mode := zf.Mode()
		if mode.IsDir() {
			if err := l.docs.MkdirAll(path.Join(staging, rel), 0o755); err != nil {
				return err
			}
			continue
		}
		if !mode.IsRegular() {
			return fmt.Errorf("%w: %q is not a regular file", ErrInvalid, zf.Name)
		}

		written, err := l.extract(zf, path.Join(staging, rel), budget)
		if err != nil {
			return err
		}
		budget -= written
		if strings.EqualFold(path.Ext(rel), ".html") {
			pages++
		}
	}
	if pages == 0 {
		return fmt.Errorf("%w: the archive holds no HTML pages", ErrInvalid)
	}
	return nil
}

// extract copies one entry, counting what is actually inflated rather than
// what the header claims: the header is written by whoever made the archive.
func (l *Library) extract(zf *zip.File, dst string, budget int64) (int64, error) {
	if err := l.docs.MkdirAll(path.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	src, err := zf.Open()
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %w", ErrInvalid, zf.Name, err)
	}
	defer src.Close()

	out, err := l.docs.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return 0, fmt.Errorf("%w: %q appears twice", ErrInvalid, zf.Name)
		}
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(src, budget+1))
	err = errors.Join(err, out.Close())
	if err != nil {
		return n, fmt.Errorf("%w: %s: %w", ErrInvalid, zf.Name, err)
	}
	if n > budget {
		return n, fmt.Errorf("%w: unpacks to more than %d bytes", ErrTooLarge, l.limits.Bytes)
	}
	return n, nil
}

// commonDir is the one top-level directory every entry sits in, if there is
// one: a ZIP of a site is often that site's directory, and the manual is what
// is inside it.
func commonDir(files []*zip.File) string {
	var top string
	for _, f := range files {
		first, _, nested := strings.Cut(f.Name, "/")
		if !nested || first == "" || first == "." || first == ".." {
			return ""
		}
		if top == "" {
			top = first
		} else if first != top {
			return ""
		}
	}
	if top == "" {
		return ""
	}
	return top + "/"
}

// ManualName turns a file name into a manual's directory name: the name is what
// every citation from the manual shows, and it is also a path on disk, so it is
// kept to letters, digits, dots, dashes and underscores.
func ManualName(name string) string {
	name = baseName(name)
	if strings.EqualFold(filepath.Ext(name), ".zip") {
		name = name[:len(name)-len(".zip")]
	}
	var b strings.Builder
	dash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
			dash = r == '-'
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-.")
}

// baseName drops any directory part a browser or a client put into the file
// name, in either separator.
func baseName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSpace(name)
}

func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
