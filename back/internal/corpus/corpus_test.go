package corpus_test

import (
	"testing"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

func TestLocator(t *testing.T) {
	cases := []struct {
		name    string
		heading string
		page    int
		printed int
		want    string
	}{
		{"заметка цитируется путём заголовков", "Что это > Пример", 0, 0, "Что это > Пример"},
		{"печатный номер отличается от файлового", "", 58, 21, "с. 21 (PDF 58)"},
		{"номера совпали", "", 58, 58, "с. 58"},
		{"печатный номер не распознан", "", 256, 0, "PDF 256"},
		// A note section above its first heading has neither a heading nor a
		// page; it was cited as "PDF 0", a page of a PDF that does not exist.
		{"текст заметки до первого заголовка", "", 0, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := corpus.Locator(c.heading, c.page, c.printed); got != c.want {
				t.Errorf("Locator(%q, %d, %d) = %q, want %q", c.heading, c.page, c.printed, got, c.want)
			}
		})
	}
}
