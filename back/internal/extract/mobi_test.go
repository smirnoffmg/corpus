package extract_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/extract"
)

// englishProse clears the floor a section has to clear to be indexed, in
// ASCII, which a hand-built PalmDOC stream takes byte for byte.
var englishProse = strings.Repeat("Linda is thirty-one years old, single, outspoken, and very bright. ", 3)

type exth struct {
	kind uint32
	data string
}

// mobiBook is what the test controls of a MOBI file; the rest of the header
// is written as a Kindle book has it.
type mobiBook struct {
	records     [][]byte // the text records as stored: compressed, with trailing entries
	textLen     int
	compression uint16
	encryption  uint16
	codepage    uint32
	version     uint32
	extraFlags  uint16
	fullName    string
	exth        []exth
}

func writeMOBI(t *testing.T, b mobiBook) string {
	t.Helper()
	if b.compression == 0 {
		b.compression = 1
	}
	if b.codepage == 0 {
		b.codepage = 65001
	}
	if b.version == 0 {
		b.version = 6
	}
	be := binary.BigEndian

	var ext bytes.Buffer
	for _, e := range b.exth {
		ext.Write(be.AppendUint32(nil, e.kind))
		ext.Write(be.AppendUint32(nil, uint32(8+len(e.data))))
		ext.WriteString(e.data)
	}
	exthBlock := append([]byte("EXTH"), be.AppendUint32(nil, uint32(12+ext.Len()))...)
	exthBlock = be.AppendUint32(exthBlock, uint32(len(b.exth)))
	exthBlock = append(exthBlock, ext.Bytes()...)
	for len(exthBlock)%4 != 0 {
		exthBlock = append(exthBlock, 0)
	}

	const headerLen = 232
	r0 := make([]byte, 16+headerLen)
	be.PutUint16(r0[0:], b.compression)
	be.PutUint32(r0[4:], uint32(b.textLen))
	be.PutUint16(r0[8:], uint16(len(b.records)))
	be.PutUint16(r0[10:], 4096)
	be.PutUint16(r0[12:], b.encryption)
	copy(r0[16:], "MOBI")
	be.PutUint32(r0[20:], headerLen)
	be.PutUint32(r0[24:], 2)
	be.PutUint32(r0[28:], b.codepage)
	be.PutUint32(r0[36:], b.version)
	be.PutUint32(r0[84:], uint32(len(r0)+len(exthBlock)))
	be.PutUint32(r0[88:], uint32(len(b.fullName)))
	be.PutUint32(r0[128:], 0x40)
	be.PutUint16(r0[0xF2:], b.extraFlags)
	r0 = slices.Concat(r0, exthBlock, []byte(b.fullName), []byte{0, 0})

	records := append([][]byte{r0}, b.records...)
	head := make([]byte, 78+8*len(records)+2)
	copy(head, "Thinking,_Fast_and_Slow")
	copy(head[60:], "BOOKMOBI")
	be.PutUint16(head[76:], uint16(len(records)))
	off := len(head)
	for i, r := range records {
		be.PutUint32(head[78+8*i:], uint32(off))
		be.PutUint32(head[82+8*i:], uint32(2*i))
		off += len(r)
	}
	pdb := slices.Concat(append([][]byte{head}, records...)...)

	path := filepath.Join(t.TempDir(), "book.mobi")
	if err := os.WriteFile(path, pdb, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type link struct{ placeholder, target string }

// withFilepos writes the byte offset of each target's first occurrence into
// the link that names it, as a Kindle book's links are written: ten digits,
// so that writing one moves nothing. links are in the order they appear.
func withFilepos(html string, links []link) string {
	for _, l := range links {
		html = strings.Replace(html, l.placeholder, "##########", 1)
	}
	for _, l := range links {
		html = strings.Replace(html, "##########", fmt.Sprintf("%010d", strings.Index(html, l.target)), 1)
	}
	return html
}

func mobiChunks(t *testing.T, path string) []corpus.Chunk {
	t.Helper()
	chunks, err := extract.DefaultNoteSplitter.MOBI(path)
	if err != nil {
		t.Fatal(err)
	}
	return chunks
}

func TestMOBICitesASectionByItsContentsPage(t *testing.T) {
	html := `<html><head><guide><reference type="toc" title="Table of Contents" filepos=TOC /></guide></head><body>` +
		`<p align="center"><img recindex="00012"></img></p><mbp:pagebreak/>` +
		`<p align="center"><font size="5"><b>Contents</b></font></p>` +
		`<p><a filepos=INTRO><b><u>Introduction</u></b></a></p>` +
		`<p><b> Part I. Two Systems</b></p>` +
		`<p><a filepos=CH1><b><u>1. The Characters of the Story</u></b></a></p><mbp:pagebreak/>` +
		`<p id="intro"><b>Introduction</b></p><p>` + englishProse + `</p><mbp:pagebreak/>` +
		`<p id="ch1"><b>1. The Characters</b></p><p>Это ` + englishProse + `</p></body></html>`
	// The second link points inside its paragraph's tag, as converters write
	// them: the section still opens at that paragraph.
	html = withFilepos(html, []link{
		{"TOC", `<p align="center"><font`},
		{"INTRO", `<p id="intro">`},
		{"CH1", `p id="ch1">`},
	})

	// The text crosses a record boundary in the middle of "Э", as a UTF-8
	// book's does: the record ends with the character's first byte, and the
	// rest, which opens the next record, is repeated in a multibyte trailing
	// entry; an indexing entry follows it.
	text := []byte(html)
	cut := bytes.Index(text, []byte("Это")) + 1
	first := append(append([]byte{}, text[:cut]...), text[cut], 0x01, 0xAA, 0xBB, 0x83)
	second := append(append([]byte{}, text[cut:]...), 0x00, 0x81)
	path := writeMOBI(t, mobiBook{records: [][]byte{first, second}, textLen: len(text), extraFlags: 0x3})

	chunks := mobiChunks(t, path)
	want := []string{"Introduction", "1. The Characters of the Story"}
	if got := headings(chunks); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("headings = %q, want %q", got, want)
	}
	if !strings.HasPrefix(chunks[1].Body, "1. The Characters\n\nЭто Linda") {
		t.Errorf("second section = %q", chunks[1].Body)
	}
	for _, c := range chunks {
		if strings.Contains(c.Body, "Contents") || strings.Contains(c.Body, "Two Systems") {
			t.Errorf("the contents page leaked into %q", c.Body)
		}
	}
}

func TestMOBIWithoutContentsFallsBackToItsHeadings(t *testing.T) {
	html := `<html><body><h2>Preface</h2><p>` + englishProse + `</p></body></html>`
	path := writeMOBI(t, mobiBook{records: [][]byte{[]byte(html)}, textLen: len(html)})

	if got := headings(mobiChunks(t, path)); strings.Join(got, "|") != "Preface" {
		t.Errorf("headings = %q", got)
	}
}

// palmDOC compresses by hand, one of each kind of token: a run of literal
// bytes, a byte taken as itself, a space folded into the next character, and
// a reference back into what is already decompressed.
func TestMOBIDecompressesPalmDOC(t *testing.T) {
	z := slices.Concat(
		[]byte("<p>caf"),
		[]byte{0x02, 0xC3, 0xA9}, // "é", copied as it is
		[]byte{0xC1},             // " A"
		[]byte(" "+englishProse),
	)
	// Three more copies of the prose, ten bytes at a time from one prose back.
	dist := len(englishProse)
	for left := 3 * dist; left > 0; {
		n := min(10, left)
		if left-n > 0 && left-n < 3 {
			n = left - 3
		}
		pair := 0x8000 | dist<<3 | (n - 3)
		z = append(z, byte(pair>>8), byte(pair))
		left -= n
	}
	z = append(z, "</p>"...)
	want := "café A " + strings.Repeat(englishProse, 4)

	path := writeMOBI(t, mobiBook{records: [][]byte{z}, textLen: len(want) + len("<p></p>"), compression: 2})
	chunks := mobiChunks(t, path)
	var body strings.Builder
	for _, c := range chunks {
		body.WriteString(c.Body)
		body.WriteString(" ")
	}
	if got := strings.Join(strings.Fields(body.String()), " "); got != strings.Join(strings.Fields(want), " ") {
		t.Errorf("text = %q,\nwant %q", got, want)
	}
}

func TestMOBIMeta(t *testing.T) {
	path := writeMOBI(t, mobiBook{
		codepage: 1252,
		fullName: "Thinking Fast",
		exth: []exth{
			{100, "Kahneman, Daniel"},
			{100, "Tversky, Amos"},
			{101, "Farrar, Straus and Giroux"},
			{104, "978-0-374-27563-1"},
			{503, "Thinking, Fast and Slow \xe9"},
		},
	})

	want := extract.Meta{Title: "Thinking, Fast and Slow é", Author: "Kahneman, Daniel; Tversky, Amos", ISBN: "978-0-374-27563-1"}
	if meta := extract.EbookMeta(path); meta != want {
		t.Errorf("meta = %+v, want %+v", meta, want)
	}
}

func TestMOBIMetaFallsBackToTheFullName(t *testing.T) {
	path := writeMOBI(t, mobiBook{fullName: "Thinking, Fast and Slow"})

	if meta := extract.EbookMeta(path); meta.Title != "Thinking, Fast and Slow" {
		t.Errorf("title = %q", meta.Title)
	}
}

func TestMOBIRefusesWhatItCannotRead(t *testing.T) {
	text := []byte("<p>" + englishProse + "</p>")
	cases := map[string]mobiBook{
		"DRM":       {encryption: 2},
		"HUFF/CDIC": {compression: 17480},
		"KF8 only":  {version: 8},
	}
	for label, b := range cases {
		t.Run(label, func(t *testing.T) {
			b.records, b.textLen = [][]byte{text}, len(text)
			if _, err := extract.DefaultNoteSplitter.MOBI(writeMOBI(t, b)); err == nil {
				t.Error("read a book it cannot read")
			}
		})
	}
}

func TestMOBIRefusesATruncatedFile(t *testing.T) {
	path := writeMOBI(t, mobiBook{records: [][]byte{[]byte("<p>" + englishProse + "</p>")}, textLen: 300})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The end of the last record cannot be told from a book that ends there;
	// a file cut before it can.
	record1 := int(binary.BigEndian.Uint32(data[78+8:]))
	for _, n := range []int{10, 80, record1 - 10} {
		short := filepath.Join(t.TempDir(), "short.mobi")
		if err := os.WriteFile(short, data[:n], 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := extract.DefaultNoteSplitter.MOBI(short); err == nil {
			t.Errorf("read a file cut at %d bytes", n)
		}
	}
}
