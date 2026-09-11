package extract

import (
	"bufio"
	"os"
	"slices"
	"strings"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// Markdown splits a note into one corpus.Chunk per heading section. Frontmatter is not
// indexed as text, but its tags ride along on every chunk of the note.
func Markdown(path string) ([]corpus.Chunk, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		chunks   []corpus.Chunk
		tags     []string
		trail    []string // heading titles by level, index 0 == H1
		body     strings.Builder
		inFence  bool
		inMatter bool
		inTags   bool
		first    = true
	)

	flush := func() {
		if strings.TrimSpace(body.String()) == "" {
			body.Reset()
			return
		}
		chunks = append(chunks, corpus.Chunk{
			Ord:     len(chunks) + 1,
			Locator: joinTrail(trail),
			Tags:    tags,
			Body:    strings.TrimSpace(body.String()),
		})
		body.Reset()
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()

		if first {
			first = false
			if strings.TrimSpace(line) == "---" {
				inMatter = true
				continue
			}
		}
		if inMatter {
			if strings.TrimSpace(line) == "---" {
				inMatter = false
			} else {
				tags, inTags = appendTags(tags, line, inTags)
			}
			continue
		}

		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		}
		// Dataview and shell blocks are full of lines starting with '#';
		// only a '#' outside a fence is a heading.
		if level := headingLevel(line); level > 0 && !inFence {
			flush()
			trail = setTrail(trail, level, strings.TrimSpace(line[level:]))
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flush()

	for i := range chunks {
		chunks[i].Tags = slices.Clone(tags)
	}
	return chunks, nil
}

// joinTrail skips empty levels: a note that opens at H2 with no H1 above it
// would otherwise be cited as " > Section".
func joinTrail(trail []string) string {
	kept := make([]string, 0, len(trail))
	for _, t := range trail {
		if t != "" {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, " > ")
}

func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(line) || line[n] != ' ' {
		return 0
	}
	return n
}

func setTrail(trail []string, level int, title string) []string {
	if level > len(trail) {
		grown := make([]string, level)
		copy(grown, trail)
		trail = grown
	}
	trail = trail[:level]
	trail[level-1] = title
	return trail
}

// appendTags reads both `tags: a, b` and the `  - a` list form; the vault uses
// each in different templates. inTags carries over between lines so that list
// items under `aliases:` or `author:` are not mistaken for tags.
func appendTags(tags []string, line string, inTags bool) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if rest, ok := strings.CutPrefix(trimmed, "- "); ok {
		if inTags {
			if v := cleanTag(rest); v != "" {
				tags = append(tags, v)
			}
		}
		return tags, inTags
	}

	key, value, ok := strings.Cut(trimmed, ":")
	if !ok || strings.ContainsAny(key, " \t") {
		return tags, inTags
	}
	if key != "tags" {
		return tags, false
	}
	value = strings.Trim(strings.TrimSpace(value), "[]")
	for _, part := range strings.Split(value, ",") {
		if v := cleanTag(part); v != "" {
			tags = append(tags, v)
		}
	}
	return tags, true
}

func cleanTag(s string) string {
	return strings.Trim(strings.TrimSpace(s), `"'#`)
}
