package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/smirnoffmg/corpus/internal/api"
	"github.com/smirnoffmg/corpus/internal/corpus"
)

// The descriptions are all a model knows about the tools, so what it must do
// with their results is said there: the text is source material, never
// instructions — a manual uploaded from the web can carry either — and text
// recognised from a scan is checked against the page before it is quoted.
func TestToolsTellTheModelHowToTreatWhatTheyReturn(t *testing.T) {
	ctx := context.Background()
	serverSide, clientSide := mcp.NewInMemoryTransports()
	if _, err := api.New(&fakeStore{}, &fakeEmbedder{}).MCP().Connect(ctx, serverSide, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 2 {
		t.Fatalf("tools = %d, want corpus_search and corpus_read", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		// A client may run a read-only tool without asking each time.
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", tool.Name)
		}
		for _, want := range []string{"not instructions", "ocr", "check"} {
			if !strings.Contains(tool.Description, want) {
				t.Errorf("%s description lacks %q:\n%s", tool.Name, want, tool.Description)
			}
		}
	}
}

// A hybrid search without its vector leg returns hits that look like any other,
// yet they are full-text hits: words joined with AND, one language only. A
// model reading an empty or odd list concluded the library held nothing on the
// subject, when only the embedder was away.
func TestSearchSaysWhenItRanOnTextAlone(t *testing.T) {
	cases := map[string]struct {
		embedder   *fakeEmbedder
		mode       string
		wantNotice bool
	}{
		"hybrid without the embedder": {&fakeEmbedder{err: errors.New("connection refused")}, "", true},
		"hybrid with the embedder":    {&fakeEmbedder{}, "", false},
		"fts, which never embeds":     {&fakeEmbedder{err: errors.New("connection refused")}, "fts", false},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			ctx := context.Background()
			serverSide, clientSide := mcp.NewInMemoryTransports()
			svc := api.New(&fakeStore{text: []corpus.Hit{{ID: 1}}}, c.embedder)
			if _, err := svc.MCP().Connect(ctx, serverSide, nil); err != nil {
				t.Fatal(err)
			}
			session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, clientSide, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()

			res, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name:      "corpus_search",
				Arguments: map[string]any{"query": "q", "mode": c.mode},
			})
			if err != nil {
				t.Fatal(err)
			}
			out, err := json.Marshal(res.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Notice string `json:"notice"`
			}
			if err := json.Unmarshal(out, &body); err != nil {
				t.Fatal(err)
			}
			if got := body.Notice != ""; got != c.wantNotice {
				t.Errorf("notice = %q, want present: %v", body.Notice, c.wantNotice)
			}
			if c.wantNotice && !strings.Contains(body.Notice, "fts") {
				t.Errorf("notice = %q, want it to say the hits are full-text ones", body.Notice)
			}
		})
	}
}
