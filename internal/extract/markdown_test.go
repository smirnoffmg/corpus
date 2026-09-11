package extract_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smirnoffmg/corpus/internal/extract"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMarkdownSplitsBySectionAndKeepsHeadingPath(t *testing.T) {
	chunks, err := extract.Markdown(write(t, `---
date: 2026-09-11
tags: daily
---

# День

Проснулся рано.

## Встречи

### 10:00 Планёрка

Обсудили релиз.
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2: %+v", len(chunks), chunks)
	}
	if got, want := chunks[0].Locator, "День"; got != want {
		t.Errorf("first locator = %q, want %q", got, want)
	}
	if got, want := chunks[1].Locator, "День > Встречи > 10:00 Планёрка"; got != want {
		t.Errorf("second locator = %q, want %q", got, want)
	}
	if got, want := chunks[1].Body, "Обсудили релиз."; got != want {
		t.Errorf("second body = %q, want %q", got, want)
	}
}

func TestMarkdownIgnoresHeadingsInsideFences(t *testing.T) {
	chunks, err := extract.Markdown(write(t, "# Заметка\n\n```bash\n# это комментарий\nls -la\n```\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1: %+v", len(chunks), chunks)
	}
	if got, want := chunks[0].Locator, "Заметка"; got != want {
		t.Errorf("locator = %q, want %q", got, want)
	}
}

func TestMarkdownTagsComeOnlyFromTagsKey(t *testing.T) {
	chunks, err := extract.Markdown(write(t, `---
aliases:
  - Второе имя
tags:
  - books
  - Философия
author: Кто-то
---

# Тело

Текст.
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	got := chunks[0].Tags
	want := []string{"books", "Философия"}
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags = %v, want %v", got, want)
		}
	}
}

func TestMarkdownInlineTagList(t *testing.T) {
	chunks, err := extract.Markdown(write(t, "---\ntags: [daily, diary]\n---\n\n# H\n\nx\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks[0].Tags) != 2 {
		t.Fatalf("tags = %v, want 2 items", chunks[0].Tags)
	}
}

func TestMarkdownLocatorSkipsMissingLevels(t *testing.T) {
	chunks, err := extract.Markdown(write(t, "## Что это\n\nОпределение.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := chunks[0].Locator, "Что это"; got != want {
		t.Errorf("locator = %q, want %q", got, want)
	}
}

func TestShortSectionStaysWhole(t *testing.T) {
	chunks, err := extract.Markdown(write(t, "# Заметка\n\n## Что это\n\nКороткое определение в один абзац.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 {
		t.Errorf("короткий раздел разрезан на %d частей", len(chunks))
	}
}

func TestLongSectionIsSplitAtParagraphs(t *testing.T) {
	para := strings.Repeat("Определение и механизм, изложенные словами. ", 20) // ~860 символов
	body := "# Заметка\n\n## Что это\n\n" + strings.Join([]string{para, para, para, para, para}, "\n\n") + "\n"

	chunks, err := extract.Markdown(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("раздел на %d символов остался одним чанком", len(body))
	}
	for i, c := range chunks {
		if c.Locator != "Заметка > Что это" {
			t.Errorf("часть %d потеряла заголовок: %q", i, c.Locator)
		}
		if c.Ord != i+1 {
			t.Errorf("часть %d получила ord %d — окно соседей строится по порядку", i, c.Ord)
		}
		if strings.TrimSpace(c.Body) == "" {
			t.Errorf("часть %d пустая", i)
		}
	}
}

func TestCodeFenceIsNeverCutInHalf(t *testing.T) {
	filler := strings.Repeat("Текст перед кодом, чтобы набрать длину. ", 45) // > partTarget
	code := "```python\n" + strings.Repeat("x = 1\n\ny = 2\n\n", 40) + "```"
	chunks, err := extract.Markdown(write(t, "# З\n\n## Код\n\n"+filler+"\n\n"+code+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range chunks {
		if strings.Count(c.Body, "```")%2 != 0 {
			t.Errorf("часть %d разрезала блок кода: %.60s…", i, c.Body)
		}
	}
}
