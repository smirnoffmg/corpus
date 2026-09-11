package extract

import (
	"os"
	"path/filepath"
	"testing"
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
	chunks, err := Markdown(write(t, `---
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
	chunks, err := Markdown(write(t, "# Заметка\n\n```bash\n# это комментарий\nls -la\n```\n"))
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
	chunks, err := Markdown(write(t, `---
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
	chunks, err := Markdown(write(t, "---\ntags: [daily, diary]\n---\n\n# H\n\nx\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks[0].Tags) != 2 {
		t.Fatalf("tags = %v, want 2 items", chunks[0].Tags)
	}
}

func TestMarkdownLocatorSkipsMissingLevels(t *testing.T) {
	chunks, err := Markdown(write(t, "## Что это\n\nОпределение.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := chunks[0].Locator, "Что это"; got != want {
		t.Errorf("locator = %q, want %q", got, want)
	}
}
