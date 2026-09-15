// Package bibfile keeps the bibliography in files beside the library, as its
// system of record. Everything else in the database is derived from files and
// rebuilt by the indexer; the bibliographic descriptions are checked by hand,
// so they would be the one thing a lost database volume took with it. "If you
// lose derived data, you can re-create it from the original source" (DDIA,
// с. 376) — the file makes the descriptions original data again, and the table
// an index of it.
//
// The table is written first, as before, and exported after every change;
// the indexer imports the file on every pass, so a restored or hand-edited
// file reaches the table. Of a description changed on both sides, the newer
// wins.
package bibfile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// Store is the bibliography table.
type Store interface {
	// WithBibliography runs fn on a snapshot taken under a lock every exporter
	// shares, so two processes exporting at once cannot leave the older
	// snapshot on disk.
	WithBibliography(ctx context.Context, fn func([]corpus.Reference, []corpus.StyleXML) error) error
	ImportReferences(ctx context.Context, refs []corpus.Reference) (int64, error)
	ImportStyles(ctx context.Context, styles []corpus.StyleXML) (int64, error)
}

const (
	referencesFile = "references.json"
	stylesDir      = "styles"
)

type File struct {
	store Store
	dir   string
}

func New(store Store, dir string) *File { return &File{store: store, dir: dir} }

type document struct {
	Note       string             `json:"note"`
	References []corpus.Reference `json:"references"`
	// Styles lists the added citation styles; each one's CSL is in
	// styles/<id>.csl, as the file a CSL tool would take.
	Styles []styleEntry `json:"styles"`
}

type styleEntry struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

const note = "Bibliographic descriptions of corpus. This file is their system of record: " +
	"the database is rebuilt from it. Edit it by hand if you like; the newer side of a description wins."

// Export writes the table to the files. A file whose content would not change
// is left alone.
func (f *File) Export(ctx context.Context) error {
	return f.store.WithBibliography(ctx, func(refs []corpus.Reference, styles []corpus.StyleXML) error {
		sort.Slice(refs, func(i, j int) bool { return refs[i].CiteKey < refs[j].CiteKey })
		sort.Slice(styles, func(i, j int) bool { return styles[i].ID < styles[j].ID })
		entries := make([]styleEntry, len(styles))
		for i, st := range styles {
			entries[i] = styleEntry{ID: st.ID, Title: st.Title}
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(document{Note: note, References: refs, Styles: entries}); err != nil {
			return err
		}
		if err := writeIfChanged(f.dir, referencesFile, buf.Bytes()); err != nil {
			return err
		}
		for _, s := range styles {
			if err := writeIfChanged(filepath.Join(f.dir, stylesDir), s.ID+".csl", []byte(s.XML)); err != nil {
				return err
			}
		}
		return nil
	})
}

// Import applies the files to the table and reports how many descriptions and
// styles it changed. Without a file there is nothing to apply.
func (f *File) Import(ctx context.Context) (int, error) {
	raw, err := os.ReadFile(filepath.Join(f.dir, referencesFile))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var doc document
	if jsonErr := json.Unmarshal(raw, &doc); jsonErr != nil {
		return 0, fmt.Errorf("%s is not valid: %w", filepath.Join(f.dir, referencesFile), jsonErr)
	}
	refs, err := f.store.ImportReferences(ctx, doc.References)
	if err != nil {
		return 0, err
	}

	styles, err := readStyles(filepath.Join(f.dir, stylesDir), doc.Styles)
	if err != nil {
		return int(refs), err
	}
	n, err := f.store.ImportStyles(ctx, styles)
	return int(refs + n), err
}

// readStyles reads each listed style's CSL. A style listed without its file is
// skipped rather than imported empty.
func readStyles(dir string, listed []styleEntry) ([]corpus.StyleXML, error) {
	var out []corpus.StyleXML
	for _, entry := range listed {
		if entry.ID == "" || strings.ContainsAny(entry.ID, `/\`) || strings.HasPrefix(entry.ID, ".") {
			return nil, fmt.Errorf("style id %q is not a file name", entry.ID)
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.ID+".csl"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, corpus.StyleXML{ID: entry.ID, Title: entry.Title, XML: string(body)})
	}
	return out, nil
}

// writeIfChanged replaces a file atomically: written beside it under a dot name
// and renamed, so a reader — or a crash — never meets half a bibliography.
func writeIfChanged(dir, name string, content []byte) error {
	path := filepath.Join(dir, name)
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(content)
	if err == nil {
		err = tmp.Sync()
	}
	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
