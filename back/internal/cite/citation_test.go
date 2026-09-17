package cite_test

import (
	"strings"
	"testing"

	"github.com/smirnoffmg/corpus/internal/cite"
)

func TestParseCitationReadsANumberedEnglishEntry(t *testing.T) {
	c := cite.ParseCitation("[1] J. Dean and S. Ghemawat. MapReduce: Simplified data processing on large clusters. In OSDI, pages 137–150, 2004.")

	if c.Label != "[1]" {
		t.Errorf("label = %q, want [1]", c.Label)
	}
	if c.Authors != "J. Dean and S. Ghemawat" {
		t.Errorf("authors = %q", c.Authors)
	}
	if c.Title != "MapReduce: Simplified data processing on large clusters" {
		t.Errorf("title = %q", c.Title)
	}
	if c.Year != 2004 {
		t.Errorf("year = %d, want 2004", c.Year)
	}
	if c.Raw == "" {
		t.Error("the entry as printed is the one thing that must survive")
	}
}

func TestParseCitationReadsAnAuthorYearEntryWithADOI(t *testing.T) {
	c := cite.ParseCitation("Melnik, Sergey, Sriram Raghavan, Beverly Yang, and Hector Garcia-Molina. 2001. " +
		"Building a distributed full-text index for the web. In Proc. WWW, pp. 396–406. ACM Press. DOI : doi.acm.org/10.1145/371920.372095")

	if c.Year != 2001 {
		t.Errorf("year = %d, want 2001", c.Year)
	}
	if c.Title != "Building a distributed full-text index for the web" {
		t.Errorf("title = %q", c.Title)
	}
	if c.DOI != "10.1145/371920.372095" {
		t.Errorf("doi = %q", c.DOI)
	}
	if c.Fingerprint != "10.1145/371920.372095" {
		t.Errorf("fingerprint = %q, want the DOI: it is what two papers citing the same work agree on", c.Fingerprint)
	}
}

// An initial after the family name is not a sentence end; an initial before it
// is not one either. The comma is what tells the two apart.
func TestParseCitationReadsAGOSTEntry(t *testing.T) {
	c := cite.ParseCitation("1. Клеппман, М. Высоконагруженные приложения. — СПб.: Питер, 2018. — 640 с.")

	if c.Label != "1." {
		t.Errorf("label = %q, want 1.", c.Label)
	}
	if c.Authors != "Клеппман, М." {
		t.Errorf("authors = %q", c.Authors)
	}
	if c.Title != "Высоконагруженные приложения" {
		t.Errorf("title = %q", c.Title)
	}
	if c.Year != 2018 {
		t.Errorf("year = %d, want 2018", c.Year)
	}
}

func TestParseCitationReadsAnArXivPreprint(t *testing.T) {
	c := cite.ParseCitation("[7] A. Vaswani et al. Attention is all you need. arXiv:1706.03762, 2017.")

	if c.ArXiv != "1706.03762" {
		t.Errorf("arxiv = %q", c.ArXiv)
	}
	if c.Fingerprint != "arxiv:1706.03762" {
		t.Errorf("fingerprint = %q", c.Fingerprint)
	}
}

func TestParseCitationKeepsAnUnreadableEntryWhole(t *testing.T) {
	raw := "см. там же"
	c := cite.ParseCitation(raw)

	if c.Raw != raw {
		t.Errorf("raw = %q, want %q", c.Raw, raw)
	}
	if c.Title != "" || c.Year != 0 {
		t.Errorf("read %q / %d out of a line that says nothing", c.Title, c.Year)
	}
	if c.Fingerprint == "" {
		t.Error("even an unreadable entry needs a fingerprint, or two papers citing it can never meet")
	}
}

// Without an identifier, two papers can still be seen to cite the same work by
// its title and year — but only when both are read the same way.
func TestParseCitationFingerprintsByTitleAndYear(t *testing.T) {
	a := cite.ParseCitation("[1] M. Kleppmann. Designing Data-Intensive Applications. O'Reilly, 2017.")
	b := cite.ParseCitation("Kleppmann, Martin. 2017. Designing data-intensive applications! O'Reilly Media.")

	if a.Fingerprint != b.Fingerprint {
		t.Errorf("fingerprints differ: %q and %q", a.Fingerprint, b.Fingerprint)
	}
}

func TestParseCitationTakesTheISBNOfABook(t *testing.T) {
	c := cite.ParseCitation("2. Рогов, Е. В. PostgreSQL 18 изнутри. — М.: ДМК Пресс, 2025. — ISBN 978-5-93700-283-9.")

	if c.ISBN != "9785937002839" {
		t.Errorf("isbn = %q", c.ISBN)
	}
}

// Found by running the parser over a real reference list. An initial that opens
// a name and an initial that closes one look the same locally, and the comma
// alone does not tell them apart: "S. Melnik, S. Raghavan" and "Клеппман, М."
// both have one.
func TestParseCitationReadsACommaSeparatedListOfInitialFirstNames(t *testing.T) {
	c := cite.ParseCitation("[3] S. Melnik, S. Raghavan, B. Yang and H. Garcia-Molina. " +
		"Building a distributed full-text index for the web. In Proc. WWW, pages 396-406, 2001.")

	if c.Authors != "S. Melnik, S. Raghavan, B. Yang and H. Garcia-Molina" {
		t.Errorf("authors = %q", c.Authors)
	}
	if c.Title != "Building a distributed full-text index for the web" {
		t.Errorf("title = %q", c.Title)
	}
	if c.Year != 2001 {
		t.Errorf("year = %d, want 2001", c.Year)
	}
}

func TestParseCitationReadsTwoInitialsAfterAFamilyName(t *testing.T) {
	c := cite.ParseCitation("2. Рогов, Е. В. PostgreSQL 18 изнутри. — М.: ДМК Пресс, 2025. — 672 с.")

	if c.Authors != "Рогов, Е. В." {
		t.Errorf("authors = %q", c.Authors)
	}
	if c.Title != "PostgreSQL 18 изнутри" {
		t.Errorf("title = %q", c.Title)
	}
}

// In author–year styles the year closes the author block, which is the one
// boundary there that no initial can be mistaken for. These are entries from
// papers in the library.
func TestParseCitationSplitsAuthorYearEntriesAtTheYear(t *testing.T) {
	cases := map[string]struct {
		raw, authors, title string
		year                int
	}{
		"elsevier": {
			"Alfayez, R., Alwehaibi, W., Winn, R., Venson, E., Boehm, B., 2020. A systematic literature review of technical debt prioritization. In: International Conference on Technical Debt 2020.",
			"Alfayez, R., Alwehaibi, W., Winn, R., Venson, E., Boehm, B.",
			"A systematic literature review of technical debt prioritization", 2020,
		},
		"elsevier with a letter after the year": {
			"Besker, T., Martini, A., Bosch, J., 2018a. Managing architectural technical debt: A unified model and systematic literature review. J. Syst. Softw. 135, 1–16.",
			"Besker, T., Martini, A., Bosch, J.",
			"Managing architectural technical debt: A unified model and systematic literature review", 2018,
		},
		"springer": {
			"Breiman L (1996) Bagging predictors. Mach Learn 24(2):123–140",
			"Breiman L", "Bagging predictors", 1996,
		},
		"apa": {
			"Bavota, G. and Russo, B. (2016). A large-scale empirical study on self-admitted technical debt. In MSR, pages 315–326.",
			"Bavota, G. and Russo, B.",
			"A large-scale empirical study on self-admitted technical debt", 2016,
		},
		"apa with a question for a title": {
			"Alla, S., & Adari, S. K. (2020). What is MLOps?. Beginning MLOps with MLFlow (pp. 79–124). Apress L. P..",
			"Alla, S., & Adari, S. K.", "What is MLOps?", 2020,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := cite.ParseCitation(c.raw)
			if got.Authors != c.authors {
				t.Errorf("authors = %q, want %q", got.Authors, c.authors)
			}
			if got.Title != c.title {
				t.Errorf("title = %q, want %q", got.Title, c.title)
			}
			if got.Year != c.year {
				t.Errorf("year = %d, want %d", got.Year, c.year)
			}
		})
	}
}

// A year inside a venue is not the end of an author block: what precedes it
// has to read as names.
func TestParseCitationDoesNotTakeATitleForAnAuthorBlock(t *testing.T) {
	c := cite.ParseCitation("[4] J. Smith. Testing in practice. In Proceedings of ICSE 2019 Workshops, pages 1–8.")
	if c.Authors != "J. Smith" || c.Title != "Testing in practice" {
		t.Errorf("authors = %q, title = %q", c.Authors, c.Title)
	}
}

// Sculley et al. 2015 as six papers in the library print it. "J.-F." is one
// initial; read as a word, it cut the author list and made "Crespo, and D." the
// title of a work six papers seemed to share.
func TestParseCitationKeepsHyphenatedInitialsInTheAuthorList(t *testing.T) {
	c := cite.ParseCitation("D. Sculley, G. Holt, D. Golovin, E. Davydov, T. Phillips, D. Ebner, V. Chaudhary, " +
		"M. Young, J.-F. Crespo, and D. Dennison. Hidden technical debt in machine learning systems. In NeurIPS, 2015.")
	if c.Title != "Hidden technical debt in machine learning systems" {
		t.Errorf("title = %q", c.Title)
	}
	if !strings.HasSuffix(c.Authors, "J.-F. Crespo, and D. Dennison") {
		t.Errorf("authors = %q", c.Authors)
	}
}

// IEEE sets the title in quotes after a comma, which is the one reliable mark
// of where the authors end.
func TestParseCitationReadsAQuotedTitle(t *testing.T) {
	c := cite.ParseCitation("[37] X. Ren, Z. Xing, X. Xia, D. Lo, X. Wang, and J. Grundy, “Neural network-based " +
		"detection of self-admitted technical debt: From performance to explainability,” ACM TOSEM, vol. 28, no. 3, 2019.")
	if c.Authors != "X. Ren, Z. Xing, X. Xia, D. Lo, X. Wang, and J. Grundy" {
		t.Errorf("authors = %q", c.Authors)
	}
	if c.Title != "Neural network-based detection of self-admitted technical debt: From performance to explainability" {
		t.Errorf("title = %q", c.Title)
	}
	if c.Year != 2019 {
		t.Errorf("year = %d", c.Year)
	}
}

// A DOI cut short at a line break is a publisher's prefix and nothing more:
// every Elsevier paper would share "10.1016/j", and so would the graph.
func TestParseCitationIgnoresADOIWithNoSuffixToSpeakOf(t *testing.T) {
	c := cite.ParseCitation("[5] A. Author. A title. J. Syst. Softw. 101, 2015. doi:10.1016/j.")
	if c.DOI != "" {
		t.Errorf("doi = %q, want none", c.DOI)
	}
	if strings.HasPrefix(c.Fingerprint, "10.1016") {
		t.Errorf("fingerprint = %q", c.Fingerprint)
	}
}
