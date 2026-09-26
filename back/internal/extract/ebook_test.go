package extract_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/extract"
)

// prose is long enough to pass the floor a section has to clear to be indexed.
var prose = strings.Repeat("Никита и Мишка пошли на деревню через сад и пруд короткой дорогой. ", 4)

func writeEPUB(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "book.epub")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	files["mimetype"] = "application/epub+zip"
	files["META-INF/container.xml"] = `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func xhtml(body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>fb2converter</title></head><body>` +
		body + `</body></html>`
}

func epubChunks(t *testing.T, path string) []corpus.Chunk {
	t.Helper()
	chunks, err := extract.DefaultNoteSplitter.EPUB(path)
	if err != nil {
		t.Fatal(err)
	}
	return chunks
}

func headings(chunks []corpus.Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.Heading
	}
	return out
}

func TestEPUBCitesASectionByTheBooksOwnContents(t *testing.T) {
	path := writeEPUB(t, map[string]string{
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package version="2.0" xmlns="http://www.idpf.org/2007/opf">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Детство Никиты</dc:title></metadata>
  <manifest>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="toc" href="toc.xhtml" media-type="application/xhtml+xml"/>
    <item id="c1" href="Text/ch%201.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="Text/ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine toc="ncx"><itemref idref="toc"/><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`,
		"OEBPS/toc.ncx": `<?xml version="1.0"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/"><navMap>
  <navPoint><navLabel><text>Часть   первая</text></navLabel><content src="Text/ch%201.xhtml"/>
    <navPoint><navLabel><text>СОЛНЕЧНОЕ УТРО</text></navLabel><content src="Text/ch%201.xhtml#morning"/></navPoint>
    <navPoint><navLabel><text>СУГРОБЫ</text></navLabel><content src="Text/ch2.xhtml#drifts"/></navPoint>
  </navPoint>
</navMap></ncx>`,
		// The book's own contents page is links and nothing else.
		"OEBPS/toc.xhtml": xhtml(`<p><a href="Text/ch%201.xhtml">Часть первая. Солнечное утро, в котором Никита просыпается</a></p>
			<p><a href="Text/ch2.xhtml#drifts">Сугробы, по которым Никита катается на скамейке с горы до самого пруда</a></p>
			<p><a href="Text/ch2.xhtml">Аркадий Иванович, учитель, который приходит в самый неподходящий час</a></p>`),
		"OEBPS/Text/ch 1.xhtml": xhtml(`<p>Вступление. ` + prose + `</p>
			<div id="morning" class="titleblock"><p class="title">СОЛНЕЧНОЕ УТРО</p></div><p>Утро. ` + prose + `</p>`),
		"OEBPS/Text/ch2.xhtml": xhtml(`<p>Утро, продолжение. ` + prose + `</p>
			<section><header id="drifts"><h1>СУГРОБЫ</h1></header><p>Сугробы. ` + prose + `</p></section>`),
	})

	chunks := epubChunks(t, path)
	want := []string{"Часть первая", "Часть первая > СОЛНЕЧНОЕ УТРО", "Часть первая > СУГРОБЫ"}
	if got := headings(chunks); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("headings = %q, want %q", got, want)
	}
	// The next file carries on the section the last one ended in.
	if !strings.Contains(chunks[1].Body, "\n\nУтро, продолжение.") {
		t.Errorf("the section's second file is missing: %q", chunks[1].Body)
	}
	for i, prefix := range []string{"Вступление.", "СОЛНЕЧНОЕ УТРО", "СУГРОБЫ"} {
		if !strings.HasPrefix(chunks[i].Body, prefix) {
			t.Errorf("chunk %d starts %q, want %q", i, chunks[i].Body[:40], prefix)
		}
		if chunks[i].Ord != i+1 || chunks[i].Page != 0 {
			t.Errorf("chunk %d: ord %d page %d", i, chunks[i].Ord, chunks[i].Page)
		}
	}
}

func TestEPUBWithoutContentsFallsBackToItsHeadings(t *testing.T) {
	path := writeEPUB(t, map[string]string{
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package version="3.0" xmlns="http://www.idpf.org/2007/opf">
  <manifest><item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="c1"/></spine>
</package>`,
		"OEBPS/ch1.xhtml": xhtml(`<section><header><h1>Getting Started</h1></header>
			<p>Title page, too short to cite.</p>
			<h2>Installing the Tools</h2><p>` + prose + `</p>
			<script>tracking()</script></section>`),
	})

	chunks := epubChunks(t, path)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1: %+v", len(chunks), chunks)
	}
	if chunks[0].Heading != "Getting Started > Installing the Tools" {
		t.Errorf("heading = %q", chunks[0].Heading)
	}
	if strings.Contains(chunks[0].Body, "tracking") {
		t.Errorf("script leaked into the text: %q", chunks[0].Body)
	}
}

func TestEPUBContentsFromAnEPUB3NavDocument(t *testing.T) {
	path := writeEPUB(t, map[string]string{
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package version="3.0" xmlns="http://www.idpf.org/2007/opf">
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="nav"/><itemref idref="c1"/></spine>
</package>`,
		"OEBPS/nav.xhtml": xhtml(`<nav epub:type="toc"><ol>
			<li><a href="ch1.xhtml">Глава первая</a><ol><li><a href="ch1.xhtml#s2">Шифр</a></li></ol></li>
			</ol></nav>`),
		"OEBPS/ch1.xhtml": xhtml(`<p>Начало. ` + prose + `</p><p id="s2">Шифр. ` + prose + `</p>`),
	})

	if got := headings(epubChunks(t, path)); strings.Join(got, "|") != "Глава первая|Глава первая > Шифр" {
		t.Errorf("headings = %q", got)
	}
}

func TestEPUBMeta(t *testing.T) {
	path := writeEPUB(t, map[string]string{
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package version="2.0" xmlns="http://www.idpf.org/2007/opf" xmlns:opf="http://www.idpf.org/2007/opf">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Индустрии будущего</dc:title>
    <dc:creator opf:file-as="Росс, Алек" opf:role="aut">Алек  Росс</dc:creator>
    <dc:creator opf:role="trl">Переводчик Неважный</dc:creator>
    <dc:creator opf:file-as="Неизв.">Jon Bodner</dc:creator>
    <dc:identifier opf:scheme="uuid">144821d3-f1be-11e6-9b47-0cc47a5203ba</dc:identifier>
    <dc:identifier opf:scheme="ISBN">978-5-17-086985-5</dc:identifier>
  </metadata>
  <manifest/><spine/>
</package>`,
	})

	meta := extract.EbookMeta(path)
	want := extract.Meta{Title: "Индустрии будущего", Author: "Росс, Алек; Jon Bodner", ISBN: "978-5-17-086985-5"}
	if meta != want {
		t.Errorf("meta = %+v, want %+v", meta, want)
	}
}

const fb2Book = `<?xml version="1.0" encoding="windows-1251"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
 <description>
  <title-info>
   <author><first-name>Алексей</first-name><middle-name>Николаевич</middle-name><last-name>Толстой</last-name></author>
   <book-title>Детство Никиты</book-title>
   <annotation><p>Аннотация не индексируется.</p></annotation>
  </title-info>
  <publish-info><isbn>5-08-000001-1</isbn></publish-info>
 </description>
 <body>
  <title><p>Детство Никиты</p></title>
  <section>
   <title><p>Часть</p><p>первая</p></title>
   <epigraph><p>Эпиграф &amp; ещё кое-что.</p></epigraph>
   <section>
    <title><p>СОЛНЕЧНОЕ УТРО</p></title>
    <p>Утро. ` + "PROSE" + `</p>
    <empty-line/>
    <poem><stanza><v>Строка стиха</v></stanza></poem>
   </section>
   <section>
    <title><p>СУГРОБЫ</p></title>
    <p>Сугробы<a l:href="#n1" type="note">1</a>. ` + "PROSE" + `</p>
   </section>
  </section>
 </body>
 <body name="notes"><section id="n1"><title><p>1</p></title><p>Сноска.</p></section></body>
 <binary id="cover.jpg" content-type="image/jpeg">/9j/4AAQSkZJRgABAQ==</binary>
</FictionBook>`

func writeFB2(t *testing.T) string {
	t.Helper()
	text := strings.ReplaceAll(fb2Book, "PROSE", prose)
	encoded, err := charmap.Windows1251.NewEncoder().String(text)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "book.fb2")
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFB2CitesASectionByItsTitles(t *testing.T) {
	chunks, err := extract.DefaultNoteSplitter.FB2(writeFB2(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Часть первая > СОЛНЕЧНОЕ УТРО", "Часть первая > СУГРОБЫ"}
	if got := headings(chunks); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("headings = %q, want %q", got, want)
	}
	if !strings.HasPrefix(chunks[0].Body, "Утро. Никита") || !strings.HasSuffix(chunks[0].Body, "\n\nСтрока стиха") {
		t.Errorf("first section = %q", chunks[0].Body)
	}
	for _, c := range chunks {
		for _, leaked := range []string{"Сноска", "Аннотация", "Эпиграф", "9j/4AAQ"} {
			if strings.Contains(c.Body, leaked) {
				t.Errorf("%q leaked into %q", leaked, c.Body)
			}
		}
	}
}

func TestFB2Meta(t *testing.T) {
	meta := extract.EbookMeta(writeFB2(t))
	want := extract.Meta{Title: "Детство Никиты", Author: "Толстой, Алексей Николаевич", ISBN: "5-08-000001-1"}
	if meta != want {
		t.Errorf("meta = %+v, want %+v", meta, want)
	}
}

// XHTML closes an empty element with "/>", which an HTML parser ignores: an
// empty <title/> swallowed a whole book into its title, and an O'Reilly index
// mark <a/> turned the rest of its paragraph into a link.
func TestEPUBReadsSelfClosedElementsAsXHTML(t *testing.T) {
	path := writeEPUB(t, map[string]string{
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package version="2.0" xmlns="http://www.idpf.org/2007/opf">
  <manifest><item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="c1"/></spine>
</package>`,
		"OEBPS/ch1.xhtml": `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml">
<head><title/><link rel="stylesheet" href="style.css"/></head>
<body><div class="section"/><h1>Глава VIII</h1>
<p><a data-type="indexterm" id="ix1"/>` + prose + `<br/>` + prose + `</p></body></html>`,
	})

	chunks := epubChunks(t, path)
	if len(chunks) != 1 || chunks[0].Heading != "Глава VIII" {
		t.Fatalf("chunks = %+v", chunks)
	}
}
