package extract

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"unicode"
)

// PDFTitle recognises what a book is actually called. A filename is whatever the
// person who uploaded the scan typed — "1476.pdf", "ТЧА.pdf" — and it ends up in
// every citation, so the document is asked first.
func PDFTitle(ctx context.Context, path, fallback string) string {
	return chooseTitle(pdfInfoField(ctx, path, "Title"), firstLines(ctx, path), fallback)
}

// PDFAuthor is the Author field of the PDF metadata, empty when absent.
func PDFAuthor(ctx context.Context, path string) string { return pdfInfoField(ctx, path, "Author") }

func pdfInfoField(ctx context.Context, path, field string) string {
	out, err := exec.CommandContext(ctx, "pdfinfo", path).Output()
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(out)) {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), field) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstLines(ctx context.Context, path string) []string {
	out, err := exec.CommandContext(ctx, "pdftotext", "-f", "1", "-l", "2", "-enc", "UTF-8", path, "-").Output()
	if err != nil {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
			if len(lines) == 8 {
				break
			}
		}
	}
	return lines
}

// toolArtifact matches the titles authoring tools leave behind, which describe
// the file that produced the PDF rather than the book inside it.
var toolArtifact = regexp.MustCompile(`(?i)^(microsoft word|corel ventura|adobe|pdfcreator|untitled|unknown)|\.(chp|doc|docx|indd|tex|qxd|pmd|rtf|dvi|djv|djvu|pdf)\b|_`)

// personName matches the author line, which sits where a title is expected on
// many covers: "В. А. Зорич", "М.А. Скопина", "Matt Butcher".
var personName = regexp.MustCompile(`^(\p{Lu}\.\s*){1,3}\p{Lu}\p{Ll}+\.?$|^\p{Lu}\p{Ll}+\s+\p{Lu}\p{Ll}+$`)

// frontMatter matches the headings that open a book before its title page:
// the praise pages, the contents, the dedication.
var frontMatter = regexp.MustCompile(`(?i)^(advance )?praise for |^(table of )?contents$|^dedication$|^about the authors?$|^foreword`)

// danglingWord catches a title cut off mid-phrase, like "Cloud Native DevOps with".
var danglingWord = regexp.MustCompile(`(?i)\s(with|and|for|of|the|in|по|для|и|в|на)$`)

func chooseTitle(meta string, first []string, fallback string) string {
	// A filename that already reads as a title was written by someone who knew
	// the book; metadata is a series name, a printing date or the editor as
	// often as not, so it does not get to overrule that.
	if curatedName(fallback) {
		return fallback
	}
	if t := strings.TrimSpace(meta); plausibleTitle(t) {
		return t
	}
	// Two capitalised words are either an author ("Matt Butcher") or a short
	// title ("Communication Patterns"), and nothing in the line itself tells
	// which. So such a candidate is held back and used only if the page offers
	// nothing better.
	var weak string
	for _, line := range first {
		if !plausibleTitle(line) || words(line) < 2 || blurb(line) {
			continue
		}
		if personName.MatchString(line) {
			if weak == "" {
				weak = line
			}
			continue
		}
		return line
	}
	if weak != "" {
		return weak
	}
	return fallback
}

// curatedName spots a filename a person composed: real words, separated by
// spaces, rather than a slug, a catalogue number or a transliteration.
func curatedName(name string) bool {
	return len(name) >= 12 && words(name) >= 2 && plausibleTitle(name)
}

// blurb rejects the praise quotes and half-sentences that open many books.
func blurb(line string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasSuffix(trimmed, ",") {
		return true
	}
	for _, quote := range []string{"«", `"`, "'", "“"} {
		if strings.HasPrefix(trimmed, quote) {
			return true
		}
	}
	return false
}

func plausibleTitle(s string) bool {
	if len(s) < 8 || len(s) > 200 || toolArtifact.MatchString(s) {
		return false
	}
	if danglingWord.MatchString(s) || spacedOut(s) || frontMatter.MatchString(s) {
		return false
	}
	var letters, expected, total int
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		total++
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		// Mojibake from a mis-decoded PDF font is made of letters too — "ËÆÊÉcËÇÊË"
		// is nine of them. What gives it away is the alphabet: this library is
		// Russian and English, so Latin-1 accents mean a broken decoding rather
		// than a word. A French or German title would be misjudged here.
		if r < unicode.MaxASCII || unicode.Is(unicode.Cyrillic, r) {
			expected++
		}
	}
	if total == 0 || letters == 0 {
		return false
	}
	// Page furniture and formulas are mostly not letters.
	return float64(letters)/float64(total) > 0.7 && float64(expected)/float64(letters) > 0.8
}

func words(s string) int { return len(strings.Fields(s)) }

// spacedOut rejects letter-spaced headings such as "Б И Б Л И О Т Е Ч К А",
// which arrive as a row of one-letter words.
func spacedOut(s string) bool {
	fields := strings.Fields(s)
	if len(fields) < 4 {
		return false
	}
	single := 0
	for _, f := range fields {
		if len([]rune(f)) == 1 {
			single++
		}
	}
	return single*2 > len(fields)
}
