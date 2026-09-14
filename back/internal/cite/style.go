package cite

import (
	"encoding/xml"
	"errors"
	"strings"
)

// ErrNotAStyle is XML that is not a CSL style.
var ErrNotAStyle = errors.New("not a CSL style")

// StyleInfo is what a CSL file says about itself.
type StyleInfo struct {
	ID     string
	Title  string
	Parent string // the independent style a dependent one borrows its rules from
}

// ParseStyle reads the <info> of a CSL style, which is what names it in a list
// and tells a dependent style from one that can format anything.
func ParseStyle(body string) (StyleInfo, error) {
	var doc struct {
		XMLName xml.Name `xml:"style"`
		Info    struct {
			ID    string `xml:"id"`
			Title string `xml:"title"`
			Links []struct {
				Rel  string `xml:"rel,attr"`
				Href string `xml:"href,attr"`
			} `xml:"link"`
		} `xml:"info"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil || doc.XMLName.Local != "style" || doc.Info.Title == "" {
		return StyleInfo{}, ErrNotAStyle
	}
	info := StyleInfo{ID: doc.Info.ID, Title: strings.TrimSpace(doc.Info.Title)}
	for _, l := range doc.Info.Links {
		if l.Rel == "independent-parent" {
			info.Parent = l.Href
		}
	}
	return info, nil
}

// StyleSlug turns a style's id — usually a URL — into a short name.
func StyleSlug(id string) string {
	id = strings.TrimRight(id, "/")
	return strings.Trim(strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 'a' - 'A'
		}
		return '-'
	}, id[strings.LastIndex(id, "/")+1:]), "-")
}
