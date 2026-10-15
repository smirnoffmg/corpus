// Command shrink re-encodes the images of the PDFs already in the library, and
// moves what is filed under each file's content hash to the new one. Uploads
// are shrunk by mcpd as they arrive; this is for the files that came before.
//
// The indexer has to be stopped: a pass between a file and the database moving
// would take the file for a new book. Run it through compose, which mounts the
// library writable for it:
//
//	docker compose stop indexer
//	docker compose --profile tools run --rm shrink --dry-run
//	docker compose --profile tools run --rm shrink
//	docker compose start indexer
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/smirnoffmg/corpus/internal/bibfile"
	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/shrink"
	"github.com/smirnoffmg/corpus/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := run(); err != nil {
		slog.Error("exit", "err", err)
		os.Exit(1)
	}
}

type shelf struct{ kind, root string }

func run() error {
	books := flag.String("books", "/data/books", "directory with books")
	papers := flag.String("papers", "/data/papers", "directory with publications; empty skips them")
	ocrDir := flag.String("ocr", "/data/ocr", "cache of recognised pages; empty when there is none")
	bibDir := flag.String("bibliography", "/data/bibliography", "directory of the bibliography file; empty keeps descriptions in the database alone")
	dryRun := flag.Bool("dry-run", false, "shrink each file to a temporary copy and report what would be saved, changing nothing")
	only := flag.String("only", "", "shrink only files whose path in the library starts with this")
	textDPI := flag.Int("text-dpi", 150, "resolution images of a PDF with a text layer are taken down to")
	scanDPI := flag.Int("scan-dpi", 300, "resolution images of a scan are taken down to")
	minGain := flag.Float64("min-gain", 0.1, "share of its size a file must lose to be replaced")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer st.Close()

	r := shrink.Reshelver{
		Shrinker: shrink.Shrinker{TextDPI: *textDPI, ScanDPI: *scanDPI, MinGain: *minGain},
		Store:    st,
		OCR:      *ocrDir,
		DryRun:   *dryRun,
	}
	if *bibDir != "" {
		r.Export = bibfile.New(st, *bibDir).Export
	}

	var shelves []shelf
	for _, s := range []shelf{{"book", *books}, {"paper", *papers}} {
		if s.root != "" {
			shelves = append(shelves, s)
		}
	}
	type file struct {
		shelf
		rel string
	}
	var files []file
	for _, s := range shelves {
		rels, err := pdfs(s.root, *only)
		if err != nil {
			return err
		}
		for _, rel := range rels {
			files = append(files, file{s, rel})
		}
	}

	var before, after int64
	var replaced, failed int
	for i, f := range files {
		if ctx.Err() != nil {
			break
		}
		// Checked before every file, not once: an indexer started halfway
		// would index a file between its replacement and the move.
		if !*dryRun {
			if err := indexerStopped(ctx, st); err != nil {
				return err
			}
		}
		start := time.Now()
		res, err := r.File(ctx, f.kind, f.root, f.rel)
		prefix := fmt.Sprintf("[%d/%d] %s", i+1, len(files), f.rel)
		switch {
		case err != nil:
			failed++
			fmt.Printf("%s: FAILED, left as it was: %v\n", prefix, err)
			continue
		case !res.Kept:
			fmt.Printf("%s: kept, %s (%s)\n", prefix, res.Reason, mb(res.Before))
			continue
		}
		replaced++
		before += res.Before
		after += res.After
		fmt.Printf("%s: %s → %s (−%.0f%%) in %s\n", prefix, mb(res.Before), mb(res.After),
			100*(1-float64(res.After)/float64(res.Before)), time.Since(start).Round(time.Second))
	}

	verb := "replaced"
	if *dryRun {
		verb = "would replace"
	}
	fmt.Printf("\n%s %d of %d PDFs: %s → %s, %s saved; %d failed\n",
		verb, replaced, len(files), mb(before), mb(after), mb(before-after), failed)
	return ctx.Err()
}

// pdfs lists the PDFs under root, relative to it, in path order. Dot names are
// files being written and directories the indexer skips.
func pdfs(root, only string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".pdf") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); strings.HasPrefix(rel, only) {
			out = append(out, rel)
		}
		return nil
	})
	slices.Sort(out)
	return out, err
}

var errIndexerRunning = errors.New("the indexer is running: stop it first (docker compose stop indexer)")

func indexerStopped(ctx context.Context, st *store.Store) error {
	p, found, err := st.Pulse(ctx)
	if err != nil {
		return fmt.Errorf("read the indexer's beat: %w", err)
	}
	// A few missed beats, not one, make a stopped indexer.
	if found && p.Now.Sub(p.At) < 3*corpus.BeatEvery {
		return errIndexerRunning
	}
	return nil
}

func mb(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }
