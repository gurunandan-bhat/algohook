// Package webhook handles parsing of GitHub push event payloads.
//
// Flow:
//  1. Parse the raw push-event JSON to extract the repo and the HEAD commit SHA.
//  2. For every commit SHA in the push, call the GitHub Commits API to retrieve
//     the list of changed files. Files without a "patch" field are binary — skipped.
//  3. Deduplicate paths across commits, respecting final state.
//  4. Fetch the raw content of every non-deleted, non-binary file.
package webhook

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// -----------------------------------------------------------------------------
// Webhook payload types
// -----------------------------------------------------------------------------

// PushEvent represents the top-level GitHub webhook payload for a push event.
type PushEvent struct {
	Ref        string   `json:"ref"`   // e.g. "refs/heads/main"
	After      string   `json:"after"` // SHA of HEAD after the push
	Repository Repo     `json:"repository"`
	Commits    []Commit `json:"commits"`
}

// Repo holds the repository details needed to build API URLs.
type Repo struct {
	FullName string `json:"full_name"` // "owner/repo"
	HTMLURL  string `json:"html_url"`
}

// Commit is a commit reference inside the push event.
// We only need the SHA — full file details come from the Commits API.
type Commit struct {
	ID string `json:"id"`
}

// -----------------------------------------------------------------------------
// GitHub Commits API types
// -----------------------------------------------------------------------------

// commitDetail is the response body from:
//
//	GET /repos/{owner}/{repo}/commits/{sha}
type commitDetail struct {
	Files []commitFile `json:"files"`
}

// commitFile represents one file entry inside a commitDetail.
// Patch is nil for binary files — that absence is our authoritative binary signal.
type commitFile struct {
	Filename string  `json:"filename"`
	Status   string  `json:"status"` // "added" | "modified" | "removed" | "renamed"
	Patch    *string `json:"patch"`  // nil → binary; present (even if empty) → text
}

func (f commitFile) isBinary() bool {
	return f.Patch == nil
}

// -----------------------------------------------------------------------------
// Output type
// -----------------------------------------------------------------------------

// ChangedFile is the value passed downstream to the HTML parser / Algolia indexer.
type ChangedFile struct {
	Path    string // repo-relative path, e.g. "blog/post.html"
	Content []byte // raw file bytes at HEAD; nil when Deleted is true
	Deleted bool   // true when the file was removed in this push
}

// -----------------------------------------------------------------------------
// Client — wraps all GitHub API and raw-content calls
// -----------------------------------------------------------------------------

// Client handles all communication with GitHub.
// Construct one with NewClient and reuse it across requests.
type Client struct {
	token      string
	httpClient *http.Client
}

// NewClient creates a Client. Pass an empty token only for public repositories.
func NewClient(token string) *Client {
	return &Client{
		token:      token,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) setHeaders(req *http.Request, acceptJSON bool) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("User-Agent", "algolia-indexer/1.0")
	if acceptJSON {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
}

func (c *Client) getCommitDetail(repoFullName, sha string) ([]commitFile, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/commits/%s", repoFullName, sha)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("client: build commits API request: %w", err)
	}
	c.setHeaders(req, true)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("client: GET commits API: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Printf("error closing response body: %s", err.Error())
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("client: commits API returned %d for %s@%s",
			resp.StatusCode, repoFullName, sha)
	}

	var detail commitDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return nil, fmt.Errorf("client: decode commits API response: %w", err)
	}
	return detail.Files, nil
}

func (c *Client) fetchRawFile(repoFullName, ref, path string) ([]byte, error) {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s",
		repoFullName, ref, path)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("client: build raw-content request for %q: %w", path, err)
	}
	c.setHeaders(req, false)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("client: GET raw content for %q: %w", path, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Printf("error closing response body: %s", err.Error())
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("client: file not found at ref %q: %s", ref, path)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("client: unexpected status %d for %s", resp.StatusCode, path)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("client: read raw content for %q: %w", path, err)
	}
	return data, nil
}

// -----------------------------------------------------------------------------
// Aggregation
// -----------------------------------------------------------------------------

type aggregatedChanges struct {
	upsert  map[string]struct{}
	removed map[string]struct{}
}

func newAggregatedChanges() aggregatedChanges {
	return aggregatedChanges{
		upsert:  make(map[string]struct{}),
		removed: make(map[string]struct{}),
	}
}

// apply folds one commit's file list into the running aggregated state.
func (a *aggregatedChanges) apply(files []commitFile) {
	for _, f := range files {
		if f.isBinary() {
			// Clear any earlier reference to this path — never index binary files.
			// This also handles the edge case where a file was text in an earlier
			// commit of the same push but has since become binary.
			delete(a.upsert, f.Filename)
			delete(a.removed, f.Filename)
			continue
		}
		switch f.Status {
		case "added", "modified", "renamed":
			a.upsert[f.Filename] = struct{}{}
			delete(a.removed, f.Filename)
		case "removed":
			a.removed[f.Filename] = struct{}{}
			delete(a.upsert, f.Filename)
		}
	}
}

// -----------------------------------------------------------------------------
// Top-level entry point
// -----------------------------------------------------------------------------

// ParsePushEvent deserialises a raw GitHub push-event JSON body.
func ParsePushEvent(body []byte) (*PushEvent, error) {
	var event PushEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("webhook: unmarshal push event: %w", err)
	}
	if event.Repository.FullName == "" {
		return nil, fmt.Errorf("webhook: payload missing repository.full_name")
	}
	if event.After == "" {
		return nil, fmt.Errorf("webhook: payload missing after SHA")
	}
	return &event, nil
}

// ProcessPushEvent is the single function your HTTP handler needs to call.
//
// It parses the webhook body, queries the GitHub Commits API for each commit
// to identify changed text files (binary files are automatically excluded),
// then fetches the current content of every non-deleted file.
func ProcessPushEvent(body []byte, client *Client) ([]ChangedFile, error) {
	event, err := ParsePushEvent(body)
	if err != nil {
		return nil, err
	}

	agg := newAggregatedChanges()
	for _, commit := range event.Commits {
		files, err := client.getCommitDetail(event.Repository.FullName, commit.ID)
		if err != nil {
			return nil, fmt.Errorf("processWebhook: commit %s: %w", commit.ID, err)
		}
		agg.apply(files)
	}

	var results []ChangedFile

	for path := range agg.upsert {
		content, err := client.fetchRawFile(event.Repository.FullName, event.After, path)
		if err != nil {
			return nil, fmt.Errorf("processWebhook: fetch %q: %w", path, err)
		}
		results = append(results, ChangedFile{Path: path, Content: content})
	}

	for path := range agg.removed {
		results = append(results, ChangedFile{Path: path, Deleted: true})
	}

	return results, nil
}
