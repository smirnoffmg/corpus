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

// A text layer whose fonts decode to punctuation or to letters swapped one for
// one has no foreign letters to give it away, only no vowels. All three are
// real pages: Комбинаторика pp. 30 and 48 (issue #8), go-design-patterns p. 55.
func TestUnreadableSpotsALayerWithoutVowels(t *testing.T) {
	pages := []string{
		":#' 7 !;  7 !; %3\n'     ' $     )    ) ! !\n  ( ! )  >   + !  +;'\n   ' )   $ − 4 = ,\n" +
			"   . )!   %    0\n/    B  ;\n    )      '      )\n9 %  )   : .  1   )  )+\n" +
			" ) )  %)    +  )  .1\n +   )   9 %  ) ;   : *\n(  +;      )    )+    %)",
		"Ínénxzjtvkrq x wvkzvéntq¹uq\n©zª tj ©jª Íéq ëzvu tnrvzvén qo tj¡q}  wnénxzjtvkvr\n" +
			"vrj yzx¹ vlqtjrvkuq Ëjwéquné xzv¹qn k wnékvu xzvsi|n\n" +
			"wnénxzjtvkrq ujéz éjuz uzéj ézuj wéq ëzvp ojuntn ljlyz",
		// Every space is a \x03: the shift moved it out of print.
		"7HVWLQJ\u0003LQ\u0003*R\u0003LV\u0003XVHG\u0003E\\\u0003ZULWLQJ\u0003PHWKRGV\u0003VWDUWHG\u0003ZLWK\u0003WKH\u0003SUHIL[\u00037HVW\u000f\u0003D\u0003WHVW\u0003QDPH\u000f\u0003DQG\u0003WKH\u0003LQMHFWLRQ\u0003RI\n" +
			"WKH\u0003WHVWLQJ\u00117\u0003SRLQWHU\u0003FDOOHG\u0003W\u0011\u0003&RQWUDU\\\u0003WR\u0003RWKHU\u0003ODQJXDJHV\u000f\u0003WKHUH\u0003DUH\u0003QR\u0003DVVHUWV\u0003QRU\u0003VSHFLDO\u0003V\\QWD[\n" +
			"IRU\u0003WHVWLQJ\u0003LQ\u0003*R\u0011\u0003<RX\u0003FDQ\u0003XVH\u0003*R\u0003V\\QWD[\u0003WR\u0003FKHFN\u0003IRU\u0003HUURUV\u0003DQG\u0003\\RX\u0003FDOO\u0003W\u0003ZLWK\u0003LQIRUPDWLRQ\u0003DERXW\u0003WKH\n" +
			"HUURU\u0003LQ\u0003FDVH\u0003LW\u0003IDLOV\u0011\u0003,I\u0003WKH\u0003FRGH\u0003UHDFKHV\u0003WKH\u0003HQG\u0003RI\u0003WKH\u00037HVW\u0003IXQFWLRQ\u0003ZLWKRXW\u0003DULVLQJ\u0003HUURUV\u000f\u0003WKH\n" +
			"IXQFWLRQ\u0003KDV\u0003SDVVHG\u0003WKH\u0003WHVWV\u0011",
	}
	for _, page := range pages {
		if !unreadable(page) {
			t.Errorf("a layer without vowels was kept: %.40q…", page)
		}
	}
}

func TestUnreadableKeepsRealPagesWithSymbols(t *testing.T) {
	// Measured against the corpus: pages like these sit below the threshold.
	// Digits are left out of the count, or the table and the figure would not be.
	pages := []string{
		"935 Multivariable Calculus. What is the derivative of log(x) with respect to x, " +
			"and how does ∂f/∂x relate to the gradient ∇f when f: ℝⁿ → ℝ is differentiable?",
		"Шаг 2. Разбиение классов, полученных на шаге 1 (см. рис. 2.4), продолжается " +
			"до тех пор, пока каждый класс не окажется неразделимым по любому входу.",
		// Ovadia et al., table G.2.
		" Method               Vanilla   Temp. Scaling    Ensembles   Dropout   LL-Dropout   LL-SVI\n" +
			" Brier Score (25th)    0.553           0.551         0.503     0.577        0.550     0.590\n" +
			" Brier Score (50th)    0.733           0.726         0.667     0.754        0.723     0.766\n" +
			" NLL (25th)            1.859           1.848         1.621     1.957        1.830     2.218\n" +
			" ECE (25th)            0.057           0.031         0.022     0.017        0.034     0.065",
		// Levin, Peres, Markov Chains and Mixing Times, p. 91: a figure drawn in digits.
		"6.1. Top-to-Random Shuffle\nWe begin this chapter with an example. Consider the following (slow) method\n" +
			"of shuffling a deck of n cards: take the top card and insert it uniformly at random in\nthe deck.\n" +
			strings.Repeat("                    111111111\n                    000000000\n", 14),
		"Теорема 3. Для любых x, y ∈ V\n    |(x, y)|² ≤ (x, x)(y, y),\n    ‖x + y‖ ≤ ‖x‖ + ‖y‖,\n" +
			"    A = (aᵢⱼ), det A = Σ sgn(σ) a₁σ(₁) … aₙσ(ₙ),\nпричём равенство достигается лишь при y = λx.",
		"func (s *Store) Get(ctx context.Context, id string) (*Doc, error) {\n" +
			"\trow := s.db.QueryRowContext(ctx, `SELECT body FROM docs WHERE id = $1`, id)\n" +
			"\tvar d Doc\n\tif err := row.Scan(&d.Body); err != nil {\n\t\treturn nil, fmt.Errorf(\"get %s: %w\", id, err)\n\t}\n" +
			"\treturn &d, nil\n}",
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
	if unreadable(":#' 7 !;  7 !; %3") {
		t.Error("judged a line with almost no characters")
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
