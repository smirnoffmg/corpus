package extract_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/extract"
)

func htmlChunks(t *testing.T, sp extract.Splitter, path string) []corpus.Chunk {
	t.Helper()
	chunks, err := sp.HTML(path)
	if err != nil {
		t.Fatal(err)
	}
	return chunks
}

func writeHTML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "page.html")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHTMLSplitsSphinxPageBySection(t *testing.T) {
	chunks := htmlChunks(t, extract.DefaultNoteSplitter, "testdata/sphinx-pydata.html")
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3: %+v", len(chunks), chunks)
	}

	want := []struct{ heading, anchor string }{
		{"1.4. Support Vector Machines", "support-vector-machines"},
		{"1.4. Support Vector Machines > 1.4.1. Classification", "classification"},
		{"1.4. Support Vector Machines > 1.4.1. Classification > 1.4.1.1. Multi-class classification", "multi-class-classification"},
	}
	for i, w := range want {
		if chunks[i].Heading != w.heading {
			t.Errorf("chunk %d heading = %q, want %q", i, chunks[i].Heading, w.heading)
		}
		if chunks[i].Anchor != w.anchor {
			t.Errorf("chunk %d anchor = %q, want %q", i, chunks[i].Anchor, w.anchor)
		}
		if chunks[i].Ord != i+1 {
			t.Errorf("chunk %d ord = %d, want %d", i, chunks[i].Ord, i+1)
		}
	}
	if got, want := chunks[0].Body, "This page is a fixture shaped like a manual chapter about classifiers."; got != want {
		t.Errorf("first body = %q, want %q", got, want)
	}
}

func TestHTMLKeepsCodeAsFencedBlock(t *testing.T) {
	chunks := htmlChunks(t, extract.DefaultNoteSplitter, "testdata/sphinx-pydata.html")
	want := "SVC stands in for a class the section describes.\n\n" +
		"```\n>>> from sklearn import svm\n>>> clf = svm.SVC()\n```"
	if got := chunks[1].Body; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestHTMLDropsNavigationAndChrome(t *testing.T) {
	for _, fixture := range []string{"sphinx-pydata.html", "sphinx-classic.html", "docutils-book.html"} {
		t.Run(fixture, func(t *testing.T) {
			var all strings.Builder
			for _, c := range htmlChunks(t, extract.DefaultNoteSplitter, filepath.Join("testdata", fixture)) {
				fmt.Fprintf(&all, "%s\n%s\n", c.Heading, c.Body)
			}
			text := all.String()
			for _, noise := range []string{"¶", "Copyright", "Table of Contents", "User Guide", "On this page", "Linear Models", "copy_doctest", "DOCUMENTATION_OPTIONS", "mode: rst", "margin", "System Message", "finegan2007", "api module", "[source]", "sk-container", "fit_intercept", "Documentation for", "running time", "Download Jupyter", "Sphinx-Gallery"} {
				if strings.Contains(text, noise) {
					t.Errorf("indexed text contains %q:\n%s", noise, text)
				}
			}
		})
	}
}

func TestHTMLReadsClassicSphinxMainContent(t *testing.T) {
	chunks := htmlChunks(t, extract.DefaultNoteSplitter, "testdata/sphinx-classic.html")
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (a heading with no text of its own is not a chunk): %+v", len(chunks), chunks)
	}
	c := chunks[0]
	if got, want := c.Heading, "Sample usage for tokenize > Regression Tests: NLTKWordTokenizer"; got != want {
		t.Errorf("heading = %q, want %q", got, want)
	}
	if got, want := c.Anchor, "regression-tests-nltkwordtokenizer"; got != want {
		t.Errorf("anchor = %q, want %q", got, want)
	}
	want := "Splitting a few sample strings.\n\n```\n>>> word_tokenize(s1)\n['On', 'a', '$']\n```"
	if c.Body != want {
		t.Errorf("body = %q, want %q", c.Body, want)
	}
}

func TestHTMLReadsDocutilsSections(t *testing.T) {
	chunks := htmlChunks(t, extract.DefaultNoteSplitter, "testdata/docutils-book.html")
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2: %+v", len(chunks), chunks)
	}
	if got, want := chunks[0].Heading, "1. Language Processing and Python"; got != want {
		t.Errorf("intro heading = %q, want %q", got, want)
	}
	if got, want := chunks[1].Heading, "1. Language Processing and Python > 1.1 Getting Started with Python"; got != want {
		t.Errorf("section heading = %q, want %q", got, want)
	}
	if got, want := chunks[1].Anchor, "getting-started-with-python"; got != want {
		t.Errorf("section anchor = %q, want %q", got, want)
	}
	if !strings.Contains(chunks[1].Body, "```\n>>> 2 + 2\n4\n```") {
		t.Errorf("doctest not kept as a fenced block: %q", chunks[1].Body)
	}
}

func TestHTMLSplitsLongSectionOutsideCode(t *testing.T) {
	para := "<p>" + strings.Repeat("word ", 100) + "</p>\n"
	code := "<pre>" + strings.Repeat("line of code\n\n", 40) + "</pre>\n"
	page := "<html><body><main><section id=\"s\"><h1>Long</h1>" +
		strings.Repeat(para, 3) + code + strings.Repeat(para, 3) + "</section></main></body></html>"

	chunks := htmlChunks(t, extract.DefaultNoteSplitter, writeHTML(t, page))
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want the section split", len(chunks))
	}
	for i, c := range chunks {
		if strings.Count(c.Body, "```")%2 != 0 {
			t.Errorf("chunk %d cuts through a code block: %q", i, c.Body)
		}
		if c.Heading != "Long" || c.Anchor != "s" {
			t.Errorf("chunk %d = %q#%q, want every part cited as Long#s", i, c.Heading, c.Anchor)
		}
	}
}

func TestHTMLWithoutTextYieldsNothing(t *testing.T) {
	chunks := htmlChunks(t, extract.DefaultNoteSplitter, writeHTML(t, "<html><head><title>Empty</title></head><body><nav>menu</nav></body></html>"))
	if len(chunks) != 0 {
		t.Errorf("got %+v, want no chunks", chunks)
	}
}

func TestHTMLMissingFile(t *testing.T) {
	if _, err := extract.DefaultNoteSplitter.HTML(filepath.Join(t.TempDir(), "absent.html")); err == nil {
		t.Error("want an error for a missing file")
	}
}

func TestHTMLTitle(t *testing.T) {
	cases := []struct {
		name, page, want string
	}{
		{"first h1", "testdata/sphinx-pydata.html", "1.4. Support Vector Machines"},
		{"docutils title", "testdata/docutils-book.html", "1. Language Processing and Python"},
		{"title tag without the documentation suffix", writeHTML(t, "<html><head><title>Glossary &#8212; scikit-learn 1.9.1 documentation</title></head><body><p>x</p></body></html>"), "Glossary"},
		{"title tag without the project prefix", writeHTML(t, "<html><head><title>NLTK :: Installing NLTK</title></head><body><p>x</p></body></html>"), "Installing NLTK"},
		{"fallback", writeHTML(t, "<html><body><p>x</p></body></html>"), "page"},
		{"unreadable file", filepath.Join(t.TempDir(), "absent.html"), "page"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extract.HTMLTitle(c.page, "page"); got != c.want {
				t.Errorf("HTMLTitle = %q, want %q", got, c.want)
			}
		})
	}
}
