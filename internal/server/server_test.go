package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thomasmack021/weave/web"
)

// TestHealthEndpointReturns200 is the Red-phase test for the GET /health
// liveness probe. It MUST fail until routing is implemented in the Green phase
// of Step 2: the stub Handler() serves 404 for every path.
func TestHealthEndpointReturns200(t *testing.T) {
	srv := New(web.Assets, nil, nil, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health: got status %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestServesEmbeddedIndexHTML is the Red-phase test asserting the server serves
// the embedded web/index.html at the site root. It MUST fail until the Green
// phase wires the embedded filesystem into the router.
func TestServesEmbeddedIndexHTML(t *testing.T) {
	srv := New(web.Assets, nil, nil, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: got status %d, want %d", rec.Code, http.StatusOK)
	}

	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	if !strings.Contains(string(body), `id="app"`) {
		t.Fatalf("GET /: response body did not contain the embedded index.html marker `id=\"app\"`; got:\n%s", body)
	}
}
