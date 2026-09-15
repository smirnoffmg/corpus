// Package ocr recognises the text of scanned PDFs, a page at a time, and keeps
// what it recognised. A scan has no text layer, so without it a book is a file
// search never sees — a tenth of this library was.
//
// Recognition is slow — seconds a page, an hour for a big book on a laptop —
// so every page is cached as soon as it is read, under the file's content hash:
// an interrupted book resumes where it stopped, a renamed one is not read again,
// and rebuilding the database costs no recognition at all.
package ocr

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"golang.org/x/sync/errgroup"
)

// Engine reads a PDF's page count and recognises one page.
type Engine interface {
	Pages(ctx context.Context, pdf string) (int, error)
	Recognize(ctx context.Context, pdf string, page int) (string, error)
}

// Cache keeps recognised pages as one text file each: <dir>/<hash>/<page>.txt.
type Cache struct {
	Dir string
}

var hashPattern = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

func (c Cache) pagePath(hash string, page int) (string, error) {
	if !hashPattern.MatchString(hash) {
		return "", fmt.Errorf("ocr cache key %q is not a content hash", hash)
	}
	return filepath.Join(c.Dir, hash, fmt.Sprintf("%05d.txt", page)), nil
}

// Save writes one page's text atomically: a reader, or a crash, never meets
// half a page taken for a whole one.
func (c Cache) Save(hash string, page int, text string) error {
	path, err := c.pagePath(hash, page)
	if err != nil {
		return err
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
		return mkErr
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".page-*")
	if err != nil {
		return err
	}
	_, err = tmp.WriteString(text)
	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// Missing lists the pages, from 1, not yet recognised.
func (c Cache) Missing(hash string, pages int) []int {
	var missing []int
	for p := 1; p <= pages; p++ {
		path, err := c.pagePath(hash, p)
		if err != nil {
			return nil
		}
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			missing = append(missing, p)
		}
	}
	return missing
}

// Service recognises scans with an engine into a cache.
type Service struct {
	Engine  Engine
	Cache   Cache
	Workers int // pages recognised at once
}

func (s Service) Pages(ctx context.Context, pdf string) (int, error) {
	return s.Engine.Pages(ctx, pdf)
}

// Cached returns every page of a recognised scan, in order, or false while any
// page is still missing.
func (s Service) Cached(hash string, pages int) ([]string, bool) {
	out := make([]string, pages)
	for p := 1; p <= pages; p++ {
		path, err := s.Cache.pagePath(hash, p)
		if err != nil {
			return nil, false
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return nil, false
		}
		out[p-1] = string(text)
	}
	return out, true
}

// Recognize reads the pages of a scan not yet in the cache, several at once,
// and reports whether the book is now complete. It gives way between pages when
// stop delivers — an upload waiting to be indexed matters more than the next
// page of a book already queued — and returns false with no error. progress is
// called with the number of pages recognised so far, cached ones included.
func (s Service) Recognize(ctx context.Context, hash, pdf string, pages int, stop <-chan struct{}, progress func(int)) (bool, error) {
	missing := s.Cache.Missing(hash, pages)
	done := pages - len(missing)
	if len(missing) == 0 {
		return true, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu      sync.Mutex
		stopped bool
	)
	next := make(chan int)
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		defer close(next)
		for _, page := range missing {
			select {
			case <-stop:
				mu.Lock()
				stopped = true
				mu.Unlock()
				return nil
			case <-gctx.Done():
				return nil
			case next <- page:
			}
		}
		return nil
	})

	workers := max(s.Workers, 1)
	for range workers {
		g.Go(func() error {
			for page := range next {
				text, err := s.Engine.Recognize(gctx, pdf, page)
				if err != nil {
					return fmt.Errorf("recognise page %d of %s: %w", page, pdf, err)
				}
				if err := s.Cache.Save(hash, page, text); err != nil {
					return err
				}
				mu.Lock()
				done++
				n := done
				mu.Unlock()
				if progress != nil {
					progress(n)
				}
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	mu.Lock()
	defer mu.Unlock()
	return !stopped && done == pages, nil
}
