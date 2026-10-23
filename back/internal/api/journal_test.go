package api_test

import (
	"context"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/smirnoffmg/corpus/internal/api"
	"github.com/smirnoffmg/corpus/internal/corpus"
)

type read struct {
	origin string
	id     int64
}

type fakeJournal struct {
	mu       sync.Mutex
	searches []corpus.SearchRecord
	reads    []read
	window   time.Duration
	filter   corpus.SearchFilter
}

func (j *fakeJournal) LogSearch(_ context.Context, r corpus.SearchRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.searches = append(j.searches, r)
	return nil
}

func (j *fakeJournal) LogRead(_ context.Context, origin string, id int64) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.reads = append(j.reads, read{origin, id})
	return nil
}

func (j *fakeJournal) Usage(_ context.Context, window time.Duration) ([]corpus.Usage, error) {
	j.window = window
	return []corpus.Usage{{Origin: "mcp", Searches: 3, Empty: 1, Read: 1}}, nil
}

func (j *fakeJournal) Searches(_ context.Context, f corpus.SearchFilter) ([]corpus.LoggedSearch, error) {
	j.filter = f
	return []corpus.LoggedSearch{{Origin: "mcp", Query: "nothing here", Mode: "fts"}}, nil
}

// The agents search over MCP and the UI and the evaluation over HTTP; telling
// them apart is the point, since the agents are the ones nobody watches.
func TestSearchesAndReadsAreJournalledWithWhereTheyCameFrom(t *testing.T) {
	store := &fakeStore{text: []corpus.Hit{{ID: 7}, {ID: 9}}}
	journal := &fakeJournal{}
	svc := api.New(store, &fakeEmbedder{}, api.WithJournal(journal))

	ctx := context.Background()
	serverSide, clientSide := mcp.NewInMemoryTransports()
	if _, err := svc.MCP().Connect(ctx, serverSide, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, call := range []*mcp.CallToolParams{
		{Name: "corpus_search", Arguments: map[string]any{"query": "walking skeleton", "mode": "fts", "kind": "book"}},
		{Name: "corpus_read", Arguments: map[string]any{"id": 7}},
	} {
		if _, err := session.CallTool(ctx, call); err != nil {
			t.Fatal(err)
		}
	}

	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	var out any
	get(t, srv.URL+"/search?q=outcome", &out)
	get(t, srv.URL+"/read?id=9", &out)

	journal.mu.Lock()
	defer journal.mu.Unlock()
	want := []corpus.SearchRecord{
		{Origin: "mcp", Query: "walking skeleton", Mode: "fts", Kind: "book", Hits: []int64{7, 9}},
		{Origin: "http", Query: "outcome", Mode: "hybrid", Hits: []int64{7, 9}},
	}
	if !reflect.DeepEqual(journal.searches, want) {
		t.Errorf("searches = %+v, want %+v", journal.searches, want)
	}
	if wantReads := []read{{"mcp", 7}, {"http", 9}}; !reflect.DeepEqual(journal.reads, wantReads) {
		t.Errorf("reads = %+v, want %+v", journal.reads, wantReads)
	}
}

func TestStatusSumsUpTheWeeksSearchesAndSearchesListsThem(t *testing.T) {
	journal := &fakeJournal{}
	srv := httptest.NewServer(api.New(&fakeStore{}, &fakeEmbedder{}, api.WithJournal(journal)).Handler())
	defer srv.Close()

	var status struct {
		Usage []corpus.Usage `json:"usage"`
	}
	get(t, srv.URL+"/status", &status)
	if len(status.Usage) != 1 || status.Usage[0].Searches != 3 || journal.window != 7*24*time.Hour {
		t.Errorf("usage = %+v over %v, want the journal's sums over a week", status.Usage, journal.window)
	}

	var listed struct {
		Searches []corpus.LoggedSearch `json:"searches"`
	}
	get(t, srv.URL+"/searches?origin=mcp&empty=1", &listed)
	if len(listed.Searches) != 1 || listed.Searches[0].Query != "nothing here" {
		t.Errorf("searches = %+v", listed.Searches)
	}
	if want := (corpus.SearchFilter{Origin: "mcp", Empty: true, Limit: 50}); journal.filter != want {
		t.Errorf("filter = %+v, want %+v", journal.filter, want)
	}
}
