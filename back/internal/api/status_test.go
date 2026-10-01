package api_test

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/smirnoffmg/corpus/internal/api"
)

func statusOf(t *testing.T, opts ...api.Option) map[string]any {
	t.Helper()
	srv := httptest.NewServer(api.New(&fakeStore{}, &fakeEmbedder{}, opts...).Handler())
	defer srv.Close()
	var status map[string]any
	get(t, srv.URL+"/status", &status)
	return status
}

func TestStatusReportsTheReranker(t *testing.T) {
	cases := map[string]struct {
		opts []api.Option
		want string
	}{
		"not configured": {nil, "off"},
		"answering":      {[]api.Option{api.WithReranker(&fakeReranker{})}, "ok"},
		"down":           {[]api.Option{api.WithReranker(&fakeReranker{err: errors.New("connection refused")})}, "unreachable"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := statusOf(t, c.opts...)["reranker"]; got != c.want {
				t.Errorf("reranker = %v, want %s", got, c.want)
			}
		})
	}
}

func TestStatusSaysWhetherUploadsAreAccepted(t *testing.T) {
	if got := statusOf(t)["uploads"]; got != false {
		t.Errorf("uploads without a library = %v, want false", got)
	}
	if got := statusOf(t, api.WithLibrary(&fakeLibrary{}, 1<<20))["uploads"]; got != true {
		t.Errorf("uploads with a library = %v, want true", got)
	}
}

func TestStatusEchoesTheSettingsItRunsWith(t *testing.T) {
	settings := statusOf(t, api.WithSettings(map[string]string{"model": "bge-m3"}))["settings"]
	if m, ok := settings.(map[string]any); !ok || m["model"] != "bge-m3" {
		t.Errorf("settings = %v, want model bge-m3", settings)
	}
}
