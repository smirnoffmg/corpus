package shrink

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiffersNamesWhatTheIndexerWouldReadDifferently(t *testing.T) {
	base := reading{layout: []string{"a", ""}, raw: []string{"a", ""}, title: "T", author: "A"}
	for _, tc := range []struct {
		name   string
		change func(*reading)
		want   string
	}{
		{"nothing", func(*reading) {}, ""},
		{"layout text", func(r *reading) { r.layout = []string{"b", ""} }, "text"},
		{"reading order", func(r *reading) { r.raw = []string{"b", ""} }, "reading-order text"},
		{"title", func(r *reading) { r.title = "U" }, "title"},
		{"author", func(r *reading) { r.author = "B" }, "author"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			tc.change(&o)
			require.Equal(t, tc.want, base.differs(o))
		})
	}
}

func TestDefaultsFillWhatWasNotSet(t *testing.T) {
	require.Equal(t, 150, or(0, defaultTextDPI))
	require.Equal(t, 200, or(200, defaultTextDPI))
	require.InDelta(t, 0.1, or(-1.0, defaultMinGain), 1e-9)
	require.Nil(t, reading{}.pages())
	require.Equal(t, "OSError: broken", lastLine("Traceback\n  line\nOSError: broken\n"))
}
