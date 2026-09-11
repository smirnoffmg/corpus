package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smirnoffmg/corpus/internal/extract"
	"github.com/smirnoffmg/corpus/internal/lang"
	"github.com/smirnoffmg/corpus/internal/store"
)

func main() {
	var (
		booksDir = flag.String("books", "/data/books", "directory with PDF books")
		vaultDir = flag.String("vault", "/data/vault", "Obsidian vault root")
		ddlPath  = flag.String("migrations", "/app/migrations/001_init.sql", "schema file")
		interval = flag.Duration("interval", 0, "reindex period; 0 means index once and exit")
	)
	flag.Parse()

	ctx := context.Background()
	st, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer st.Close()

	ddl, err := os.ReadFile(*ddlPath)
	if err != nil {
		log.Fatalf("read migrations: %v", err)
	}
	if err := st.Migrate(ctx, string(ddl)); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	for {
		if err := indexAll(ctx, st, *booksDir, *vaultDir); err != nil {
			log.Printf("index: %v", err)
		}
		if *interval == 0 {
			return
		}
		time.Sleep(*interval)
	}
}

func indexAll(ctx context.Context, st *store.Store, booksDir, vaultDir string) error {
	start := time.Now()

	books, err := indexKind(ctx, st, "book", booksDir, ".pdf", func(path string) ([]extract.Chunk, error) {
		return extract.PDF(ctx, path)
	})
	if err != nil {
		return err
	}

	notes, err := indexKind(ctx, st, "vault", vaultDir, ".md", extract.Markdown)
	if err != nil {
		return err
	}

	sources, chunks, err := st.Stats(ctx)
	if err != nil {
		return err
	}
	log.Printf("indexed %d books, %d notes in %s; corpus: %d sources, %d chunks",
		books, notes, time.Since(start).Round(time.Second), sources, chunks)
	return nil
}

func indexKind(
	ctx context.Context,
	st *store.Store,
	kind, root, ext string,
	parse func(string) ([]extract.Chunk, error),
) (int, error) {
	var seen []string
	updated := 0

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ext) {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		seen = append(seen, rel)

		hash, err := hashFile(path)
		if err != nil {
			return err
		}
		unchanged, err := st.Unchanged(ctx, rel, hash)
		if err != nil || unchanged {
			return err
		}

		chunks, err := parse(path)
		if err != nil {
			log.Printf("skip %s: %v", rel, err)
			return nil
		}
		for i := range chunks {
			chunks[i].Lang = lang.Detect(chunks[i].Body)
		}

		src := store.Source{
			Kind:  kind,
			Path:  rel,
			Title: strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel)),
			Hash:  hash,
		}
		if err := st.Replace(ctx, src, chunks); err != nil {
			return err
		}
		updated++
		return nil
	})
	if err != nil {
		return updated, err
	}

	if _, err := st.Prune(ctx, kind, seen); err != nil {
		return updated, err
	}
	return updated, nil
}

func skipDir(name string) bool {
	return name == ".git" || name == ".obsidian" || name == ".trash" || name == "node_modules"
}

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
