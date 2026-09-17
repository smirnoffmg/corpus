package extract

import (
	"strings"
	"testing"
)

const filler = "Обычная страница статьи с достаточным количеством текста, чтобы её ни с чем не спутали.\n" +
	"Здесь идёт рассуждение, потом ещё одно, и заканчивается абзац.\n"

func TestBibliographyIsNotFoundWhereThereIsNone(t *testing.T) {
	start, entries := Bibliography([]string{filler, filler, filler})
	if start != -1 || entries != nil {
		t.Errorf("start = %d, entries = %v, want none", start, entries)
	}
}

func TestBibliographyIgnoresTheWordInTheBody(t *testing.T) {
	body := "As References to earlier work show, the problem is old.\n" + filler
	start, _ := Bibliography([]string{body, filler, filler})
	if start != -1 {
		t.Errorf("start = %d, want -1: References inside a sentence is not a heading", start)
	}
}

func TestBibliographyCutsANumberedList(t *testing.T) {
	refs := `References

[1] J. Dean and S. Ghemawat. MapReduce: Simplified data processing on
    large clusters. In OSDI, pages 137–150, 2004.
[2] M. Kleppmann. Designing Data-Intensive Applications. O'Reilly, 2017.
[3] L. Lamport. Time, clocks, and the ordering of events in a distributed
    system. CACM, 21(7):558–565, 1978.
`
	start, entries := Bibliography([]string{filler, filler, refs})
	if start != 2 {
		t.Fatalf("start = %d, want 2", start)
	}
	if len(entries) != 3 {
		t.Fatalf("cut into %d entries, want 3: %q", len(entries), entries)
	}
	if !strings.HasPrefix(entries[0], "[1] J. Dean") || !strings.Contains(entries[0], "large clusters") {
		t.Errorf("entry 1 = %q, want the continuation joined on", entries[0])
	}
	if !strings.HasPrefix(entries[2], "[3] L. Lamport") {
		t.Errorf("entry 3 = %q", entries[2])
	}
}

// The library's own bibliographies are not numbered: an entry is known by
// starting further left than the lines that continue it. This page is
// Introduction to Information Retrieval, printed p. 507.
func TestBibliographyCutsAHangingIndentList(t *testing.T) {
	refs := `Bibliography

Melnik, Sergey, Sriram Raghavan, Beverly Yang, and Hector Garcia-Molina. 2001.
  Building a distributed full-text index for the web. In Proc. WWW, pp. 396–406.
  ACM Press. DOI : doi.acm.org/10.1145/371920.372095. 83, 523, 527, 529, 533
Mitchell, Tom M. 1997. Machine Learning. McGraw Hill. 286, 527
Moffat, Alistair, and Timothy A. H. Bell. 1995. In situ generation of compressed
  inverted files. JASIS 46(7):537–550. 83, 520, 527
`
	start, entries := Bibliography([]string{filler, refs})
	if start != 1 {
		t.Fatalf("start = %d, want 1", start)
	}
	if len(entries) != 3 {
		t.Fatalf("cut into %d entries, want 3: %q", len(entries), entries)
	}
	if !strings.HasPrefix(entries[0], "Melnik, Sergey") || !strings.Contains(entries[0], "371920.372095") {
		t.Errorf("entry 1 = %q", entries[0])
	}
	// The trailing run of bare numbers is the book's back-reference index —
	// the pages this work is cited on — not part of the work's description.
	if strings.HasSuffix(entries[1], "286, 527") {
		t.Errorf("entry 2 = %q, want the back references cut off", entries[1])
	}
}

func TestBibliographyReadsAGOSTList(t *testing.T) {
	refs := `Список литературы

1. Клеппман, М. Высоконагруженные приложения. Программирование, масштабирование,
   поддержка. — СПб.: Питер, 2018. — 640 с.
2. Рогов, Е. В. PostgreSQL 18 изнутри. — М.: ДМК Пресс, 2025. — 672 с.
`
	start, entries := Bibliography([]string{filler, refs})
	if start != 1 {
		t.Fatalf("start = %d, want 1", start)
	}
	if len(entries) != 2 {
		t.Fatalf("cut into %d entries, want 2: %q", len(entries), entries)
	}
	if !strings.Contains(entries[0], "Высоконагруженные приложения") || !strings.Contains(entries[0], "640 с.") {
		t.Errorf("entry 1 = %q, want the wrapped line joined on", entries[0])
	}
}

func TestBibliographyStopsAtTheNextSection(t *testing.T) {
	refs := `References

[1] M. Kleppmann. Designing Data-Intensive Applications. O'Reilly, 2017.

Appendix A. Proof of Theorem 1

Пусть дано множество, тогда для любого элемента выполняется неравенство,
которое доказывается индукцией по числу шагов алгоритма.
`
	_, entries := Bibliography([]string{filler, refs})
	if len(entries) != 1 {
		t.Fatalf("cut into %d entries, want 1: %q", len(entries), entries)
	}
	if strings.Contains(entries[0], "Appendix") {
		t.Errorf("entry 1 = %q, want the appendix left out", entries[0])
	}
}

func TestBibliographyJoinsAWordBrokenAcrossLines(t *testing.T) {
	refs := `References

[1] J. Dean. MapReduce: simplified data process-
    ing on large clusters. OSDI, 2004.
`
	_, entries := Bibliography([]string{refs})
	if len(entries) != 1 || !strings.Contains(entries[0], "processing on large clusters") {
		t.Errorf("entries = %q, want the hyphenated word joined", entries)
	}
}

func TestBibliographyTakesTheLastHeading(t *testing.T) {
	pages := []string{
		"References\n\n" + filler,
		filler,
		"References\n\n[1] M. Kleppmann. Designing Data-Intensive Applications. O'Reilly, 2017.\n",
	}
	start, entries := Bibliography(pages)
	if start != 2 {
		t.Errorf("start = %d, want the list at the end of the document", start)
	}
	if len(entries) != 1 {
		t.Errorf("entries = %q, want 1", entries)
	}
}

func TestWithoutBibliographyKeepsThePageAboveTheHeading(t *testing.T) {
	conclusion := "Заключение\n\n" + filler + "\nReferences\n\n[1] M. Kleppmann. Designing Data-Intensive Applications. O'Reilly, 2017.\n"
	pages := []string{filler, conclusion, "[2] L. Lamport. Time, clocks. CACM, 1978.\n"}

	kept := withoutBibliography(pages, pages)
	if len(kept) != 3 {
		t.Fatalf("kept %d pages, want 3 — page numbers must not shift", len(kept))
	}
	if strings.TrimSpace(kept[2]) != "" {
		t.Errorf("page 3 = %q, want it blank: it holds only references", kept[2])
	}
	if !strings.Contains(kept[1], "Заключение") {
		t.Errorf("page 2 = %q, want the conclusion kept", kept[1])
	}
	if strings.Contains(kept[1], "Kleppmann") {
		t.Errorf("page 2 = %q, want the references cut off", kept[1])
	}
}

func TestWithoutBibliographyLeavesAPaperWithNoListAlone(t *testing.T) {
	pages := []string{filler, filler}
	if kept := withoutBibliography(pages, pages); len(kept) != 2 {
		t.Errorf("kept %d pages, want both", len(kept))
	}
}

// pdftotext -raw keeps reading order on a two-column page and drops every
// indent, so an unnumbered list has no hanging indent left to cut by. What is
// left is the shape of a line that opens an entry: a family name and initials,
// after a line that closed the one before. These lists are from papers in the
// library, as -raw prints them.
func TestBibliographyCutsAnUnindentedAuthorYearList(t *testing.T) {
	cases := map[string]struct {
		refs  string
		want  int
		first string
		last  string
	}{
		"elsevier": {
			refs: `References
Alfayez, R., Alwehaibi, W., Winn, R., Venson, E., Boehm, B., 2020. A systematic
literature review of technical debt prioritization. In: International Conference
on Technical Debt 2020.
Alves, N.S., Mendes, T.S., de Mendonça, M.G., Spínola, R.O., Shull, F., Sea-
man, C., 2016. Identification and management of technical debt: A systematic
mapping study. Inf. Softw. Technol. 70, 100–121.
Avgeriou, P.C., Taibi, D., Ampatzoglou, A., Arcelli Fontana, F., Besker, T., Chatzige-
orgiou, A., Lenarduzzi, V., Martini, A., Moschou, N., Pigazzini, I., Saarimaki, N.,
Sas, D.D., de Toledo, S.S., Tsintzira, A.A., 2020. An overview and comparison
of technical debt measurement tools. IEEE Software 0-0.
Besker, T., Martini, A., Bosch, J., 2018a. Managing architectural technical debt: A
unified model and systematic literature review. J. Syst. Softw. 135, 1–16.
`,
			want: 4, first: "Alfayez, R.", last: "Besker, T.",
		},
		"springer": {
			refs: `References
Arisholm E, Briand LC, Fuglerud M (2007) Data mining techniques for building fault-proneness models in
telecom java software. In: The 18th IEEE international symposium on software reliability (ISSRE'07),
IEEE, pp 215–224
Bavota G, Russo B (2016) A large-scale empirical study on self-admitted technical debt. In: Proceedings of
the 13th international conference on mining software repositories, MSR '16, pp 315–326
Breiman L (1996) Bagging predictors. Mach Learn 24(2):123–140
Brown N, Cai Y, Guo Y, Kazman R, Kim M, Kruchten P, Lim E, MacCormack A, Nord R, Ozkaya I et al
(2010) Managing technical debt in software-reliant systems. In: Proceedings of the FSE/SDP workshop
`,
			want: 4, first: "Arisholm E", last: "Brown N",
		},
		"apa with ampersands": {
			refs: `References
Abrahamsson, P., Jedlitschka, A., Nguyen Duc, A., Felderer, M., Amasaki, S., &
Mikkonen, T. (2016). DevOps adoption benefits and challenges in practice: A case
study. Product-Focused software process improvement (pp. 590–597). Springer
International Publishing AG. https://doi.org/10.1007/978-3-319-49094-6_44
Alla, S., & Adari, S. K. (2020). What is MLOps?. Beginning MLOps with MLFlow (pp.
79–124). Apress L. P.. https://doi.org/10.1007/978-1-4842-6549-9_3
`,
			want: 2, first: "Abrahamsson, P.", last: "Alla, S.",
		},
		"author list wrapped mid-name": {
			refs: `References
Brown, T., Mann, B., Ryder, N., Subbiah, M., Kaplan, J. D., Dhariwal, P.,
Neelakantan, A., Shyam, P., Sastry, G., Askell, A., Agarwal, S., Herbert-
Voss, A., Krueger, G., Henighan, T., Child, R., Ramesh, A., Ziegler, D., Wu,
J. (2020). Language models are few-shot learners.
Bavota, G. and Russo, B. (2016). A large-scale empirical study on self-
admitted technical debt. In MSR, pages 315–326.
`,
			want: 2, first: "Brown, T.", last: "Bavota, G.",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, entries := Bibliography([]string{filler, c.refs})
			if len(entries) != c.want {
				t.Fatalf("cut into %d entries, want %d:\n%s", len(entries), c.want, strings.Join(entries, "\n---\n"))
			}
			if !strings.HasPrefix(entries[0], c.first) || !strings.HasPrefix(entries[len(entries)-1], c.last) {
				t.Errorf("first = %.40q, last = %.40q", entries[0], entries[len(entries)-1])
			}
		})
	}
}

// A volume number opening a continuation line — "12. Springer" — is not entry
// twelve of a numbered list, and two of them do not make one.
func TestBibliographyWantsANumberedListToStartAtOne(t *testing.T) {
	refs := `References
Smith, J., 2019. A study of things. J. Things
12. Springer, Berlin.
Jones, K., 2020. Another study. Proc. Stuff
14. ACM, New York.
`
	_, entries := Bibliography([]string{filler, refs})
	if len(entries) != 2 || !strings.HasPrefix(entries[0], "Smith") || !strings.HasPrefix(entries[1], "Jones") {
		t.Fatalf("entries = %q, want Smith's and Jones's", entries)
	}
}

// In -raw output a continuation line sits at the margin like any other, and
// one that happens to begin with "Index" is still part of the entry.
func TestBibliographyIsNotEndedByAWordThatOnlyLooksLikeASection(t *testing.T) {
	refs := `References
[1] J. Zobel and A. Moffat. Inverted files for text search engines.
Index compression and query evaluation. ACM Computing Surveys, 2006.
[2] M. Kleppmann. Designing Data-Intensive Applications. O'Reilly, 2017.
`
	_, entries := Bibliography([]string{filler, refs})
	if len(entries) != 2 {
		t.Fatalf("cut into %d entries, want 2: %q", len(entries), entries)
	}
}

// Word processors leave zero-width spaces behind, and pdftotext keeps them; a
// label followed by one is still a label. Seen in a preprint in the library.
func TestBibliographyReadsLabelsFollowedByInvisibleSpaces(t *testing.T) {
	refs := "References\n" +
		"1.\u200b Peláez-Sánchez, I. C., & Glasserman-Morales, L. D. (2023). Learning ecosystems.\n" +
		"2.\u200b Kasneci, E., Sessler, K. (2023). ChatGPT for good? Learning and Individual Differences.\n" +
		"Preprint, under review\u200b \u200b \u200b 155\n" +
		"3.\u200b Bai, L., Liu, X. (2023). ChatGPT: The cognitive effects.\n"
	_, entries := Bibliography([]string{filler, refs})
	if len(entries) != 3 {
		t.Fatalf("cut into %d entries, want 3: %q", len(entries), entries)
	}
	if strings.ContainsRune(entries[0], '\u200b') {
		t.Errorf("entry 1 = %q, want the invisible space gone", entries[0])
	}
}

// A soft hyphen marks where a word may break, and is not a character of it.
func TestBibliographyDropsSoftHyphens(t *testing.T) {
	refs := "References\n[1] M. Klepp\u00admann. Designing Data-Intensive Applications. O'Reilly, 2017.\n"
	_, entries := Bibliography([]string{filler, refs})
	if len(entries) != 1 || !strings.Contains(entries[0], "Kleppmann") {
		t.Errorf("entries = %q", entries)
	}
}

// A running footer repeats on every page of a long list, with the page number
// changing, and is glued onto whichever entry it interrupts.
func TestBibliographyDropsARunningFooter(t *testing.T) {
	pages := []string{
		filler,
		"References\n[1] A. Author. First work. Venue, 2020.\nPreprint, under review 154\n",
		"[2] B. Author. Second work. Venue, 2021.\nPreprint, under review 155\n",
		"[3] C. Author. Third work. Venue, 2022.\nPreprint, under review 156\n",
	}
	_, entries := Bibliography(pages)
	if len(entries) != 3 {
		t.Fatalf("cut into %d entries, want 3: %q", len(entries), entries)
	}
	for _, e := range entries {
		if strings.Contains(e, "Preprint") {
			t.Errorf("entry = %q, want the footer gone", e)
		}
	}
}

// A page range alone on a line recurs across a long list too, and it is part
// of an entry: furniture is words.
func TestBibliographyKeepsRecurringPageRanges(t *testing.T) {
	pages := []string{
		filler,
		"References\n[1] A. Author. First work. Mach Learn 24(2):\n123–140\n",
		"[2] B. Author. Second work. Mach Learn 25(1):\n11–40\n",
	}
	_, entries := Bibliography(pages)
	if len(entries) != 2 || !strings.Contains(entries[0], "123–140") {
		t.Errorf("entries = %q, want the page range kept", entries)
	}
}

// What a paper prints after its list, and what used to be swallowed into the
// last entry. Each is from a paper in the library.
func TestBibliographyEndsWhereThePaperMovesOn(t *testing.T) {
	cases := map[string]string{
		"an appendix lettered without the word": "A Author contributions\n" +
			"Joel Becker and Nate Rush designed the study.\n",
		"an appendix in capitals": "A CONSTRAINTS IN THE DATA SCHEMA\n" +
			"message Feature { optional string name = 1; }\n",
		"a numbered appendix section": "G.3 Onboarding call\n" +
			"4. I am not sure how to label things.\n",
		"a heading in capitals": "APPLYING “THEORY BUILDING”\n" +
			"Viewing programming as theory building helps us understand metaphor building.\n",
		"a biography opening with the name in capitals": "JERNEJ FLISAR received the B.Sc. and M.Sc. degrees in computer science.\n",
		"a biography":                    "Valentina Lenarduzzi is a postdoctoral researcher at the LUT University in Finland.\n",
		"a biography after a page range": "Qiao Huang is currently a Ph.D. candidate in the College of Computer Science.\n",
	}
	for name, after := range cases {
		t.Run(name, func(t *testing.T) {
			refs := "References\n" +
				"Wohlin, C., Runeson, P., Höst, M., 2012. Experimentation in Software Engineering. Springer.\n" +
				"Zhou J, Zhang H, Lo D (2012) Where should the bugs be fixed? In: ICSE. IEEE, pp 14–24\n" +
				after
			_, entries := Bibliography([]string{filler, refs})
			if len(entries) != 2 {
				t.Fatalf("cut into %d entries, want 2: %q", len(entries), entries)
			}
			if !strings.HasSuffix(entries[1], "pp 14–24") {
				t.Errorf("last entry = %q, want it to end where the list does", entries[1])
			}
		})
	}
}

// A title that wraps onto a line of its own is not a heading, even when it
// begins with a lone capital.
func TestBibliographyKeepsATitleThatWrapsAfterALoneCapital(t *testing.T) {
	refs := "References\n" +
		"Besker, T., Martini, A., Bosch, J., 2018a. Managing architectural technical debt:\n" +
		"A Unified Model and Systematic Literature Review\n" +
		"J. Syst. Softw. 135, 1–16.\n" +
		"Wohlin, C., 2012. Experimentation in Software Engineering. Springer.\n"
	_, entries := Bibliography([]string{filler, refs})
	if len(entries) != 2 || !strings.Contains(entries[0], "Unified Model") {
		t.Errorf("entries = %q", entries)
	}
}

// Chicago writes given names in full, which is still the start of an entry.
func TestBibliographyCutsAListWithFullGivenNames(t *testing.T) {
	refs := `References
Marsden, Peter V., and James D. Wright. Handbook of Survey
Research. Emerald Group Publishing, 2010.
Merriam, Sharan B., and Elizabeth J. Tisdell. Qualitative Research:
A Guide to Design and Implementation. John Wiley & Sons,
2015.
Neuman, William Lawrence. Social Research Methods: Qualitative
and Quantitative Approaches. Harlow: Pearson Education,
2013.
`
	_, entries := Bibliography([]string{filler, refs})
	if len(entries) != 3 {
		t.Fatalf("cut into %d entries, want 3: %q", len(entries), entries)
	}
}

// Only the list leaves the index. An appendix after it is text like any other,
// and it keeps its page numbers.
func TestWithoutBibliographyKeepsWhatComesAfterTheList(t *testing.T) {
	pages := []string{
		filler,
		"References\n[1] A. Author. First work. Venue, 2020.\n",
		"[2] B. Author. Second work. Venue, 2021.\nA Author contributions\nJoel Becker designed the study and wrote the analysis.\n",
		"B Survey questions\nWhat best describes how you edit AI generated code?\n",
	}
	kept := withoutBibliography(pages, pages)
	if len(kept) != len(pages) {
		t.Fatalf("kept %d pages, want %d — page numbers must not shift", len(kept), len(pages))
	}
	if strings.Contains(kept[1], "First work") || strings.Contains(kept[2], "Second work") {
		t.Errorf("the list is still there: %q / %q", kept[1], kept[2])
	}
	if !strings.Contains(kept[2], "Joel Becker designed") || !strings.Contains(kept[3], "Survey questions") {
		t.Errorf("the appendix is gone: %q / %q", kept[2], kept[3])
	}
}

// A DOI wraps at its dots and slashes, and the join must not put a space into
// it. From an Elsevier reference in the library.
func TestBibliographyJoinsADOIBrokenAcrossLines(t *testing.T) {
	refs := "References\n" +
		"[1] A. Author. A title. J. Syst. Softw. 101, 2015. doi:10.1016/j.\n" +
		"jss.2015.05.001\n" +
		"[2] D. Minbaeva. MNC knowledge transfer. JIBS 34, 2003. doi:10.1057/palgrave.\n" +
		"jibs.8400056.\n"
	_, entries := Bibliography([]string{filler, refs})
	if len(entries) != 2 {
		t.Fatalf("entries = %q", entries)
	}
	if !strings.Contains(entries[0], "10.1016/j.jss.2015.05.001") || !strings.Contains(entries[1], "10.1057/palgrave.jibs.8400056") {
		t.Errorf("entries = %q, want the DOIs whole", entries)
	}
}

// Small capitals come out of a PDF letter-spaced: "R EFERENCES".
func TestBibliographyReadsALetterSpacedHeading(t *testing.T) {
	refs := "R EFERENCES\n[1] M. Kleppmann. Designing Data-Intensive Applications. O'Reilly, 2017.\n" +
		"[2] L. Lamport. Time, clocks. CACM, 1978.\n"
	start, entries := Bibliography([]string{filler, refs})
	if start != 1 || len(entries) != 2 {
		t.Errorf("start = %d, entries = %q", start, entries)
	}
}

// On a two-column page the layout text sets the heading beside the other
// column, so the list is found in the reading-order text and cut out of the
// layout text page by page. From breck2019, where the references stayed in the
// index because the layout text has no line that is only the heading.
func TestWithoutBibliographyFindsTheListInReadingOrderAndCutsTheLayout(t *testing.T) {
	layout := []string{
		filler,
		"We conclude that validation matters.        and the schema evolves with the data.\n" +
			"R EFERENCES                                   Ding, H., Trajcevski, G. Querying and mining\n" +
			"Baylor, D., Breck, E. TFX: A TensorFlow-     of time series data. VLDB, 2008.\n",
		"Witten, I. H. Data Mining. Morgan Kaufmann.   Zhang, Y. Something else. 2019.\n",
	}
	raw := []string{
		filler,
		"We conclude that validation matters.\nand the schema evolves with the data.\nREFERENCES\n" +
			"Baylor, D., Breck, E. TFX: A TensorFlow-based platform. KDD, 2017.\n" +
			"Ding, H., Trajcevski, G. Querying and mining of time series data. VLDB, 2008.\n",
		"Witten, I. H. Data Mining. Morgan Kaufmann, 2011.\nZhang, Y. Something else. 2019.\n",
	}
	kept := withoutBibliography(layout, raw)
	if len(kept) != 3 {
		t.Fatalf("kept %d pages, want 3", len(kept))
	}
	if !strings.Contains(kept[1], "We conclude") {
		t.Errorf("page 2 = %q, want the conclusion above the heading", kept[1])
	}
	if strings.Contains(kept[1], "Baylor") || strings.Contains(kept[2], "Witten") {
		t.Errorf("the list is still indexed: %q / %q", kept[1], kept[2])
	}
}
