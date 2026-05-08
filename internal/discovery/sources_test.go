package discovery_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ringo380/ccmcp/internal/discovery"
)

func TestEmbeddedSourceParses(t *testing.T) {
	src := discovery.EmbeddedSource()
	rows, err := src.Fetch(context.Background(), http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("embedded registry should ship with at least one entry")
	}
	for _, r := range rows {
		if r.Origin != "embedded" {
			t.Errorf("origin=%q for %s, want embedded", r.Origin, r.Name)
		}
	}
}

func TestUserURLSourceSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":1,"marketplaces":[{"name":"x","source":"github","repo":"a/b"}]}`))
	}))
	defer srv.Close()

	src := discovery.UserURLSource(srv.URL)
	rows, err := src.Fetch(context.Background(), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "x" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if !strings.HasPrefix(rows[0].Origin, "user:") {
		t.Errorf("origin=%q, want user:* prefix", rows[0].Origin)
	}
}

func TestUserURLSourceServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	src := discovery.UserURLSource(srv.URL)
	if _, err := src.Fetch(context.Background(), srv.Client()); err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestUserURLSourceMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	src := discovery.UserURLSource(srv.URL)
	if _, err := src.Fetch(context.Background(), srv.Client()); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestAnthropicSource404IsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	src := discovery.AnthropicSource(srv.URL)
	rows, err := src.Fetch(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("404 should not be an error, got %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("404 should yield empty, got %+v", rows)
	}
}

func TestAnthropicSourceEmptyEndpointIsNoop(t *testing.T) {
	rows, err := discovery.AnthropicSource("").Fetch(context.Background(), http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("empty endpoint should be no-op, got %+v", rows)
	}
}

func TestAwesomeListSourceParsesReadme(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("- [foo](https://github.com/owner/foo)\n- https://github.com/owner/bar\n"))
	}))
	defer srv.Close()

	// Awesome list source uses raw.githubusercontent.com URL — we can't
	// override that path through public API, so we cover the core via
	// ExtractGitHubRepos in parser_test.go and a minimal smoke against the
	// constructor to catch refactor breakage.
	src := discovery.AwesomeListSource("hesreallyhim", "awesome-claude-code", "", "")
	if id := src.ID(); !strings.HasPrefix(id, "awesome-list:") {
		t.Errorf("id=%q, want awesome-list:* prefix", id)
	}
}
