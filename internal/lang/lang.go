package lang

import "unicode"

const (
	Russian = "russian"
	English = "english"
)

// cyrillicShare above which a text is treated as Russian. A genuinely English
// page carries almost no Cyrillic, while a Russian page about software is often
// half Latin terms, so an even split has to resolve to Russian: losing Russian
// stemming costs far more than stemming English words with the Russian rules.
const cyrillicShare = 0.25

func Detect(s string) string {
	var cyr, lat int
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			cyr++
		case unicode.Is(unicode.Latin, r):
			lat++
		}
	}
	if cyr == 0 {
		return English
	}
	if float64(cyr)/float64(cyr+lat) >= cyrillicShare {
		return Russian
	}
	return English
}
