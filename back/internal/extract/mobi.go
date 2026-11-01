package extract

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/text/encoding/charmap"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// The layout is Mobipocket's as calibre reads it (ebooks/mobi/reader): a Palm
// database whose record 0 holds the PalmDOC header, the MOBI header and EXTH,
// followed by the text records.

// MOBI splits a Mobipocket book the way EPUB splits its documents: the text is
// cut at its page breaks, a contents page is left out, and a section is cited
// by the book's own contents. The contents are links to byte offsets in the
// text; there are no printed pages to cite.
//
// Only the Mobipocket 6 text is read. A KF8 (AZW3) book, a HUFF/CDIC one and
// one under DRM are refused.
func (sp Splitter) MOBI(file string) ([]corpus.Chunk, error) {
	b, err := openMOBI(file)
	if err != nil {
		return nil, err
	}
	text, err := b.text()
	if err != nil {
		return nil, err
	}

	var marks map[string]tocEntry
	if toc := b.contents(text); len(toc) > 0 {
		marks = make(map[string]tocEntry, len(toc))
		var offsets []int
		for _, l := range toc {
			marks[l.entry.id] = l.entry
			offsets = append(offsets, l.offset)
		}
		text = anchorOffsets(text, offsets, b.codepage == utf8Codepage)
	}

	w := &htmlWalker{sp: sp, noise: ebookNoise, minSection: minPageChars, marks: marks}
	for _, piece := range mobiPagebreak.Split(b.decode(text), -1) {
		body, err := parseBody(piece)
		if err != nil {
			return nil, err
		}
		if navigation(body) {
			continue
		}
		w.walk(body)
		w.endParagraph()
	}
	w.flush()
	return w.chunks, nil
}

func mobiMeta(file string) Meta {
	b, err := openMOBI(file)
	if err != nil {
		return Meta{}
	}
	m := Meta{Title: collapse(b.decode(b.name))}
	var authors []string
	for _, e := range b.exth {
		v := collapse(b.decode(e.data))
		switch e.kind {
		case exthAuthor:
			if v != "" {
				authors = append(authors, v)
			}
		case exthISBN:
			if m.ISBN == "" {
				m.ISBN = v
			}
		case exthTitle:
			if v != "" {
				m.Title = v
			}
		}
	}
	m.Author = strings.Join(authors, "; ")
	return m
}

const (
	utf8Codepage = 65001

	compressionNone    = 1
	compressionPalmDOC = 2
	compressionHuffman = 17480

	exthAuthor = 100
	exthISBN   = 104
	exthTitle  = 503
)

type mobiRecord struct {
	kind uint32
	data []byte
}

type mobiBook struct {
	records     [][]byte
	compression uint16
	textRecords int
	codepage    uint32
	extraFlags  uint16
	name        []byte
	exth        []mobiRecord
}

var be = binary.BigEndian

func openMOBI(file string) (*mobiBook, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if len(data) < 78 || string(data[60:68]) != "BOOKMOBI" {
		return nil, errors.New("not a Mobipocket book")
	}
	n := int(be.Uint16(data[76:]))
	if n == 0 || len(data) < 78+8*n {
		return nil, errors.New("the record list is cut short")
	}
	b := &mobiBook{records: make([][]byte, n)}
	for i := range n {
		start, end := int(be.Uint32(data[78+8*i:])), len(data)
		if i+1 < n {
			end = int(be.Uint32(data[78+8*(i+1):]))
		}
		if start > end || end > len(data) {
			return nil, fmt.Errorf("record %d lies outside the file", i)
		}
		b.records[i] = data[start:end]
	}

	r0 := b.records[0]
	if len(r0) < 0x84 || string(r0[16:20]) != "MOBI" {
		return nil, errors.New("record 0 has no MOBI header")
	}
	b.compression = be.Uint16(r0[0:])
	b.textRecords = int(be.Uint16(r0[8:]))
	b.codepage = be.Uint32(r0[28:])
	switch {
	case be.Uint16(r0[12:]) != 0:
		return nil, errors.New("the book is locked with DRM")
	case be.Uint32(r0[36:]) >= 8:
		return nil, errors.New("a KF8 (AZW3) book is not read; convert it to EPUB")
	case b.compression == compressionHuffman:
		return nil, errors.New("HUFF/CDIC compression is not read; convert the book to EPUB")
	case b.compression != compressionNone && b.compression != compressionPalmDOC:
		return nil, fmt.Errorf("unknown compression %d", b.compression)
	case b.textRecords >= n:
		return nil, fmt.Errorf("%d text records in a book of %d", b.textRecords, n)
	}

	headerLen := int(be.Uint32(r0[20:]))
	// The flags field exists from a 0xE4-byte header on.
	if headerLen >= 0xE4 && len(r0) >= 0xF4 {
		b.extraFlags = be.Uint16(r0[0xF2:])
	}
	if off, size := int(be.Uint32(r0[84:])), int(be.Uint32(r0[88:])); off+size <= len(r0) {
		b.name = r0[off : off+size]
	}
	if be.Uint32(r0[128:])&0x40 != 0 {
		b.exth = exthRecords(r0, 16+headerLen)
	}
	return b, nil
}

// exthRecords reads what it can of the EXTH block at off; a record running
// past the end stops it, since metadata is never worth failing the book over.
func exthRecords(r0 []byte, off int) []mobiRecord {
	if off+12 > len(r0) || string(r0[off:off+4]) != "EXTH" {
		return nil
	}
	count := int(be.Uint32(r0[off+8:]))
	var out []mobiRecord
	p := off + 12
	for range count {
		if p+8 > len(r0) {
			break
		}
		size := int(be.Uint32(r0[p+4:]))
		if size < 8 || p+size > len(r0) {
			break
		}
		out = append(out, mobiRecord{kind: be.Uint32(r0[p:]), data: r0[p+8 : p+size]})
		p += size
	}
	return out
}

func (b *mobiBook) text() ([]byte, error) {
	var out []byte
	for i, rec := range b.records[1 : 1+b.textRecords] {
		rec = trimTrailingEntries(rec, b.extraFlags)
		if b.compression == compressionNone {
			out = append(out, rec...)
		} else {
			var err error
			if out, err = unpackPalmDOC(out, rec); err != nil {
				return nil, fmt.Errorf("text record %d: %w", i+1, err)
			}
		}
		if len(out) > maxEntry {
			return nil, errors.New("the text unpacks to more than a book holds")
		}
	}
	return out, nil
}

// trimTrailingEntries cuts what follows a text record's data: one entry per
// set flag from bit 1 up, the first of them last in the record, each ending in
// its size; then, for bit 0, the bytes of a character the record split, which
// the next record repeats.
func trimTrailingEntries(rec []byte, flags uint16) []byte {
	for f := flags >> 1; f != 0; f >>= 1 {
		if f&1 != 0 {
			rec = rec[:len(rec)-min(len(rec), backwardVarint(rec))]
		}
	}
	if flags&1 != 0 && len(rec) > 0 {
		rec = rec[:len(rec)-min(len(rec), int(rec[len(rec)-1]&3)+1)]
	}
	return rec
}

// backwardVarint reads a size written backwards from the end of b: seven bits
// a byte, the last byte read marked by its high bit.
func backwardVarint(b []byte) int {
	v, shift := 0, 0
	for i := len(b) - 1; i >= 0 && shift < 28; i-- {
		v |= int(b[i]&0x7F) << shift
		shift += 7
		if b[i]&0x80 != 0 {
			break
		}
	}
	return v
}

// unpackPalmDOC appends a record's text to dst. Each record is compressed on
// its own, so a reference never reaches back past the record's start.
func unpackPalmDOC(dst, src []byte) ([]byte, error) {
	start := len(dst)
	for i := 0; i < len(src); {
		c := src[i]
		i++
		switch {
		case c >= 1 && c <= 8:
			if i+int(c) > len(src) {
				return nil, errors.New("a literal run past the record's end")
			}
			dst = append(dst, src[i:i+int(c)]...)
			i += int(c)
		case c < 0x80:
			dst = append(dst, c)
		case c >= 0xC0:
			dst = append(dst, ' ', c^0x80)
		default:
			if i >= len(src) {
				return nil, errors.New("a reference cut at the record's end")
			}
			pair := int(c)<<8 | int(src[i])
			i++
			dist, n := pair>>3&0x7FF, pair&7+3
			if dist == 0 || dist > len(dst)-start {
				return nil, errors.New("a reference before the record's start")
			}
			for range n {
				dst = append(dst, dst[len(dst)-dist])
			}
		}
	}
	return dst, nil
}

func (b *mobiBook) decode(s []byte) string {
	if b.codepage != utf8Codepage {
		// calibre reads any other codepage as cp1252 too.
		if d, err := charmap.Windows1252.NewDecoder().Bytes(s); err == nil {
			s = d
		}
	}
	return string(bytes.ToValidUTF8(s, nil))
}

type mobiLink struct {
	offset int
	entry  tocEntry
}

var (
	mobiReference = regexp.MustCompile(`(?i)<reference\b[^>]*>`)
	mobiTOCType   = regexp.MustCompile(`(?i)\btype=["']?toc\b`)
	mobiFilepos   = regexp.MustCompile(`(?i)\bfilepos=["']?(\d+)`)
	mobiPagebreak = regexp.MustCompile(`(?i)<\s*/?\s*mbp:pagebreak[^>]*>`)
)

// contents reads the page the book's guide names as its contents: every link
// on it is an entry. A book without one has no contents, and its sections
// come from its headings.
func (b *mobiBook) contents(text []byte) []mobiLink {
	start := -1
	for _, ref := range mobiReference.FindAll(text, -1) {
		if m := mobiFilepos.FindSubmatch(ref); m != nil && mobiTOCType.Match(ref) {
			start, _ = strconv.Atoi(string(m[1]))
			break
		}
	}
	if start < 0 || start >= len(text) {
		return nil
	}
	end := len(text)
	if loc := mobiPagebreak.FindIndex(text[start:]); loc != nil {
		end = start + loc[0]
	}
	page, err := parseBody(b.decode(text[start:end]))
	if err != nil {
		return nil
	}
	var toc []mobiLink
	for n := range page.Descendants() {
		if n.DataAtom != atom.A {
			continue
		}
		off, err := strconv.Atoi(attr(n, "filepos"))
		if label := collapse(rawText(n)); err == nil && label != "" && off < len(text) {
			toc = append(toc, mobiLink{offset: off, entry: tocEntry{id: fileposID(off), level: 1, label: label}})
		}
	}
	return toc
}

func fileposID(off int) string { return "filepos" + strconv.Itoa(off) }

// anchorOffsets puts an empty element with a contents entry's id where the
// entry points, which is how the walker finds where a section opens. An
// offset inside a tag, or inside a UTF-8 character, moves back to its start.
func anchorOffsets(text []byte, offsets []int, utf8Text bool) []byte {
	slices.Sort(offsets)
	offsets = slices.Compact(offsets)
	out := slices.Clone(text)
	for _, off := range slices.Backward(offsets) {
		at := off
		if lt := bytes.LastIndexByte(text[:at], '<'); lt > bytes.LastIndexByte(text[:at], '>') {
			at = lt
		}
		for utf8Text && at > 0 && at < len(text) && !utf8.RuneStart(text[at]) {
			at--
		}
		out = slices.Insert(out, at, fmt.Appendf(nil, `<a id="%s"></a>`, fileposID(off))...)
	}
	return out
}

func parseBody(doc string) (*html.Node, error) {
	root, err := html.Parse(bytes.NewReader(selfClosed.ReplaceAllFunc([]byte(doc), openAndClose)))
	if err != nil {
		return nil, err
	}
	for n := range root.Descendants() {
		if n.DataAtom == atom.Body {
			return n, nil
		}
	}
	return root, nil
}
