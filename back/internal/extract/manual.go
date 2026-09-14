package extract

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

var (
	versionRe   = regexp.MustCompile(`\b\d+(?:\.\d+)+\b`)
	copyrightRe = regexp.MustCompile(`(?:©|Copyright)\s*(?:\d{4}\s*[-–]\s*)?\d{4},?\s*([^.\n]+?)\.?\s*(?:$|\n)`)
)

// ManualDraft describes a manual as an online resource, from what its index
// page says about itself: the site's title and version from <title>, where it
// lives from <link rel="canonical"> or, for a GitHub Pages site, its CNAME
// file, and who publishes it from the copyright line. accessed is when the
// copy was taken, which a reference to an online resource has to state.
func ManualDraft(dir, name string, accessed time.Time) corpus.CSL {
	draft := corpus.CSL{
		"type":     "webpage",
		"title":    name,
		"accessed": map[string]any{"date-parts": []any{[]any{accessed.Year(), int(accessed.Month()), accessed.Day()}}},
	}
	doc, err := parseHTML(filepath.Join(dir, "index.html"))
	if err != nil {
		return draft
	}

	for n := range doc.Descendants() {
		switch {
		case n.DataAtom == atom.Title && draft["title"] == name:
			title := collapse(rawText(n))
			if _, site, ok := strings.Cut(title, " — "); ok {
				title = site
			}
			if title != "" {
				draft["title"] = strings.ReplaceAll(title, " :: ", " : ")
			}
			if v := versionRe.FindString(title); v != "" {
				draft["version"] = v
			}
		case n.DataAtom == atom.Link && attr(n, "rel") == "canonical" && draft["URL"] == nil:
			if href := attr(n, "href"); href != "" {
				draft["URL"] = strings.TrimSuffix(href, "index.html")
			}
		}
	}
	if draft["URL"] == nil {
		if cname, err := os.ReadFile(filepath.Join(dir, "CNAME")); err == nil && strings.TrimSpace(string(cname)) != "" {
			draft["URL"] = "https://" + strings.TrimSpace(string(cname)) + "/"
		}
	}
	if m := copyrightRe.FindStringSubmatch(textOf(doc)); m != nil {
		// "scikit-learn developers (BSD License)": the licence is not the publisher.
		publisher, _, _ := strings.Cut(m[1], " (")
		draft["publisher"] = strings.TrimSpace(publisher)
	}
	return draft
}

// textOf is every text node of the page, one per line, so a pattern cannot run
// from the footer into whatever follows it.
func textOf(doc *html.Node) string {
	var b strings.Builder
	for n := range doc.Descendants() {
		if n.Type == html.TextNode && n.Parent != nil && n.Parent.DataAtom != atom.Script && n.Parent.DataAtom != atom.Style {
			if t := collapse(n.Data); t != "" {
				b.WriteString(t)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}
