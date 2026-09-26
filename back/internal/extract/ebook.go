package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// Meta is what an e-book says about itself. Unlike a PDF's, it is written by
// whoever made the book, so it names the book rather than the tool.
type Meta struct {
	Title  string
	Author string // "Family, Given" where the book files it so; several joined by "; "
	ISBN   string
}

// EbookMeta reads an EPUB's package document or an FB2's description; an
// unreadable file is described by nothing.
func EbookMeta(path string) Meta {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".epub":
		b, err := openEPUB(path)
		if err != nil {
			return Meta{}
		}
		defer b.Close()
		return b.meta()
	case ".fb2":
		return fb2Meta(path)
	}
	return Meta{}
}

// maxEntry caps what one file inside an EPUB may unpack to: a chapter is well
// under a megabyte, and a zip bomb is not.
const maxEntry = 64 << 20

// linkShare above which a document is navigation — the book's contents page or
// its subject index — rather than text. Measured on the library's e-books:
// contents pages sit at 0.98–1.00, an O'Reilly index at 0.60, chapters near 0.
const linkShare = 0.5

// EPUB splits a book into sections in reading order, each cited by the path of
// its entries in the book's contents. There are no pages to cite: an EPUB
// reflows, and none of the library's books marks where the printed pages
// break.
func (sp Splitter) EPUB(file string) ([]corpus.Chunk, error) {
	b, err := openEPUB(file)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	toc := b.contents()
	w := &htmlWalker{sp: sp, noise: ebookNoise, minSection: minPageChars}
	for _, doc := range b.spine() {
		root, err := b.parse(doc)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", doc, err)
		}
		body := root
		for n := range root.Descendants() {
			if n.DataAtom == atom.Body {
				body = n
				break
			}
		}
		if navigation(body) {
			continue
		}
		if toc != nil {
			w.marks = map[string]tocEntry{}
			for _, e := range toc {
				switch {
				case e.file == doc && e.id == "":
					w.flush()
					w.trail = setTrail(w.trail, e.level, e.label)
				case e.file == doc:
					if _, seen := w.marks[e.id]; !seen {
						w.marks[e.id] = e
					}
				}
			}
		}
		w.walk(body)
		w.endParagraph()
		// A document boundary is a paragraph boundary, not a section one:
		// converters cut a long chapter into files wherever it grew too big.
	}
	w.flush()
	return w.chunks, nil
}

func ebookNoise(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return n.Type == html.CommentNode
	}
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Nav, atom.Template:
		return true
	}
	return false
}

// navigation reports a document whose text is mostly links into the book.
func navigation(body *html.Node) bool {
	var linked, total int
	var visit func(n *html.Node, inLink bool)
	visit = func(n *html.Node, inLink bool) {
		if n.Type == html.TextNode {
			k := len(strings.Join(strings.Fields(n.Data), ""))
			total += k
			if inLink {
				linked += k
			}
			return
		}
		if n.DataAtom == atom.A {
			if href := attr(n, "href"); href != "" && !strings.Contains(href, "://") {
				inLink = true
			}
		}
		for c := range n.ChildNodes() {
			visit(c, inLink)
		}
	}
	visit(body, false)
	return total > 0 && float64(linked)/float64(total) > linkShare
}

type tocEntry struct {
	file, id string // the document an entry points at, and the element in it
	level    int
	label    string
}

type epub struct {
	zr    *zip.ReadCloser
	files map[string]*zip.File
	opf   string // path of the package document
	pkg   opfPackage
}

type opfPackage struct {
	Metadata struct {
		Titles   []string `xml:"title"`
		Creators []struct {
			Name   string `xml:",chardata"`
			FileAs string `xml:"file-as,attr"`
			Role   string `xml:"role,attr"`
		} `xml:"creator"`
		Identifiers []struct {
			Value  string `xml:",chardata"`
			Scheme string `xml:"scheme,attr"`
		} `xml:"identifier"`
	} `xml:"metadata"`
	Items []struct {
		ID         string `xml:"id,attr"`
		Href       string `xml:"href,attr"`
		MediaType  string `xml:"media-type,attr"`
		Properties string `xml:"properties,attr"`
	} `xml:"manifest>item"`
	Spine struct {
		TOC   string `xml:"toc,attr"`
		Items []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func openEPUB(file string) (*epub, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, err
	}
	b := &epub{zr: zr, files: make(map[string]*zip.File, len(zr.File))}
	for _, f := range zr.File {
		b.files[f.Name] = f
	}
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := b.decode("META-INF/container.xml", &container); err != nil {
		zr.Close()
		return nil, err
	}
	if len(container.Rootfiles) == 0 {
		zr.Close()
		return nil, errors.New("container.xml names no package document")
	}
	b.opf = container.Rootfiles[0].FullPath
	if err := b.decode(b.opf, &b.pkg); err != nil {
		zr.Close()
		return nil, err
	}
	return b, nil
}

func (b *epub) Close() error { return b.zr.Close() }

func (b *epub) open(name string) (io.ReadCloser, error) {
	f, ok := b.files[name]
	if !ok {
		return nil, fmt.Errorf("%s: not in the book", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(rc, maxEntry), rc}, nil
}

func (b *epub) decode(name string, v any) error {
	rc, err := b.open(name)
	if err != nil {
		return err
	}
	defer rc.Close()
	d := xml.NewDecoder(rc)
	d.CharsetReader = charset.NewReaderLabel
	return d.Decode(v)
}

func (b *epub) parse(name string) (*html.Node, error) {
	rc, err := b.open(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	doc, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	return html.Parse(bytes.NewReader(selfClosed.ReplaceAllFunc(doc, openAndClose)))
}

// selfClosed is an XHTML element closed by "/>", which an HTML parser reads as
// an opening tag: an empty <title/> in the head takes the whole body for its
// text. Only void elements such as <br/> may stay as they are.
var selfClosed = regexp.MustCompile(`<([A-Za-z][\w:.-]*)(\s[^<>]*?)?\s*/>`)

func openAndClose(tag []byte) []byte {
	m := selfClosed.FindSubmatch(tag)
	switch strings.ToLower(string(m[1])) {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return tag
	}
	return fmt.Appendf(nil, "<%s%s></%s>", m[1], m[2], m[1])
}

// resolve turns an href written in the document at base into a path in the
// archive, without its fragment.
func resolve(base, href string) (file, id string) {
	href, id, _ = strings.Cut(href, "#")
	if u, err := url.PathUnescape(href); err == nil {
		href = u
	}
	if href == "" {
		return base, id
	}
	return path.Join(path.Dir(base), href), id
}

func (b *epub) spine() []string {
	byID := make(map[string]string, len(b.pkg.Items))
	for _, it := range b.pkg.Items {
		if strings.Contains(it.MediaType, "html") {
			byID[it.ID], _ = resolve(b.opf, it.Href)
		}
	}
	var docs []string
	for _, ref := range b.pkg.Spine.Items {
		if doc, ok := byID[ref.IDRef]; ok {
			docs = append(docs, doc)
		}
	}
	return docs
}

// contents is the book's table of contents in reading order: the EPUB 2 NCX
// when there is one, which nearly every book still carries, or else the EPUB 3
// navigation document. nil when the book has neither.
func (b *epub) contents() []tocEntry {
	for _, it := range b.pkg.Items {
		if it.ID == b.pkg.Spine.TOC || it.MediaType == "application/x-dtbncx+xml" {
			ncx, _ := resolve(b.opf, it.Href)
			if toc := b.ncx(ncx); toc != nil {
				return toc
			}
		}
	}
	for _, it := range b.pkg.Items {
		if strings.Contains(" "+it.Properties+" ", " nav ") {
			nav, _ := resolve(b.opf, it.Href)
			return b.nav(nav)
		}
	}
	return nil
}

type navPoint struct {
	Label   string `xml:"navLabel>text"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Points []navPoint `xml:"navPoint"`
}

func (b *epub) ncx(name string) []tocEntry {
	var doc struct {
		Points []navPoint `xml:"navMap>navPoint"`
	}
	if err := b.decode(name, &doc); err != nil {
		return nil
	}
	var toc []tocEntry
	var add func(points []navPoint, level int)
	add = func(points []navPoint, level int) {
		for _, p := range points {
			file, id := resolve(name, p.Content.Src)
			if label := collapse(p.Label); label != "" {
				toc = append(toc, tocEntry{file: file, id: id, level: level, label: label})
			}
			add(p.Points, level+1)
		}
	}
	add(doc.Points, 1)
	return toc
}

func (b *epub) nav(name string) []tocEntry {
	root, err := b.parse(name)
	if err != nil {
		return nil
	}
	var toc []tocEntry
	var add func(list *html.Node, level int)
	add = func(list *html.Node, level int) {
		for li := range list.ChildNodes() {
			if li.DataAtom != atom.Li {
				continue
			}
			for c := range li.ChildNodes() {
				switch c.DataAtom {
				case atom.A:
					if label := collapse(rawText(c)); label != "" {
						file, id := resolve(name, attr(c, "href"))
						toc = append(toc, tocEntry{file: file, id: id, level: level, label: label})
					}
				case atom.Ol:
					add(c, level+1)
				}
			}
		}
	}
	for n := range root.Descendants() {
		if n.DataAtom == atom.Nav && strings.Contains(" "+attr(n, "epub:type")+" ", " toc ") {
			for c := range n.ChildNodes() {
				if c.DataAtom == atom.Ol {
					add(c, 1)
				}
			}
			break
		}
	}
	return toc
}

// isbn13 is an identifier that is an ISBN without saying so, as O'Reilly's are.
var isbn13 = regexp.MustCompile(`^97[89]\d{10}$`)

func (b *epub) meta() Meta {
	md := b.pkg.Metadata
	var m Meta
	if len(md.Titles) > 0 {
		m.Title = collapse(md.Titles[0])
	}
	var authors []string
	for _, c := range md.Creators {
		if c.Role != "" && c.Role != "aut" {
			continue
		}
		// file-as is the name as a catalogue sorts it, "Росс, Алек"; without a
		// comma it is as often a placeholder ("Неизв.") as a name.
		name := collapse(c.Name)
		if strings.Contains(c.FileAs, ",") {
			name = collapse(c.FileAs)
		}
		if name != "" {
			authors = append(authors, name)
		}
	}
	m.Author = strings.Join(authors, "; ")
	for _, id := range md.Identifiers {
		v := strings.TrimSpace(id.Value)
		urn := len(v) > 9 && strings.EqualFold(v[:9], "urn:isbn:")
		if urn {
			v = v[9:]
		}
		if urn || strings.EqualFold(id.Scheme, "ISBN") || isbn13.MatchString(v) {
			m.ISBN = v
			break
		}
	}
	return m
}
