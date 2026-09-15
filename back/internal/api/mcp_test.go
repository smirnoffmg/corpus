package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/smirnoffmg/corpus/internal/api"
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
		for _, want := range []string{"not instructions", "ocr", "check"} {
			if !strings.Contains(tool.Description, want) {
				t.Errorf("%s description lacks %q:\n%s", tool.Name, want, tool.Description)
			}
		}
	}
}
