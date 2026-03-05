package webhook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func strPtr(s string) *string { return &s }

// newTestServer returns a test server that handles both API and raw-content calls.
func newTestServer(t *testing.T, files []commitFile, rawContent string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		if strings.HasPrefix(r.URL.Path, "/commits") {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(commitDetail{Files: files}); err != nil {
				t.Errorf("error encoding response: %v", err)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(rawContent)); err != nil {
			t.Errorf("error writing response: %v", err)
		}
	}))
}

// prefixRewriteTransport redirects all requests to a fixed test server URL.
type prefixRewriteTransport struct{ base string }

func (t *prefixRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = "http"
	cloned.URL.Host = t.base[7:] // strip "http://"
	return http.DefaultTransport.RoundTrip(cloned)
}

func newTestClient(server *httptest.Server) *Client {
	return &Client{
		token:      "",
		httpClient: &http.Client{Transport: &prefixRewriteTransport{base: server.URL}},
	}
}

// --- ParsePushEvent ----------------------------------------------------------

func TestParsePushEvent_valid(t *testing.T) {
	body := []byte(`{
        "ref": "refs/heads/main",
		"after": "abc123",
        "repository": {
			"full_name": "owner/repo",
			"contents_url": "https://example.com/contents",
			"commits_url": "https://example.com/commits"
		},
        "commits": [{"id": "aaa"}, {"id": "bbb"}]
    }`)
	event, err := ParsePushEvent(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.After != "abc123" {
		t.Errorf("After = %q, want abc123", event.After)
	}
	if len(event.Commits) != 2 {
		t.Errorf("got %d commits, want 2", len(event.Commits))
	}
}

func TestParsePushEvent_missingRepo(t *testing.T) {
	if _, err := ParsePushEvent([]byte(`{"after":"abc","commits":[]}`)); err == nil {
		t.Fatal("expected error for missing repository.full_name")
	}
}

func TestParsePushEvent_missingSHA(t *testing.T) {
	if _, err := ParsePushEvent([]byte(`{"repository":{"full_name":"owner/repo"},"commits":[]}`)); err == nil {
		t.Fatal("expected error for missing after SHA")
	}
}

// --- aggregatedChanges -------------------------------------------------------

func TestAggregate_basicUpsertAndRemove(t *testing.T) {
	agg := newAggregatedChanges()
	agg.apply([]commitFile{
		{Filename: "index.html", Status: "modified", Patch: strPtr("@@...")},
		{Filename: "old.html", Status: "removed", Patch: strPtr("@@...")},
	})
	if _, ok := agg.upsert["index.html"]; !ok {
		t.Error("expected index.html in upsert")
	}
	if _, ok := agg.removed["old.html"]; !ok {
		t.Error("expected old.html in removed")
	}
}

func TestAggregate_binaryFileIgnored(t *testing.T) {
	agg := newAggregatedChanges()
	agg.apply([]commitFile{
		{Filename: "photo.jpg", Status: "added", Patch: nil}, // binary: no patch
	})
	if _, ok := agg.upsert["photo.jpg"]; ok {
		t.Error("binary file should not appear in upsert")
	}
	if _, ok := agg.removed["photo.jpg"]; ok {
		t.Error("binary file should not appear in removed")
	}
}

func TestAggregate_addedThenRemovedInSamePush(t *testing.T) {
	agg := newAggregatedChanges()
	agg.apply([]commitFile{{Filename: "about.html", Status: "added", Patch: strPtr("@@...")}})
	agg.apply([]commitFile{{Filename: "about.html", Status: "removed", Patch: strPtr("@@...")}})
	if _, ok := agg.upsert["about.html"]; ok {
		t.Error("about.html should not be in upsert (was removed later)")
	}
	if _, ok := agg.removed["about.html"]; !ok {
		t.Error("about.html should be in removed")
	}
}

func TestAggregate_textFileTurnsBinaryInSamePush(t *testing.T) {
	agg := newAggregatedChanges()
	agg.apply([]commitFile{{Filename: "weird.bin", Status: "modified", Patch: strPtr("@@...")}})
	agg.apply([]commitFile{{Filename: "weird.bin", Status: "modified", Patch: nil}})
	if _, ok := agg.upsert["weird.bin"]; ok {
		t.Error("file that became binary should be cleared from upsert")
	}
}

func TestAggregate_renamedFile(t *testing.T) {
	agg := newAggregatedChanges()
	agg.apply([]commitFile{
		{Filename: "new-name.html", Status: "renamed", Patch: strPtr("@@...")},
		{Filename: "old-name.html", Status: "removed", Patch: strPtr("@@...")},
	})
	if _, ok := agg.upsert["new-name.html"]; !ok {
		t.Error("rename destination should be in upsert")
	}
	if _, ok := agg.removed["old-name.html"]; !ok {
		t.Error("rename source should be in removed")
	}
}

// --- ProcessPushEvent (integration) -----------------------------------------

func TestProcessPushEvent_fetchesTextSkipsBinary(t *testing.T) {
	serverFiles := []commitFile{
		{Filename: "index.html", Status: "modified", Patch: strPtr("@@...")},
		{Filename: "photo.jpg", Status: "modified", Patch: nil},
	}
	server := newTestServer(t, serverFiles, "<h1>Hello</h1>")
	defer server.Close()

	body := []byte(`{
        "ref": "refs/heads/main",
		"after": "abc123",
        "repository": {
			"full_name": "owner/repo",
			"contents_url": "https://example.com/contents",
			"commits_url": "https://example.com/commits"
		},
        "commits": [{"id": "aaa"}, {"id": "bbb"}]
    }`)

	results, err := ProcessPushEvent(body, newTestClient(server))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result (text only), got %d", len(results))
	}
	if results[0].Path != "index.html" {
		t.Errorf("unexpected path %q", results[0].Path)
	}
	if string(results[0].Content) != "<h1>Hello</h1>" {
		t.Errorf("unexpected content: %s", results[0].Content)
	}
}

func TestProcessPushEvent_deletedFileHasNoContent(t *testing.T) {
	serverFiles := []commitFile{
		{Filename: "gone.html", Status: "removed", Patch: strPtr("@@...")},
	}
	server := newTestServer(t, serverFiles, "")
	defer server.Close()

	body := []byte(`{
        "ref": "refs/heads/main",
		"after": "abc123",
        "repository": {
			"full_name": "owner/repo",
			"contents_url": "https://example.com/contents",
			"commits_url": "https://example.com/commits"
		},
        "commits": [{"id": "aaa"}, {"id": "bbb"}]
    }`)

	results, err := ProcessPushEvent(body, newTestClient(server))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Deleted {
		t.Error("expected Deleted=true")
	}
	if results[0].Content != nil {
		t.Error("expected nil Content for deleted file")
	}
}
