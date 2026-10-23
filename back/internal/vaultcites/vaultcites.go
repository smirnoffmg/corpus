// Package vaultcites counts the citations in the vault's notes and tells those
// of books the library holds from the rest. A quote that reached a note is
// what the library is for, so their number is the closest measure of it that
// can be read off the vault by code.
package vaultcites

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Line is a line of a note, with the note it is in.
type Line struct {
	Note string
	Text string
}

// Tally is what a set of lines cites. Library counts the citations matched to
// a book or paper the library holds, Sources the distinct ones among them, and
// Notes the notes with at least one.
type Tally struct {
	Citations int
	Library   int
	Sources   int
	Notes     int
}

// citation is an italic title followed by a locator, the way the citing
// guide writes them: *Title*, с. 189 (PDF 203), or a chapter for an e-book.
var citation = regexp.MustCompile(`\*([^*\n]{2,200}?)\*,?\s+(?i:с\.|c\.|стр\.|pp?\.|pdf|гл\.|ch\.|chapter|глава|part)\s?[0-9IVXLC]`)

// minTitle keeps a one-word title such as *The* from matching every book that
// starts with it.
const minTitle = 5

func Count(lines []Line, library []string) Tally {
	titles := make([]string, len(library))
	for i, t := range library {
		titles[i] = normal(t)
	}
	var t Tally
	sources := map[int]bool{}
	notes := map[string]bool{}
	for _, l := range lines {
		for _, m := range citation.FindAllStringSubmatch(l.Text, -1) {
			t.Citations++
			if i := match(normal(m[1]), titles); i >= 0 {
				t.Library++
				sources[i] = true
				notes[l.Note] = true
			}
		}
	}
	t.Sources, t.Notes = len(sources), len(notes)
	return t
}

// match finds the library title the cited one begins, ending on a word: notes
// cite a book by its short title, and the library's titles carry the author
// and the topic after it.
func match(cited string, titles []string) int {
	if utf8.RuneCountInString(cited) < minTitle {
		return -1
	}
	for i, title := range titles {
		rest, ok := strings.CutPrefix(title, cited)
		if !ok {
			continue
		}
		if next, _ := utf8.DecodeRuneInString(rest); rest == "" || !unicode.IsLetter(next) && !unicode.IsDigit(next) {
			return i
		}
	}
	return -1
}

// normal folds case and ё, and drops the ellipsis a shortened title ends in.
func normal(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	s = strings.TrimSuffix(s, "…")
	s = strings.TrimSuffix(s, "...")
	return strings.Join(strings.Fields(s), " ")
}
