package vaultpairs_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/smirnoffmg/corpus/internal/vaultpairs"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func find(pairs []vaultpairs.Pair, query string) (vaultpairs.Pair, bool) {
	for _, p := range pairs {
		if p.Query == query {
			return p, true
		}
	}
	return vaultpairs.Pair{}, false
}

func collect(t *testing.T, root string) []vaultpairs.Pair {
	t.Helper()
	pairs, err := vaultpairs.Collect(root)
	if err != nil {
		t.Fatal(err)
	}
	return pairs
}

func TestAnAliasNamesItsNote(t *testing.T) {
	root := t.TempDir()
	write(t, root, "brain/Кросс-энтропия.md", "---\naliases:\n  - cross-entropy\n  - log loss\ntags: [ml]\n---\n## Что это\n\nМера расхождения распределений.\n")

	pairs := collect(t, root)
	p, ok := find(pairs, "cross-entropy")
	if !ok {
		t.Fatalf("no pair for the alias in %+v", pairs)
	}
	if p.Note != "brain/Кросс-энтропия.md" || p.Source != "alias" || p.Section != "" {
		t.Errorf("got %+v, want the whole note, source alias", p)
	}
	if _, ok := find(pairs, "log loss"); !ok {
		t.Error("the second alias is missing")
	}
}

func TestAnInlineAliasListCounts(t *testing.T) {
	root := t.TempDir()
	write(t, root, "BM25.md", "---\naliases: [Okapi BM25, \"best matching\"]\n---\nФормула ранжирования.\n")

	pairs := collect(t, root)
	for _, q := range []string{"Okapi BM25", "best matching"} {
		if _, ok := find(pairs, q); !ok {
			t.Errorf("no pair for %q in %+v", q, pairs)
		}
	}
}

func TestALinksDisplayTextNamesItsTarget(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/RAG.md", "## Что это\n\nПоиск плюс генерация.\n")
	write(t, root, "b/Лекция.md", "Сегодня про [[RAG|генерацию с поиском]] и [[RAG]].\n")

	pairs := collect(t, root)
	p, ok := find(pairs, "генерацию с поиском")
	if !ok {
		t.Fatalf("no pair for the display text in %+v", pairs)
	}
	if p.Note != "a/RAG.md" || p.Source != "link" {
		t.Errorf("got %+v, want a/RAG.md from a link", p)
	}
	if _, ok := find(pairs, "RAG"); ok {
		t.Error("a bare link repeats the title and says nothing a title search does not")
	}
}

func TestALinkToAHeadingNamesTheSection(t *testing.T) {
	root := t.TempDir()
	write(t, root, "BM25.md", "## Что это\n\nФормула.\n\n## Как устроено\n\n### Насыщение\n\nЧастота слова насыщается.\n")
	write(t, root, "Конспект.md", "См. [[BM25#Насыщение|насыщение частоты]] и [[BM25#Как устроено]].\n")

	pairs := collect(t, root)
	p, ok := find(pairs, "насыщение частоты")
	if !ok {
		t.Fatalf("no pair for the section link in %+v", pairs)
	}
	if p.Section != "Насыщение" || p.Source != "section" {
		t.Errorf("got %+v, want section Насыщение", p)
	}
	q, ok := find(pairs, "Как устроено")
	if !ok || q.Section != "Как устроено" {
		t.Errorf("a heading link without display text is asked by its heading: got %+v, %v", q, ok)
	}
}

func TestLinksThatNameNothingIndexedAreLeftOut(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x/Note.md", "## Раздел\n\nТекст.\n")
	write(t, root, "y/Note.md", "## Раздел\n\nДругой текст.\n")
	write(t, root, "Пусто.md", "---\naliases: [void]\n---\n")
	write(t, root, ".obsidian/Скрытое.md", "---\naliases: [hidden]\n---\nТекст.\n")
	write(t, root, "Ссылки.md", "[[Note|двусмысленная]] [[x/Note#Нет такого|битый раздел]] "+
		"![[x/Note|вставка]] [[x/Note#^abc123|блок]] [[Ссылки#Сам|на себя]] [[Нет заметки|никуда]] "+
		"[[x/Note|тут]]\n\n## Сам\n\nТекст.\n")

	pairs := collect(t, root)
	for _, q := range []string{"двусмысленная", "битый раздел", "вставка", "блок", "на себя", "никуда", "void", "hidden", "тут"} {
		if p, ok := find(pairs, q); ok {
			t.Errorf("pair %+v should have been left out", p)
		}
	}
}

func TestAPathInTheLinkResolvesAnAmbiguousName(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x/Note.md", "Текст про индексы.\n")
	write(t, root, "y/Note.md", "Другой текст.\n")
	write(t, root, "Ссылки.md", "[[x/Note|индексы в базе]]\n")

	p, ok := find(collect(t, root), "индексы в базе")
	if !ok || p.Note != "x/Note.md" {
		t.Errorf("got %+v, %v; want x/Note.md", p, ok)
	}
}

func TestShareCountsStemsTheTargetRepeats(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Эмбеддинг.md", "---\naliases:\n  - векторных представлений\n  - word vectors\n---\n## Что это\n\nВекторные представления слов.\n")

	pairs := collect(t, root)
	ru, _ := find(pairs, "векторных представлений")
	if ru.Share != 1 {
		t.Errorf("inflected Russian query: share = %v, want 1", ru.Share)
	}
	en, _ := find(pairs, "word vectors")
	if en.Share != 0 {
		t.Errorf("English query on a Russian note: share = %v, want 0", en.Share)
	}
}

func TestShareIgnoresStopWords(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Индекс.md", "---\naliases:\n  - индекс для поиска\n---\nИнвертированный индекс.\n")

	p, _ := find(collect(t, root), "индекс для поиска")
	if math.Abs(p.Share-0.5) > 1e-9 {
		t.Errorf("share = %v, want 0.5: «для» is a stop word, «поиска» is absent", p.Share)
	}
}

func TestSampleIsStableAndCappedPerSource(t *testing.T) {
	var pairs []vaultpairs.Pair
	for i := range 30 {
		pairs = append(pairs,
			vaultpairs.Pair{Query: string(rune('a'+i)) + " alias", Note: "n.md", Source: "alias"},
			vaultpairs.Pair{Query: string(rune('a'+i)) + " link", Note: "n.md", Source: "link"})
	}
	first := vaultpairs.Sample(pairs, 10)
	if len(first) != 20 {
		t.Fatalf("got %d pairs, want 10 of each source", len(first))
	}
	reversed := make([]vaultpairs.Pair, len(pairs))
	for i, p := range pairs {
		reversed[len(pairs)-1-i] = p
	}
	again := vaultpairs.Sample(reversed, 10)
	for i := range first {
		if first[i] != again[i] {
			t.Fatalf("sample depends on input order: %+v vs %+v", first[i], again[i])
		}
	}
}

func TestATextThatNamesSeveralNotesNamesNone(t *testing.T) {
	root := t.TempDir()
	write(t, root, "2024-01-01.md", "Запись дня.\n\n[[2024-01-02|Завтра]]\n")
	write(t, root, "2024-01-02.md", "Запись дня.\n\n[[2024-01-03|Завтра]] [[2024-01-01|Вчера]]\n")
	write(t, root, "2024-01-03.md", "Запись дня.\n\n[[2024-01-02|Вчера]]\n")
	write(t, root, "Бустинг.md", "---\naliases: [boosting]\n---\nАнсамбль.\n")
	write(t, root, "Градиентный бустинг.md", "---\naliases: [boosting, GBM]\n---\nАнсамбль деревьев.\n")

	pairs := collect(t, root)
	for _, q := range []string{"Завтра", "Вчера", "boosting"} {
		if p, ok := find(pairs, q); ok {
			t.Errorf("pair %+v should have been left out: the text names more than one note", p)
		}
	}
	if _, ok := find(pairs, "GBM"); !ok {
		t.Error("an alias only one note has is still a pair")
	}
}
