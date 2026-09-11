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
