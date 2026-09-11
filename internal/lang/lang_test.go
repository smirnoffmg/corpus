package lang_test

import (
	"testing"

	"github.com/smirnoffmg/corpus/internal/lang"
)

func TestDetect(t *testing.T) {
	cases := map[string]string{
		"Агрегат — это граница согласованности":             lang.Russian,
		"An aggregate is a consistency boundary":            lang.English,
		"Агрегат (aggregate) в DDD — boundary консистенции": lang.Russian,
		"": lang.English,
	}
	for in, want := range cases {
		if got := lang.Detect(in); got != want {
			t.Errorf("lang.Detect(%q) = %q, want %q", in, got, want)
		}
	}
}
