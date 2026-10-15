package shrink

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Store moves what the database files under a content hash.
type Store interface {
	Rehash(ctx context.Context, kind, path, oldHash, newHash string) error
}

// Reshelver shrinks a file already in the library. A new file only changes its
// bytes; one that is shelved has a content hash that the source, its scan, its
// description, its reference list and its recognised pages are all filed under,
// and those move to the new hash along with it.
//
// Nothing may index the library meanwhile: a pass between the file and the
// database moving would take the file for a new book.
type Reshelver struct {
	Shrinker Shrinker
	Store    Store
	OCR      string                          // recognised-page cache; empty when there is none
	Export   func(ctx context.Context) error // writes the bibliography file; nil when there is none
	DryRun   bool                            // shrink to a temporary file, report and keep nothing
}

// File shrinks the PDF at rel under root. On any failure past the shrink the
// original is put back.
func (r Reshelver) File(ctx context.Context, kind, root, rel string) (Result, error) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	dir, name := filepath.Split(path)
	// Dot names: the indexer does not walk them.
	small := filepath.Join(dir, "."+name+".small")
	backup := filepath.Join(dir, "."+name+".orig")

	res, err := r.Shrinker.Shrink(ctx, path, small)
	if err != nil || !res.Kept || r.DryRun {
		_ = os.Remove(small)
		return res, err
	}

	oldHash, err := hashFile(path)
	if err != nil {
		return res, errors.Join(err, os.Remove(small))
	}
	newHash, err := hashFile(small)
	if err != nil {
		return res, errors.Join(err, os.Remove(small))
	}
	copied, err := r.copyCache(oldHash, newHash)
	if err != nil {
		return res, errors.Join(err, os.Remove(small))
	}
	undo := func(cause error) (Result, error) {
		errs := []error{cause}
		if copied != "" {
			errs = append(errs, os.RemoveAll(copied))
		}
		return res, errors.Join(errs...)
	}

	if err := os.Rename(path, backup); err != nil {
		return undo(errors.Join(err, os.Remove(small)))
	}
	if err := os.Rename(small, path); err != nil {
		return undo(errors.Join(err, os.Remove(small), os.Rename(backup, path)))
	}
	if err := r.Store.Rehash(ctx, kind, rel, oldHash, newHash); err != nil {
		return undo(errors.Join(fmt.Errorf("%s: %w", rel, err), os.Rename(backup, path)))
	}
	if err := os.Remove(backup); err != nil {
		return res, err
	}
	if r.Export != nil {
		if err := r.Export(ctx); err != nil {
			return res, fmt.Errorf("export the bibliography: %w", err)
		}
	}
	return res, nil
}

// copyCache copies a scan's recognised pages to the new hash and returns the
// directory it made, or "" when there was nothing to copy. The old pages stay
// until the move is committed; an abandoned copy would be a cache under a hash
// no file has.
func (r Reshelver) copyCache(oldHash, newHash string) (string, error) {
	if r.OCR == "" {
		return "", nil
	}
	from, to := filepath.Join(r.OCR, oldHash), filepath.Join(r.OCR, newHash)
	pages, err := os.ReadDir(from)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// Already there, under a file uploaded shrunk: not this move's to undo.
	if _, err := os.Stat(to); err == nil {
		return "", nil
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		return "", err
	}
	for _, p := range pages {
		if p.IsDir() {
			continue
		}
		if err := copyFile(filepath.Join(from, p.Name()), filepath.Join(to, p.Name())); err != nil {
			return to, errors.Join(err, os.RemoveAll(to))
		}
	}
	return to, nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}

// hashFile is the content hash the indexer files a source under.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
