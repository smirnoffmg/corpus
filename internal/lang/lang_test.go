package lang

import "testing"

func TestDetect(t *testing.T) {
	cases := map[string]string{
		"Агрегат — это граница согласованности":             Russian,
		"An aggregate is a consistency boundary":            English,
		"Агрегат (aggregate) в DDD — boundary консистенции": Russian,
		"": English,
	}
	for in, want := range cases {
		if got := Detect(in); got != want {
			t.Errorf("Detect(%q) = %q, want %q", in, got, want)
		}
	}
}
