package extract

import "testing"

func TestChooseTitlePrefersUsableMetadata(t *testing.T) {
	got := chooseTitle("Введение в современную криптографию", []string{"А.Ю. Нестеренко"}, "ТЧА")
	if want := "Введение в современную криптографию"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
}

func TestChooseTitleRejectsAuthoringToolArtifacts(t *testing.T) {
	// Real metadata from this library.
	for _, meta := range []string{"Corel Ventura - TITUL.CHP", "Microsoft Word - lecture1.doc", "untitled", "PhD"} {
		got := chooseTitle(meta, []string{"Комбинаторика и теория графов"}, "файл")
		if want := "Комбинаторика и теория графов"; got != want {
			t.Errorf("metadata %q gave title %q, want %q", meta, got, want)
		}
	}
}

func TestChooseTitleSkipsMojibakeAndFurniture(t *testing.T) {
	first := []string{"ËÆÊÉcËÇÊË ¬ËÆÊÉcËÇÊË", "2013", "Издательство «Наука»"}
	if got := chooseTitle("", first, "Комбинаторика"); got != "Издательство «Наука»" {
		// The first plausible multi-word line wins; garbage and a bare year lose.
		t.Errorf("title = %q, want the first readable line", got)
	}
}

func TestChooseTitleFallsBackToTheFilename(t *testing.T) {
	if got := chooseTitle("", []string{"123", "..."}, "Ерофеева"); got != "Ерофеева" {
		t.Errorf("title = %q, want the filename", got)
	}
}

func TestChooseTitleKeepsAFilenameSomebodyComposed(t *testing.T) {
	// Metadata here is the series, the printing date and the editor — all worse
	// than the names already on disk.
	cases := []struct{ meta, filename string }{
		{"Foundations and Trends", "Architecture of a Database System (Hellerstein, Stonebraker, устройство СУБД)"},
		{"First Printing: July 5, 2023", "An Introduction to Statistical Learning with Python"},
		{"designing data intensive applications", "Designing Data-Intensive Applications (Kleppmann, системы данных)"},
	}
	for _, c := range cases {
		if got := chooseTitle(c.meta, []string{"P. BAXENDALE,"}, c.filename); got != c.filename {
			t.Errorf("title = %q, want the filename %q", got, c.filename)
		}
	}
}

func TestChooseTitleStillRescuesAnOpaqueFilename(t *testing.T) {
	if got := chooseTitle("Техническая защита информации", nil, "1476"); got != "Техническая защита информации" {
		t.Errorf("title = %q, want the recognised one", got)
	}
	if got := chooseTitle("", []string{"ТЕОРИЯ ВСПЛЕСКОВ и её приложения"}, "Скопина"); got == "Скопина" {
		t.Error("an opaque filename was kept even though the first page names the book")
	}
}

func TestChooseTitleIgnoresPraiseQuotes(t *testing.T) {
	first := []string{"«Очень хорошая книга, обстоятельная, ясная,", "Курс аспиранта"}
	if got := chooseTitle("PhD", first, "Aspirantura-Kurs"); got != "Курс аспиранта" {
		t.Errorf("title = %q, want the line after the blurb", got)
	}
}

func TestChooseTitleRejectsTheAuthorLine(t *testing.T) {
	// All of these were recognised as titles before: they are the author.
	for _, line := range []string{"В. А. Зорич", "М.А. Скопина", "Matt Butcher", "И. Ю. Винокурова"} {
		if got := chooseTitle("", []string{line, "Курс математического анализа"}, "Zorich-1"); got == line {
			t.Errorf("author line %q was taken as the title", line)
		}
	}
}

func TestChooseTitleRejectsFilenameMetadataAndSpacedHeadings(t *testing.T) {
	if got := chooseTitle("sicp.dvi", nil, "sicp"); got != "sicp" {
		t.Errorf("title = %q, want the filename — the metadata is a filename too", got)
	}
	if got := chooseTitle("", []string{"Б И Б Л И О Т Е Ч К А", "Мины и приборы"}, "Miny_Prib"); got == "Б И Б Л И О Т Е Ч К А" {
		t.Error("a letter-spaced heading was taken as the title")
	}
	if got := chooseTitle("Cloud Native DevOps with", nil, "k8s_для_devops"); got != "k8s_для_devops" {
		t.Errorf("title = %q, want the filename — the metadata is cut off", got)
	}
}
