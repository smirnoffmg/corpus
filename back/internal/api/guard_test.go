package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smirnoffmg/corpus/internal/api"
)

func guarded(t *testing.T) http.Handler {
	t.Helper()
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })
	return api.Guard(ok, []string{"localhost", "127.0.0.1", "::1"})
}

func serve(h http.Handler, r *http.Request) int {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

// A page on another site can point its own domain at 127.0.0.1 and read the
// answers: the browser sees one origin, and only the Host header gives it away.
func TestGuardRefusesAHostNameThatIsNotThisMachine(t *testing.T) {
	h := guarded(t)
	for host, want := range map[string]int{
		"localhost:8080":        http.StatusOK,
		"127.0.0.1:8080":        http.StatusOK,
		"[::1]:8080":            http.StatusOK,
		"LOCALHOST":             http.StatusOK,
		"rebind.attacker.test":  http.StatusForbidden,
		"localhost.attacker.io": http.StatusForbidden,
		"192.168.1.20:8080":     http.StatusForbidden,
		"":                      http.StatusForbidden,
	} {
		r := httptest.NewRequest(http.MethodGet, "/search?q=дневник", http.NoBody)
		r.Host = host
		if got := serve(h, r); got != want {
			t.Errorf("Host %q: status = %d, want %d", host, got, want)
		}
	}
}

// A form on another site can post a multipart upload without a preflight.
func TestGuardRefusesCrossSiteWrites(t *testing.T) {
	h := guarded(t)

	cross := httptest.NewRequest(http.MethodPost, "/upload?kind=book", strings.NewReader("x"))
	cross.Host = "localhost:8080"
	cross.Header.Set("Sec-Fetch-Site", "cross-site")
	if got := serve(h, cross); got != http.StatusForbidden {
		t.Errorf("cross-site POST: status = %d, want 403", got)
	}

	foreign := httptest.NewRequest(http.MethodPost, "/styles", strings.NewReader("x"))
	foreign.Host = "localhost:8080"
	foreign.Header.Set("Origin", "https://attacker.test")
	if got := serve(h, foreign); got != http.StatusForbidden {
		t.Errorf("foreign Origin: status = %d, want 403", got)
	}
}

func TestGuardLetsTheUIAndNonBrowserClientsWrite(t *testing.T) {
	h := guarded(t)

	ui := httptest.NewRequest(http.MethodPut, "/bibliography?kind=book&path=a.pdf", strings.NewReader("{}"))
	ui.Host = "localhost:8081"
	ui.Header.Set("Origin", "http://localhost:8081")
	ui.Header.Set("Sec-Fetch-Site", "same-origin")
	if got := serve(h, ui); got != http.StatusOK {
		t.Errorf("the UI's own request: status = %d, want 200", got)
	}

	curl := httptest.NewRequest(http.MethodPost, "/upload?kind=book", strings.NewReader("x"))
	curl.Host = "127.0.0.1:8080"
	if got := serve(h, curl); got != http.StatusOK {
		t.Errorf("curl and MCP clients send no Origin: status = %d, want 200", got)
	}
}
