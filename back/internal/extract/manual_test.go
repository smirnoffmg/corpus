package extract_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smirnoffmg/corpus/internal/extract"
)

func manualDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

var accessed = time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)

func TestManualDraftReadsASphinxIndexPage(t *testing.T) {
	dir := manualDir(t, map[string]string{"index.html": `<html><head>
<title>scikit-learn: machine learning in Python &#8212; scikit-learn 1.9.1 documentation</title>
<link rel="canonical" href="https://scikit-learn.org/stable/index.html" />
</head><body><article><h1>scikit-learn</h1></article>
<footer><p>&#169; Copyright 2007 - 2026, scikit-learn developers (BSD License).</p></footer></body></html>`})

	d := extract.ManualDraft(dir, "scikit-learn", accessed)
	for field, want := range map[string]any{
		"type": "webpage", "title": "scikit-learn 1.9.1 documentation", "version": "1.9.1",
		"URL": "https://scikit-learn.org/stable/", "publisher": "scikit-learn developers",
	} {
		if d[field] != want {
			t.Errorf("%s = %v, want %v", field, d[field], want)
		}
	}
	accessedAt, _ := d["accessed"].(map[string]any)
	dateParts, _ := accessedAt["date-parts"].([]any)
	parts, _ := dateParts[0].([]any)
	if parts[0] != 2026 || parts[1] != 9 || parts[2] != 14 {
		t.Errorf("accessed = %v", parts)
	}
}

func TestManualDraftFallsBackToTheCNAME(t *testing.T) {
	dir := manualDir(t, map[string]string{
		"index.html": `<html><head><title>NLTK :: Natural Language Toolkit</title></head><body><div class="copyright">&copy; 2025, NLTK Project.</div></body></html>`,
		"CNAME":      "www.nltk.org\n",
	})
	d := extract.ManualDraft(dir, "nltk", accessed)
	if d["URL"] != "https://www.nltk.org/" || d["title"] != "NLTK : Natural Language Toolkit" || d["publisher"] != "NLTK Project" {
		t.Errorf("draft = %v", d)
	}
}

func TestManualDraftWithoutAnIndexNamesTheDirectory(t *testing.T) {
	d := extract.ManualDraft(t.TempDir(), "numpy", accessed)
	if d["title"] != "numpy" || d["URL"] != nil {
		t.Errorf("draft = %v", d)
	}
}
