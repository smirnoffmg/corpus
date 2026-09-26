package extract

import (
	"encoding/xml"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/net/html/charset"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// FB2 splits a FictionBook into its sections, each cited by the titles of the
// sections it is nested in. The format has no pages, only sections.
func (sp Splitter) FB2(path string) ([]corpus.Chunk, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	d := fb2Decoder(f)
	w := &htmlWalker{sp: sp, minSection: minPageChars}
	var (
		depth   int // sections open around the reader
		inTitle bool
		title   []string
	)
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "description", "binary":
				if err := d.Skip(); err != nil {
					return nil, err
				}
			case "body":
				// The other bodies are the footnotes and the comments, each cut
				// into one-line sections titled with its number.
				if fb2Attr(t, "name") != "" {
					if err := d.Skip(); err != nil {
						return nil, err
					}
				}
			case "a":
				// A footnote mark would glue its number to the word before it.
				if fb2Attr(t, "type") == "note" {
					if err := d.Skip(); err != nil {
						return nil, err
					}
				}
			case "section":
				w.flush()
				depth++
			case "title":
				if depth == 0 {
					// The body's own title is the book's, already its name.
					if err := d.Skip(); err != nil {
						return nil, err
					}
					continue
				}
				w.flush()
				inTitle, title = true, nil
			case "empty-line":
				w.endParagraph()
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "section":
				w.flush()
				depth--
			case "title":
				if inTitle {
					w.trail = setTrail(w.trail, depth, strings.Join(title, " "))
					inTitle = false
				}
			case "p", "v", "subtitle", "text-author", "td", "th":
				if inTitle {
					if line := collapse(w.para.String()); line != "" {
						title = append(title, line)
					}
					w.para.Reset()
					continue
				}
				w.endParagraph()
			}
		case xml.CharData:
			w.para.Write(t)
		}
	}
	w.flush()
	return w.chunks, nil
}

// fb2Decoder reads FictionBook as it is found rather than as specified: half
// the files are windows-1251, and many carry HTML entities or an unescaped
// ampersand.
func fb2Decoder(r io.Reader) *xml.Decoder {
	d := xml.NewDecoder(r)
	d.CharsetReader = charset.NewReaderLabel
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	return d
}

func fb2Attr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func fb2Meta(path string) Meta {
	f, err := os.Open(path)
	if err != nil {
		return Meta{}
	}
	defer f.Close()

	var desc struct {
		Title   string `xml:"title-info>book-title"`
		Authors []struct {
			First    string `xml:"first-name"`
			Middle   string `xml:"middle-name"`
			Last     string `xml:"last-name"`
			Nickname string `xml:"nickname"`
		} `xml:"title-info>author"`
		ISBN string `xml:"publish-info>isbn"`
	}
	d := fb2Decoder(f)
	for {
		tok, err := d.Token()
		if err != nil {
			return Meta{}
		}
		if t, ok := tok.(xml.StartElement); ok && t.Name.Local == "description" {
			if err := d.DecodeElement(&desc, &t); err != nil {
				return Meta{}
			}
			break
		}
	}

	var authors []string
	for _, a := range desc.Authors {
		given := collapse(a.First + " " + a.Middle)
		switch family := collapse(a.Last); {
		case family != "" && given != "":
			authors = append(authors, family+", "+given)
		case family != "":
			authors = append(authors, family)
		case collapse(a.Nickname) != "":
			authors = append(authors, collapse(a.Nickname))
		}
	}
	return Meta{Title: collapse(desc.Title), Author: strings.Join(authors, "; "), ISBN: collapse(desc.ISBN)}
}
