package bibfile_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/bibfile"
	"github.com/smirnoffmg/corpus/internal/corpus"
)

// memory is a bibliography table with the import rule the real store applies:
// a file entry wins when the table has none, or an older one.
type memory struct {
	mu     sync.Mutex
	refs   map[string]corpus.Reference
	styles map[string]corpus.StyleXML
}

func newMemory(refs ...corpus.Reference) *memory {
	m := &memory{refs: map[string]corpus.Reference{}, styles: map[string]corpus.StyleXML{}}
	for _, r := range refs {
		m.refs[r.Key] = r
	}
	return m
}

func (m *memory) WithBibliography(_ context.Context, fn func([]corpus.Reference, []corpus.StyleXML) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	refs := make([]corpus.Reference, 0, len(m.refs))
	for _, r := range m.refs {
		refs = append(refs, r)
	}
	styles := make([]corpus.StyleXML, 0, len(m.styles))
	for _, s := range m.styles {
		styles = append(styles, s)
	}
	return fn(refs, styles)
}

func (m *memory) ImportReferences(_ context.Context, refs []corpus.Reference) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, r := range refs {
		if have, ok := m.refs[r.Key]; ok && !have.UpdatedAt.Before(r.UpdatedAt) {
			continue
		}
		m.refs[r.Key] = r
		n++
	}
	return n, nil
}

func (m *memory) ImportStyles(_ context.Context, styles []corpus.StyleXML) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, s := range styles {
		if have, ok := m.styles[s.ID]; ok && have.XML == s.XML {
			continue
		}
		m.styles[s.ID] = s
		n++
	}
	return n, nil
}

var t0 = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

func ref(key, citekey, title string, at time.Time) corpus.Reference {
	return corpus.Reference{Key: key, CiteKey: citekey, Status: "checked", UpdatedAt: at,
		CSL: corpus.CSL{"type": "book", "title": title}}
}

func TestExportWritesADiffableFileSortedByCitekey(t *testing.T) {
	dir := t.TempDir()
	store := newMemory(ref("h2", "zeta2020", "Z", t0), ref("h1", "alpha2019", "Альфа", t0))
	store.styles["nature"] = corpus.StyleXML{ID: "nature", Title: "Nature", XML: "<style/>"}

	require.NoError(t, bibfile.New(store, dir).Export(context.Background()))

	raw, err := os.ReadFile(filepath.Join(dir, "references.json"))
	require.NoError(t, err)
	require.True(t, strings.Contains(string(raw), "\n  "), "pretty-printed, so a change is a readable diff")
	require.Contains(t, string(raw), "Альфа", "not escaped to \\u sequences")
	var file struct {
		References []corpus.Reference `json:"references"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))
	require.Len(t, file.References, 2)
	require.Equal(t, "alpha2019", file.References[0].CiteKey)
	require.Equal(t, "h1", file.References[0].Key, "the key is what ties a description to its source")

	style, err := os.ReadFile(filepath.Join(dir, "styles", "nature.csl"))
	require.NoError(t, err)
	require.Equal(t, "<style/>", string(style))

	leftovers, err := filepath.Glob(filepath.Join(dir, ".*"))
	require.NoError(t, err)
	require.Empty(t, leftovers, "no temporary files are left behind")
}

// The point of the file: the database volume can be lost, and the hand-checked
// descriptions come back from the library directory.
func TestAnEmptyDatabaseIsRestoredFromTheFile(t *testing.T) {
	dir := t.TempDir()
	before := newMemory(ref("h1", "knuth1968", "TAOCP", t0))
	before.styles["nature"] = corpus.StyleXML{ID: "nature", Title: "Nature", XML: "<style>n</style>"}
	require.NoError(t, bibfile.New(before, dir).Export(context.Background()))

	after := newMemory()
	n, err := bibfile.New(after, dir).Import(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, "knuth1968", after.refs["h1"].CiteKey)
	require.True(t, after.refs["h1"].UpdatedAt.Equal(t0), "the time survives the round trip")
	require.Equal(t, "Nature", after.styles["nature"].Title)
}

func TestImportTakesTheNewerSideOfEachDescription(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, bibfile.New(newMemory(
		ref("edited-by-hand", "a", "from the file", t0.Add(time.Hour)),
		ref("edited-in-ui", "b", "stale in the file", t0),
	), dir).Export(context.Background()))

	store := newMemory(
		ref("edited-by-hand", "a", "stale in the database", t0),
		ref("edited-in-ui", "b", "from the database", t0.Add(time.Hour)),
	)
	n, err := bibfile.New(store, dir).Import(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, "from the file", store.refs["edited-by-hand"].CSL["title"])
	require.Equal(t, "from the database", store.refs["edited-in-ui"].CSL["title"])
}

func TestWithoutAFileImportIsANoOp(t *testing.T) {
	n, err := bibfile.New(newMemory(), t.TempDir()).Import(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestAMalformedFileIsRefusedNotApplied(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "references.json"), []byte("{not json"), 0o600))
	store := newMemory(ref("h1", "k", "kept", t0))

	_, err := bibfile.New(store, dir).Import(context.Background())
	require.Error(t, err)
	require.Equal(t, "kept", store.refs["h1"].CSL["title"])
}

// Exporting an unchanged bibliography every pass would touch the file every
// fifteen minutes, and every backup would think it changed.
func TestAnUnchangedBibliographyIsNotRewritten(t *testing.T) {
	dir := t.TempDir()
	file := bibfile.New(newMemory(ref("h1", "k", "T", t0)), dir)
	require.NoError(t, file.Export(context.Background()))
	path := filepath.Join(dir, "references.json")
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))

	require.NoError(t, file.Export(context.Background()))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, info.ModTime().Equal(old), "rewritten although nothing changed")
}

// The file is edited by hand, so a style id is not trusted as a path.
func TestAStyleIDThatIsAPathIsRefused(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "references.json"),
		[]byte(`{"references": [], "styles": [{"id": "../../etc/passwd", "title": "x"}]}`), 0o600))
	_, err := bibfile.New(newMemory(), dir).Import(context.Background())
	require.Error(t, err)
}
