package extract

import "strings"

// NoteAliases reads `aliases:` (or `alias:`) from a note's frontmatter, as a
// block list, an inline list or a single value. In a vault that is where a note
// keeps the other names it answers to: the English term, an abbreviation, a
// spelling variant.
func NoteAliases(raw string) []string {
	if !strings.HasPrefix(raw, "---\n") {
		return nil
	}
	matter, _, ok := strings.Cut(raw[4:], "\n---")
	if !ok {
		return nil
	}
	var out []string
	inList := false
	for line := range strings.SplitSeq(matter, "\n") {
		if key, value, ok := strings.Cut(line, ":"); ok && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "-") {
			inList = false
			if key != "aliases" && key != "alias" {
				continue
			}
			value = strings.TrimSpace(value)
			if value == "" {
				inList = true
				continue
			}
			for v := range strings.SplitSeq(strings.Trim(value, "[]"), ",") {
				out = appendAlias(out, v)
			}
			continue
		}
		if item, ok := strings.CutPrefix(strings.TrimSpace(line), "- "); ok && inList {
			out = appendAlias(out, item)
		}
	}
	return out
}

func appendAlias(out []string, v string) []string {
	v = strings.Trim(strings.TrimSpace(v), `"'`)
	if v == "" {
		return out
	}
	return append(out, v)
}
