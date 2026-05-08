package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// debugLog is the package-level logger for raw Jira API traffic. Enabled
// by setting IHJ_LOG=<path>; nil otherwise. Initialised lazily on first
// use so import-time costs are zero in the common case.
var (
	debugLogOnce sync.Once
	debugLog     *log.Logger
)

func dbg() *log.Logger {
	debugLogOnce.Do(func() {
		path := os.Getenv("IHJ_LOG")
		if path == "" {
			return
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ihj: cannot open IHJ_LOG=%s: %v\n", path, err)
			return
		}
		debugLog = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
		debugLog.Printf("=== ihj log opened (pid=%d) ===", os.Getpid())
	})
	return debugLog
}

// truncate caps a string at n bytes for log readability.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(+" + fmt.Sprintf("%d", len(s)-n) + " bytes)"
}

// API is the interface for all Jira operations. The concrete Client implements
// it against the real REST API; MockClient implements it with in-memory data
// for demo mode and testing.
type API interface {
	SearchIssues(ctx context.Context, req searchRequest) (*searchResponse, error)
	FetchTransitions(ctx context.Context, issueKey string) ([]transition, error)
	DoTransition(ctx context.Context, issueKey, transitionID string) error
	FetchMyself(ctx context.Context) (*user, error)
	AssignIssue(ctx context.Context, issueKey, accountID string) error
	CreateIssue(ctx context.Context, payload map[string]any) (*createdIssue, error)
	UpdateIssue(ctx context.Context, issueKey string, payload map[string]any) error
	AddComment(ctx context.Context, issueKey string, adfBody map[string]any) error
	FetchActiveSprint(ctx context.Context, boardID int) (*sprint, error)
	FetchNextFutureSprint(ctx context.Context, boardID int) (*sprint, error)
	FetchSprints(ctx context.Context, boardID int, states []string) ([]sprint, error)
	AddToSprint(ctx context.Context, sprintID int, issueKeys []string) error
	MoveToBacklog(ctx context.Context, issueKeys []string) error
	FetchIssue(ctx context.Context, issueKey string) (*issue, error)
	FetchBoardConfig(ctx context.Context, boardID int) (*boardConfiguration, error)
	FetchFilter(ctx context.Context, filterID string) (*jiraFilter, error)
	FetchFields(ctx context.Context) ([]fieldDefinition, error)
	FetchStatuses(ctx context.Context) ([]status, error)
	FetchProject(ctx context.Context, projectKey string) (*project, error)
	FetchVersions(ctx context.Context, projectKey string) ([]projectVersion, error)
	FetchLabelSuggestions(ctx context.Context, customFieldID int, prefix string) ([]string, error)
	FetchBoardsForProject(ctx context.Context, projectKey string) ([]agileBoard, error)
	SearchUsers(ctx context.Context, query string) ([]user, error)
	FetchCreateMetaIssueTypes(ctx context.Context, projectKey string) ([]createMetaIssueType, error)
	FetchCreateMetaFields(ctx context.Context, projectKey string, issueTypeID string) ([]createMetaField, error)
	DownloadTo(ctx context.Context, url string, dst io.Writer) error
}

// Compile-time check that *Client implements API.
var _ API = (*Client)(nil)

// Client handles all communication with the Jira REST API.
type Client struct {
	Server     string
	token      string
	httpClient *http.Client
	maxRetries int
}

// New creates a Jira REST API client for the given server and auth token.
func New(server, token string) *Client {
	return NewWithHTTPClient(server, token, &http.Client{Timeout: 30 * time.Second})
}

// NewWithHTTPClient creates a Jira REST API client using the supplied
// *http.Client. Useful for tests that inject an httptest.Server transport
// and for offline fixture replay.
func NewWithHTTPClient(server, token string, httpClient *http.Client) *Client {
	return &Client{
		Server:     server,
		token:      token,
		maxRetries: 3,
		httpClient: httpClient,
	}
}

// apiError represents a non-2xx response from Jira.
type apiError struct {
	StatusCode int
	Body       string
	Method     string
	Path       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("jira %s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

func (e *apiError) IsRetryable() bool {
	return e.StatusCode == 429 || e.StatusCode == 503
}

func (c *Client) SearchIssues(ctx context.Context, req searchRequest) (*searchResponse, error) {
	var resp searchResponse
	if err := c.post(ctx, "/rest/api/3/search/jql", req, &resp); err != nil {
		return nil, err
	}
	if resp.NextPageToken == "" {
		resp.IsLast = true
	}
	return &resp, nil
}

func (c *Client) FetchTransitions(ctx context.Context, issueKey string) ([]transition, error) {
	var resp transitionsResponse
	if err := c.get(ctx, fmt.Sprintf("/rest/api/3/issue/%s/transitions", issueKey), &resp); err != nil {
		return nil, err
	}
	return resp.Transitions, nil
}

func (c *Client) DoTransition(ctx context.Context, issueKey, transitionID string) error {
	payload := map[string]any{"transition": map[string]any{"id": transitionID}}
	return c.postNoResponse(ctx, fmt.Sprintf("/rest/api/3/issue/%s/transitions", issueKey), payload)
}

func (c *Client) FetchMyself(ctx context.Context) (*user, error) {
	var u user
	if err := c.get(ctx, "/rest/api/3/myself", &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (c *Client) AssignIssue(ctx context.Context, issueKey, accountID string) error {
	// Jira API requires {"accountId": null} to unassign, not {"accountId": ""}.
	var payload map[string]any
	if accountID == "" {
		payload = map[string]any{"accountId": nil}
	} else {
		payload = map[string]any{"accountId": accountID}
	}
	return c.put(ctx, fmt.Sprintf("/rest/api/3/issue/%s/assignee", issueKey), payload)
}

func (c *Client) CreateIssue(ctx context.Context, payload map[string]any) (*createdIssue, error) {
	var resp createdIssue
	if err := c.post(ctx, "/rest/api/3/issue", payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateIssue(ctx context.Context, issueKey string, payload map[string]any) error {
	return c.put(ctx, fmt.Sprintf("/rest/api/3/issue/%s", issueKey), payload)
}

func (c *Client) AddComment(ctx context.Context, issueKey string, adfBody map[string]any) error {
	return c.postNoResponse(ctx, fmt.Sprintf("/rest/api/3/issue/%s/comment", issueKey),
		map[string]any{"body": adfBody})
}

func (c *Client) FetchIssue(ctx context.Context, issueKey string) (*issue, error) {
	var iss issue
	if err := c.get(ctx, fmt.Sprintf("/rest/api/3/issue/%s", issueKey), &iss); err != nil {
		return nil, err
	}
	return &iss, nil
}

func (c *Client) FetchActiveSprint(ctx context.Context, boardID int) (*sprint, error) {
	var resp sprintList
	if err := c.get(ctx, fmt.Sprintf("/rest/agile/1.0/board/%d/sprint?state=active", boardID), &resp); err != nil {
		return nil, err
	}
	if len(resp.Values) == 0 {
		return nil, nil
	}
	return &resp.Values[0], nil
}

// FetchNextFutureSprint returns the earliest future sprint (lowest ID) for
// a board. When multiple future sprints exist, the lowest-ID sprint is the
// one created first — typically the next sprint to be started.
func (c *Client) FetchNextFutureSprint(ctx context.Context, boardID int) (*sprint, error) {
	var resp sprintList
	if err := c.get(ctx, fmt.Sprintf("/rest/agile/1.0/board/%d/sprint?state=future", boardID), &resp); err != nil {
		return nil, err
	}
	if len(resp.Values) == 0 {
		return nil, nil
	}
	// Pick the lowest-ID sprint — earliest created, next to start.
	best := resp.Values[0]
	for _, s := range resp.Values[1:] {
		if s.ID < best.ID {
			best = s
		}
	}
	return &best, nil
}

// FetchSprints returns sprints for a board filtered by state. states may be
// any combination of "active", "future", "closed"; an empty slice returns
// all sprints. Results are paginated by Jira; this collapses pages.
func (c *Client) FetchSprints(ctx context.Context, boardID int, states []string) ([]sprint, error) {
	path := fmt.Sprintf("/rest/agile/1.0/board/%d/sprint", boardID)
	if len(states) > 0 {
		path += "?state=" + strings.Join(states, ",")
	}
	var resp sprintList
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return resp.Values, nil
}

func (c *Client) AddToSprint(ctx context.Context, sprintID int, issueKeys []string) error {
	return c.postNoResponse(ctx, fmt.Sprintf("/rest/agile/1.0/sprint/%d/issue", sprintID),
		map[string]any{"issues": issueKeys})
}

// MoveToBacklog removes issues from any sprint, placing them in the backlog.
func (c *Client) MoveToBacklog(ctx context.Context, issueKeys []string) error {
	return c.postNoResponse(ctx, "/rest/agile/1.0/backlog",
		map[string]any{"issues": issueKeys})
}

func (c *Client) FetchBoardConfig(ctx context.Context, boardID int) (*boardConfiguration, error) {
	var cfg boardConfiguration
	if err := c.get(ctx, fmt.Sprintf("/rest/agile/1.0/board/%d/configuration", boardID), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Client) FetchFilter(ctx context.Context, filterID string) (*jiraFilter, error) {
	var f jiraFilter
	if err := c.get(ctx, fmt.Sprintf("/rest/api/3/filter/%s", filterID), &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func (c *Client) FetchFields(ctx context.Context) ([]fieldDefinition, error) {
	var fields []fieldDefinition
	if err := c.get(ctx, "/rest/api/3/field", &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

func (c *Client) FetchStatuses(ctx context.Context) ([]status, error) {
	var statuses []status
	if err := c.get(ctx, "/rest/api/3/status", &statuses); err != nil {
		return nil, err
	}
	return statuses, nil
}

func (c *Client) FetchProject(ctx context.Context, projectKey string) (*project, error) {
	var p project
	if err := c.get(ctx, fmt.Sprintf("/rest/api/3/project/%s", projectKey), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *Client) FetchVersions(ctx context.Context, projectKey string) ([]projectVersion, error) {
	var versions []projectVersion
	if err := c.get(ctx, fmt.Sprintf("/rest/api/3/project/%s/versions", projectKey), &versions); err != nil {
		return nil, err
	}
	return versions, nil
}

// FetchLabelSuggestions returns the autocomplete suggestions for a labels
// custom field that begin with prefix. Backed by the JQL autocomplete
// endpoint with the cf[N] field selector. The endpoint caps results at
// ~15 entries per call, so callers wanting the full label set should
// fan out across multiple seed prefixes (see FetchLabelSuggestionsAll).
func (c *Client) FetchLabelSuggestions(ctx context.Context, customFieldID int, prefix string) ([]string, error) {
	var resp struct {
		Results []struct {
			Value       string `json:"value"`
			DisplayName string `json:"displayName"`
		} `json:"results"`
	}
	q := url.Values{}
	q.Set("fieldName", fmt.Sprintf("cf[%d]", customFieldID))
	q.Set("fieldValue", prefix)
	path := "/rest/api/3/jql/autocompletedata/suggestions?" + q.Encode()
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		v := r.Value
		if v == "" {
			v = r.DisplayName
		}
		if v != "" {
			// Strip surrounding quotes the autocomplete API wraps numeric
			// or otherwise-special values with — e.g. "\"0\"" → 0.
			out = append(out, strings.Trim(v, `"`))
		}
	}
	return out, nil
}

func (c *Client) FetchBoardsForProject(ctx context.Context, projectKey string) ([]agileBoard, error) {
	var resp agileBoardList
	if err := c.get(ctx, fmt.Sprintf("/rest/agile/1.0/board?projectKeyOrId=%s", projectKey), &resp); err != nil {
		return nil, err
	}
	return resp.Values, nil
}

// SearchUsers finds Jira users matching the given query (typically an email).
func (c *Client) SearchUsers(ctx context.Context, query string) ([]user, error) {
	var users []user
	if err := c.get(ctx, fmt.Sprintf("/rest/api/3/user/search?query=%s&maxResults=10", query), &users); err != nil {
		return nil, err
	}
	return users, nil
}

// FetchCreateMetaIssueTypes returns the issue types available for creation
// in the given project. Uses the non-deprecated per-project endpoint.
func (c *Client) FetchCreateMetaIssueTypes(ctx context.Context, projectKey string) ([]createMetaIssueType, error) {
	var resp createMetaIssueTypeList
	path := fmt.Sprintf("/rest/api/3/issue/createmeta/%s/issuetypes", projectKey)
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return resp.IssueTypes, nil
}

// FetchCreateMetaFields returns the fields available when creating an issue
// of the given type in the given project. Uses the non-deprecated per-type endpoint.
// Handles pagination automatically — returns the complete field list.
func (c *Client) FetchCreateMetaFields(ctx context.Context, projectKey string, issueTypeID string) ([]createMetaField, error) {
	basePath := fmt.Sprintf("/rest/api/3/issue/createmeta/%s/issuetypes/%s", projectKey, issueTypeID)

	var all []createMetaField
	startAt := 0

	for {
		var resp createMetaFieldList
		path := fmt.Sprintf("%s?startAt=%d", basePath, startAt)
		if err := c.get(ctx, path, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Fields...)

		if len(all) >= resp.Total || len(resp.Fields) == 0 {
			break
		}
		startAt = len(all)
	}

	return all, nil
}

// DownloadTo streams an authenticated GET to the given writer. The url
// can be absolute (Jira returns absolute attachment URLs) or relative
// to the configured server.
func (c *Client) DownloadTo(ctx context.Context, url string, dst io.Writer) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = c.Server + url
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Basic "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &apiError{StatusCode: resp.StatusCode, Body: string(body), Method: "GET", Path: url}
	}
	if _, err := io.Copy(dst, resp.Body); err != nil {
		return fmt.Errorf("streaming %s: %w", url, err)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, dest any) error {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return c.doWithRetry(req, dest)
}

func (c *Client) post(ctx context.Context, path string, payload, dest any) error {
	req, err := c.newRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return err
	}
	return c.doWithRetry(req, dest)
}

func (c *Client) postNoResponse(ctx context.Context, path string, payload any) error {
	req, err := c.newRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return err
	}
	return c.doWithRetry(req, nil)
}

func (c *Client) put(ctx context.Context, path string, payload any) error {
	req, err := c.newRequest(ctx, http.MethodPut, path, payload)
	if err != nil {
		return err
	}
	return c.doWithRetry(req, nil)
}

func (c *Client) newRequest(ctx context.Context, method, path string, payload any) (*http.Request, error) {
	var body io.Reader
	var bodyBytes []byte
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshaling request: %w", err)
		}
		bodyBytes = data
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.Server+path, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Basic "+c.token)
	if payload != nil || method == http.MethodPost || method == http.MethodPut {
		req.Header.Set("Content-Type", "application/json")
	}
	if l := dbg(); l != nil && bodyBytes != nil {
		l.Printf("→ %s %s body=%s", method, path, truncate(string(bodyBytes), 4000))
	} else if l != nil {
		l.Printf("→ %s %s", method, path)
	}
	return req, nil
}

func (c *Client) doWithRetry(req *http.Request, dest any) error {
	var lastErr error

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			time.Sleep(backoff)

			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return fmt.Errorf("recreating request body: %w", err)
				}
				req.Body = body
			}
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if l := dbg(); l != nil {
				l.Printf("✗ %s %s transport error (attempt %d/%d): %v",
					req.Method, req.URL.Path, attempt+1, c.maxRetries+1, err)
			}
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			if l := dbg(); l != nil {
				l.Printf("✗ %s %s read error (attempt %d/%d): %v",
					req.Method, req.URL.Path, attempt+1, c.maxRetries+1, err)
			}
			lastErr = fmt.Errorf("reading response: %w", err)
			continue
		}

		if resp.StatusCode >= 400 {
			// Sanitize non-JSON responses (HTML from proxies/WAFs).
			body := string(bodyBytes)
			ct := resp.Header.Get("Content-Type")
			if !strings.Contains(ct, "application/json") && len(body) > 200 {
				body = body[:200] + "... (truncated non-JSON response)"
			}
			if l := dbg(); l != nil {
				l.Printf("← %d %s %s body=%s", resp.StatusCode, req.Method, req.URL.Path, truncate(body, 4000))
			}
			apiErr := &apiError{
				StatusCode: resp.StatusCode,
				Body:       body,
				Method:     req.Method,
				Path:       req.URL.Path,
			}
			if apiErr.IsRetryable() {
				lastErr = apiErr
				continue
			}
			return apiErr
		}

		if l := dbg(); l != nil {
			// Log body for endpoints we're actively debugging.
			if strings.Contains(req.URL.Path, "/jql/autocompletedata/suggestions") {
				l.Printf("← %d %s %s body=%s", resp.StatusCode, req.Method, req.URL.Path, truncate(string(bodyBytes), 4000))
			} else {
				l.Printf("← %d %s %s", resp.StatusCode, req.Method, req.URL.Path)
			}
		}

		if len(bodyBytes) == 0 || dest == nil {
			return nil
		}

		if err := json.Unmarshal(bodyBytes, dest); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
		return nil
	}

	return fmt.Errorf("max retries exceeded: %w", lastErr)
}
