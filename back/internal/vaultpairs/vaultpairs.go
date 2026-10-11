// Package vaultpairs derives judged queries from a vault's own structure: an
// alias names its note, a link's display text names its target, and a link to
// a heading names that section. Nobody writes the queries, so nobody's sense of
// what the system can answer leaks into them.
//
// The pairs are known-item topics, one relevant note each, which is what MRR was
// made to measure (Sanderson, Test Collection Based Evaluation of IR Systems,
// p. 284). Anchor text is also a ranking signal in its own right (Manning et al.,
// IIR, p. 463): once the index carries aliases or link text, pairs from the same
// source measure the index against itself, which is why every pair keeps its
// Source.
package vaultpairs

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/kljensen/snowball/english"
	"github.com/kljensen/snowball/russian"

	"github.com/smirnoffmg/corpus/internal/extract"
)

// Pair is one judged query.
type Pair struct {
	Query   string
	Note    string  // the target note, relative to the vault root
	Section string  // the heading a link names; empty means the whole note
	Source  string  // alias, link or section
	Share   float64 // share of the query's stems that the target's text contains
}

type note struct {
	rel      string
	title    string
	aliases  []string
	text     string
	sections map[string]string // heading → text of the section and its subsections
	raw      string
}

// Collect reads every note the indexer would index and returns the pairs its
// aliases and links give, each once.
func Collect(root string) ([]Pair, error) {
	notes, err := readNotes(root)
	if err != nil {
		return nil, err
	}
	byName := map[string][]*note{}
	for _, n := range notes {
		key := strings.ToLower(n.title)
		byName[key] = append(byName[key], n)
	}

	type candidate struct {
		pair   Pair
		target *note
	}
	var candidates []candidate
	for _, n := range notes {
		if n.text == "" {
			continue
		}
		for _, a := range n.aliases {
			candidates = append(candidates, candidate{Pair{Query: a, Note: n.rel, Source: "alias"}, n})
		}
	}
	for _, n := range notes {
		for _, m := range wikilink.FindAllStringSubmatch(n.raw, -1) {
			embed, target, heading, display := m[1], m[2], m[3], m[4]
			if embed != "" || strings.HasPrefix(heading, "#^") {
				continue
			}
			t := resolve(byName, target)
			if t == nil || t == n || t.text == "" {
				continue
			}
			display = strings.TrimSpace(strings.TrimPrefix(display, "|"))
			if heading == "" {
				if display != "" {
					candidates = append(candidates, candidate{Pair{Query: display, Note: t.rel, Source: "link"}, t})
				}
				continue
			}
			parts := strings.Split(strings.TrimPrefix(heading, "#"), "#")
			section := strings.TrimSpace(parts[len(parts)-1])
			if _, ok := t.sections[section]; !ok {
				continue
			}
			if display == "" {
				display = section
			}
			candidates = append(candidates, candidate{Pair{Query: display, Note: t.rel, Section: section, Source: "section"}, t})
		}
	}

	// A text that names several notes names none of them: a diary's «Завтра»
	// links every day to the next, and no ranking could pick the right day.
	// It is the anchor text a web engine discounts like a common term (IIR,
	// p. 463: "click" and "here").
	named := map[string]map[string]bool{}
	for _, c := range candidates {
		key := strings.ToLower(c.pair.Query)
		if named[key] == nil {
			named[key] = map[string]bool{}
		}
		named[key][c.pair.Note+"\x00"+c.pair.Section] = true
	}

	var pairs []Pair
	seen := map[string]bool{}
	for _, c := range candidates {
		p, t := c.pair, c.target
		key := strings.ToLower(p.Query)
		if len(named[key]) > 1 || strings.EqualFold(p.Query, t.title) {
			continue
		}
		text := t.text
		if p.Section != "" {
			text = t.sections[p.Section]
		}
		share, ok := stemShare(p.Query, text)
		if !ok {
			continue
		}
		p.Share = share
		id := key + "\x00" + p.Note + "\x00" + p.Section
		if seen[id] {
			continue
		}
		seen[id] = true
		pairs = append(pairs, p)
	}
	return pairs, nil
}

// Sample keeps at most perSource pairs of each source, chosen by a hash of the
// pair, so the same vault gives the same set whatever order it was read in.
func Sample(pairs []Pair, perSource int) []Pair {
	sorted := slices.Clone(pairs)
	slices.SortFunc(sorted, func(a, b Pair) int { return strings.Compare(digest(a), digest(b)) })
	taken := map[string]int{}
	var out []Pair
	for _, p := range sorted {
		if taken[p.Source] < perSource {
			taken[p.Source]++
			out = append(out, p)
		}
	}
	return out
}

func digest(p Pair) string {
	sum := sha256.Sum256([]byte(p.Source + "\x00" + p.Query + "\x00" + p.Note + "\x00" + p.Section))
	return string(sum[:])
}

// wikilink captures an embed mark, the target, a #heading or #^block, and a
// |display text.
var wikilink = regexp.MustCompile(`(!?)\[\[([^\]|#]*)(#[^\]|]*)?(\|[^\]]*)?\]\]`)

// resolve finds a link's target the way Obsidian does: by the note's name, or by
// the end of its path when the link spells one. A name two notes share, given
// without a path, names neither.
func resolve(byName map[string][]*note, target string) *note {
	target = strings.TrimSuffix(strings.TrimSpace(target), ".md")
	if target == "" {
		return nil
	}
	candidates := byName[strings.ToLower(filepath.Base(target))]
	if strings.Contains(target, "/") {
		var matched []*note
		suffix := strings.ToLower(target) + ".md"
		for _, c := range candidates {
			rel := strings.ToLower(c.rel)
			if rel == suffix || strings.HasSuffix(rel, "/"+suffix) {
				matched = append(matched, c)
			}
		}
		candidates = matched
	}
	if len(candidates) != 1 {
		return nil
	}
	return candidates[0]
}

func readNotes(root string) ([]*note, error) {
	var notes []*note
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		n, err := readNote(root, path)
		if err != nil {
			return err
		}
		notes = append(notes, n)
		return nil
	})
	return notes, err
}

func readNote(root, path string) (*note, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	// The splitter the indexer uses, so a section here is a section there.
	chunks, err := extract.DefaultNoteSplitter.Markdown(path)
	if err != nil {
		return nil, err
	}
	n := &note{
		rel:      filepath.ToSlash(rel),
		title:    strings.TrimSuffix(filepath.Base(path), ".md"),
		aliases:  extract.NoteAliases(string(raw)),
		sections: map[string]string{},
		raw:      string(raw),
	}
	var all strings.Builder
	for _, c := range chunks {
		all.WriteString(c.Body)
		all.WriteByte('\n')
		for heading := range strings.SplitSeq(c.Heading, " > ") {
			if heading != "" {
				n.sections[heading] += c.Body + "\n"
			}
		}
	}
	n.text = all.String()
	return n, nil
}

// stemShare is the share of the query's content words whose stem occurs in the
// text. A query of stop words alone gives no tsquery, and is no query.
func stemShare(query, text string) (float64, bool) {
	words := stems(query)
	if len(words) == 0 {
		return 0, false
	}
	have := map[string]bool{}
	for _, w := range stems(text) {
		have[w] = true
	}
	found := 0
	for _, w := range words {
		if have[w] {
			found++
		}
	}
	return float64(found) / float64(len(words)), true
}

func stems(s string) []string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(words))
	for _, w := range words {
		if cyrillic(w) {
			if !russian.IsStopWord(w) {
				out = append(out, russian.Stem(w, true))
			}
		} else if !english.IsStopWord(w) {
			out = append(out, english.Stem(w, true))
		}
	}
	return out
}

func cyrillic(w string) bool {
	for _, r := range w {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}
