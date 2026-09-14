package extract

import (
	"os"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// HTML splits a documentation page — Sphinx or plain docutils output — into one
// chunk per section, cited by its heading path and linked by the section's id.
// Only the page's content area is read: the sidebars, headers and footers of a
// manual repeat on every one of its thousands of pages, and would match any
// query that names the project.
func (sp Splitter) HTML(path string) ([]corpus.Chunk, error) {
	doc, err := parseHTML(path)
	if err != nil {
		return nil, err
	}
	w := &htmlWalker{sp: sp}
	w.walk(contentRoot(doc))
	w.flush()
	return w.chunks, nil
}

// HTMLTitle names a page by its first heading, which Sphinx and docutils both
// render from the document's own title; <title> is only a fallback because
// themes decorate it with the project and version.
func HTMLTitle(path, fallback string) string {
	doc, err := parseHTML(path)
	if err != nil {
		return fallback
	}
	for n := range contentRoot(doc).Descendants() {
		if n.DataAtom == atom.H1 && !isNoise(n) {
			if t := collapse(rawText(n)); t != "" {
				return t
			}
		}
	}
	for n := range doc.Descendants() {
		if n.DataAtom != atom.Title {
			continue
		}
		t := collapse(rawText(n))
		if before, _, ok := strings.Cut(t, " — "); ok {
			t = before
		}
		if _, after, ok := strings.Cut(t, " :: "); ok {
			t = after
		}
		if t != "" {
			return t
		}
	}
	return fallback
}

func parseHTML(path string) (*html.Node, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return html.Parse(f)
}

// contentRoot picks the content area by the markers themes actually use, most
// specific first: pydata-sphinx-theme puts its sidebars inside role="main" but
// outside <article>, so role="main" alone would index the sidebars.
func contentRoot(doc *html.Node) *html.Node {
	markers := []func(*html.Node) bool{
		func(n *html.Node) bool { return n.DataAtom == atom.Article },
		func(n *html.Node) bool { return attr(n, "role") == "main" },
		func(n *html.Node) bool { return n.DataAtom == atom.Main },
		func(n *html.Node) bool { return n.DataAtom == atom.Div && hasClass(n, "body") },
		func(n *html.Node) bool { return n.DataAtom == atom.Body },
	}
	for _, marker := range markers {
		for n := range doc.Descendants() {
			if n.Type == html.ElementNode && marker(n) {
				return n
			}
		}
	}
	return doc
}

type htmlWalker struct {
	sp     Splitter
	chunks []corpus.Chunk
	trail  []string
	anchor string
	body   strings.Builder // finished paragraphs of the current section
	para   strings.Builder // inline text of the paragraph being read
}

func (w *htmlWalker) walk(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		w.para.WriteString(n.Data)
		return
	case html.ElementNode:
	case html.DocumentNode:
		for c := range n.ChildNodes() {
			w.walk(c)
		}
		return
	default:
		return
	}

	if isNoise(n) {
		return
	}
	if level := htmlHeadingLevel(n); level > 0 {
		w.flush()
		w.trail = setTrail(w.trail, level, collapse(rawText(n)))
		w.anchor = sectionID(n)
		return
	}
	switch n.DataAtom {
	case atom.Pre:
		w.endParagraph()
		if code := strings.TrimRight(strings.Trim(rawText(n), "\n"), " \t\n"); code != "" {
			w.appendParagraph("```\n" + code + "\n```")
		}
		return
	case atom.Br:
		w.para.WriteByte(' ')
		return
	}

	block := isBlock(n)
	if block {
		w.endParagraph()
	}
	for c := range n.ChildNodes() {
		w.walk(c)
	}
	if block {
		w.endParagraph()
	}
}

// endParagraph collapses whitespace, which in HTML outside <pre> is layout:
// source line breaks and runs of &nbsp; between a section number and its title.
func (w *htmlWalker) endParagraph() {
	if t := collapse(w.para.String()); t != "" {
		w.appendParagraph(t)
	}
	w.para.Reset()
}

// appendParagraph separates blocks by a blank line, which is where the splitter
// is allowed to cut.
func (w *htmlWalker) appendParagraph(t string) {
	if w.body.Len() > 0 {
		w.body.WriteString("\n\n")
	}
	w.body.WriteString(t)
}

func (w *htmlWalker) flush() {
	w.endParagraph()
	if strings.TrimSpace(w.body.String()) == "" {
		w.body.Reset()
		return
	}
	heading := joinTrail(w.trail)
	for _, part := range w.sp.splitSection(w.body.String()) {
		w.chunks = append(w.chunks, corpus.Chunk{
			Ord:     len(w.chunks) + 1,
			Heading: heading,
			Anchor:  w.anchor,
			Body:    part,
		})
	}
	w.body.Reset()
}

func htmlHeadingLevel(n *html.Node) int {
	switch n.DataAtom {
	case atom.H1:
		return 1
	case atom.H2:
		return 2
	case atom.H3:
		return 3
	case atom.H4:
		return 4
	case atom.H5:
		return 5
	case atom.H6:
		return 6
	}
	return 0
}

// sectionID is the id of the section a heading opens. Sphinx puts the id on
// <section>, docutils on <div class="section">; the heading itself has none.
func sectionID(heading *html.Node) string {
	for p := range heading.Ancestors() {
		if p.DataAtom == atom.Section || (p.DataAtom == atom.Div && hasClass(p, "section")) {
			return attr(p, "id")
		}
	}
	return attr(heading, "id")
}

var (
	noiseTags = []atom.Atom{
		atom.Script, atom.Style, atom.Nav, atom.Header, atom.Footer, atom.Form,
		atom.Button, atom.Noscript, atom.Template, atom.Iframe, atom.Select,
	}
	noiseClasses = []string{
		"headerlink", "sphinxsidebar", "related", "prev-next-area",
		"bd-sidebar-primary", "bd-sidebar-secondary", "bd-header-article", "bd-footer-article",
		// A toctree is a list of links to other pages, each of which is indexed
		// in its own right; docutils system messages are build errors.
		"toctree-wrapper", "system-messages",
		// "[source]" beside every API entry.
		"viewcode-link",
		// Rich cell output of a gallery example: estimator diagrams that print
		// every parameter's docstring again, and data frames of sample rows. One
		// example page came to 248 chunks with it. Plain printed output is
		// sphx-glr-script-out and stays.
		"output_html",
		"sphx-glr-timing", "sphx-glr-footer", "sphx-glr-signature", "sphx-glr-download-link-note",
	}
)

func isNoise(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return n.Type == html.CommentNode
	}
	if slices.Contains(noiseTags, n.DataAtom) || attr(n, "role") == "navigation" {
		return true
	}
	return slices.ContainsFunc(noiseClasses, func(c string) bool { return hasClass(n, c) })
}

func isBlock(n *html.Node) bool {
	switch n.DataAtom {
	case atom.P, atom.Div, atom.Section, atom.Article, atom.Main, atom.Blockquote,
		atom.Ul, atom.Ol, atom.Li, atom.Dl, atom.Dt, atom.Dd,
		atom.Table, atom.Tr, atom.Td, atom.Th, atom.Figure, atom.Figcaption, atom.Hr:
		return true
	}
	return false
}

// rawText is the text under a node as written, noise excluded; whitespace is
// left alone because inside <pre> it is the content.
func rawText(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			return
		}
		if isNoise(n) {
			return
		}
		for c := range n.ChildNodes() {
			visit(c)
		}
	}
	visit(n)
	return b.String()
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	return slices.Contains(strings.Fields(attr(n, "class")), class)
}
