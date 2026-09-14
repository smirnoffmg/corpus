package extract

import (
	"strings"
	"testing"
)

func TestFrontOrBackMatterSpotsAContentsPage(t *testing.T) {
	page := strings.Repeat("8.4 Scalability . . . . . . . . . . . . . . . . . . . 213\n", 12)
	if !frontOrBackMatter(page) {
		t.Error("a page of dot leaders was kept")
	}
}

func TestFrontOrBackMatterSpotsASubjectIndex(t *testing.T) {
	page := `Index 505
unconditionally secure, 333, 335
plaintext equivalence, 178
message authentication, 12, 44, 91
public key, 201–204
digital signature, 77
hash function, 61, 62
key exchange, 155
nonce, 88
padding oracle, 240
stream cipher, 19, 20
block cipher, 21
`
	if !frontOrBackMatter(page) {
		t.Error("a subject index was kept")
	}
}

func TestFrontOrBackMatterKeepsRealPages(t *testing.T) {
	// Both were dropped by a cruder digit-density rule: a figure caption about
	// numeric formats, and a table of address ranges. Both are content.
	pages := []string{
		"Figure 7-6. Different numerical formats with their range and precision. " +
			"Formats with more bits can represent a wider range of values, at the cost " +
			"of memory. FP32 uses 8 bits for the exponent and 23 for the fraction, " +
			"while BF16 keeps the exponent and truncates the fraction to 7 bits.",
		"IPv4 CLASSFUL IP RANGES\nA 0.0.0.0 - 127.255.255.255\nB 128.0.0.0 - 191.255.255.255\n" +
			"C 192.0.0.0 - 223.255.255.255\nD 224.0.0.0 - 239.255.255.255 multicast\n" +
			"E 240.0.0.0 - 255.255.255.255 reserved for future use and research\n",
	}
	for _, page := range pages {
		if frontOrBackMatter(page) {
			t.Errorf("real content was dropped: %.60s…", page)
		}
	}
}

func TestFrontOrBackMatterDropsADivider(t *testing.T) {
	if !frontOrBackMatter("Part II  Specific Advances in Steganalysis") {
		t.Error("a part divider was kept")
	}
}

func TestUnreadableSpotsMisdecodedText(t *testing.T) {
	// Both are real pages from this library: Cyrillic read through the wrong
	// font encoding, in two different flavours.
	pages := []string{
		"ɤɢɧɭɬɵɣ ɝɨɪɲɨɤ, ɚ ɡɚɬɟɦ ɜ ɩɟɪɟɜɟɪɧɭɬɨɦ ɜɢɞɟ ɜɵɤɥɚɞɵɜɚɥɚ ɧɚ ɫɬɨɥ ɢ ɫɧɨɜɚ",
		"Î÷åâèäíî, ÷òî íà îñíîâå ñêàëîãðàììû ìîæíî ââåñòè åùå îäíó õàðàêòåðèñòèêó",
	}
	for _, page := range pages {
		if !unreadable(page) {
			t.Errorf("mojibake was kept: %.40s…", page)
		}
	}
}

func TestUnreadableKeepsRealPagesWithSymbols(t *testing.T) {
	// Measured against the corpus: pages like these sit below the threshold.
	pages := []string{
		"935 Multivariable Calculus. What is the derivative of log(x) with respect to x, " +
			"and how does ∂f/∂x relate to the gradient ∇f when f: ℝⁿ → ℝ is differentiable?",
		"Шаг 2. Разбиение классов, полученных на шаге 1 (см. рис. 2.4), продолжается " +
			"до тех пор, пока каждый класс не окажется неразделимым по любому входу.",
	}
	for _, page := range pages {
		if unreadable(page) {
			t.Errorf("a real page was dropped: %.40s…", page)
		}
	}
}

func TestUnreadableIgnoresPagesTooShortToJudge(t *testing.T) {
	if unreadable("Î÷åâ") {
		t.Error("judged a page with almost no letters")
	}
}

func TestEveryPartIsCheckedNotOnlyTheWholePage(t *testing.T) {
	// A page that splits leaves its running head as a part of its own. Checking
	// only the whole page let those through: 12% of the corpus was fragments
	// like "page 508" before this.
	for _, fragment := range []string{"page 508", "Patterns_book.indb 136 22.04.2008 21:59:55", "—"} {
		if !tooShort(fragment) {
			t.Errorf("fragment kept: %q", fragment)
		}
	}
	if tooShort(strings.Repeat("Осмысленный текст. ", 12)) {
		t.Error("a real paragraph was called too short")
	}
}
