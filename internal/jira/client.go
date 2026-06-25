package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a minimal Jira Cloud REST v2 client.
// Auth uses HTTP Basic with an Atlassian API token:
//   Authorization: Basic base64(email:token)
type Client struct {
	baseURL string
	email   string
	token   string
	http    *http.Client
}

func NewClient(baseURL, email, token string) *Client {
	return &Client{
		baseURL: baseURL,
		email:   email,
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// CreatedIssue is the response from POST /rest/api/2/issue.
type CreatedIssue struct {
	ID  string `json:"id"`
	Key string `json:"key"`
	URL string `json:"self"`
}

// CreateIssueRequest is the payload for creating a candidate issue.
type CreateIssueRequest struct {
	Project     string
	Summary     string
	Description string
	Labels      []string
	IssueType   string // defaults to "Task"
}

func (c *Client) CreateIssue(req CreateIssueRequest) (*CreatedIssue, error) {
	if req.IssueType == "" {
		req.IssueType = "Task"
	}

	body := map[string]any{
		"fields": map[string]any{
			"project":     map[string]string{"key": req.Project},
			"summary":     req.Summary,
			"description": req.Description,
			"issuetype":   map[string]string{"name": req.IssueType},
			"labels":      req.Labels,
		},
	}

	var created CreatedIssue
	if err := c.post("/rest/api/2/issue", body, &created); err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}
	// Build a browsable URL from the self URL's host.
	created.URL = c.baseURL + "/browse/" + created.Key
	return &created, nil
}

// TransitionRequest transitions an issue to a named status.
// It first fetches the available transitions to find the ID by name.
func (c *Client) Transition(issueKey, statusName string) error {
	// GET available transitions.
	var result struct {
		Transitions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"transitions"`
	}
	if err := c.get(fmt.Sprintf("/rest/api/2/issue/%s/transitions", issueKey), &result); err != nil {
		return fmt.Errorf("get transitions for %s: %w", issueKey, err)
	}

	var transitionID string
	for _, t := range result.Transitions {
		if t.Name == statusName {
			transitionID = t.ID
			break
		}
	}
	if transitionID == "" {
		return fmt.Errorf("transition %q not found for issue %s", statusName, issueKey)
	}

	body := map[string]any{
		"transition": map[string]string{"id": transitionID},
	}
	if err := c.post(fmt.Sprintf("/rest/api/2/issue/%s/transitions", issueKey), body, nil); err != nil {
		return fmt.Errorf("transition %s to %q: %w", issueKey, statusName, err)
	}
	return nil
}

// IssueRef is a lightweight Jira issue returned by SearchIssues.
type IssueRef struct {
	Key    string
	Status string
}

// SearchIssues runs a JQL query and returns matching issues with the requested
// fields populated. Returns an empty slice (not an error) when there are no results.
// fields should be a subset of ["status", "summary", "labels"]; if empty, only
// the issue key is returned.
func (c *Client) SearchIssues(jql string, fields []string) ([]IssueRef, error) {
	if len(fields) == 0 {
		fields = []string{"status"}
	}

	body := map[string]any{
		"jql":        jql,
		"fields":     fields,
		"maxResults": 50,
	}

	var result struct {
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Status struct {
					Name string `json:"name"`
				} `json:"status"`
			} `json:"fields"`
		} `json:"issues"`
	}
	if err := c.post("/rest/api/3/search/jql", body, &result); err != nil {
		return nil, fmt.Errorf("search issues (jql=%q): %w", jql, err)
	}

	out := make([]IssueRef, 0, len(result.Issues))
	for _, iss := range result.Issues {
		out = append(out, IssueRef{
			Key:    iss.Key,
			Status: iss.Fields.Status.Name,
		})
	}
	return out, nil
}

// AddComment adds a comment to an issue.
func (c *Client) AddComment(issueKey, body string) error {
	payload := map[string]string{"body": body}
	if err := c.post(fmt.Sprintf("/rest/api/2/issue/%s/comment", issueKey), payload, nil); err != nil {
		return fmt.Errorf("add comment to %s: %w", issueKey, err)
	}
	return nil
}

// Comment is a single comment on a Jira issue.
type Comment struct {
	Author  string
	Body    string
	Created string
}

// GetComments returns all comments on an issue in chronological order.
// Replies appear as sequential comments after the one they respond to.
func (c *Client) GetComments(issueKey string) ([]Comment, error) {
	var result struct {
		Comments []struct {
			Author struct {
				DisplayName string `json:"displayName"`
			} `json:"author"`
			Body    string `json:"body"`
			Created string `json:"created"`
		} `json:"comments"`
	}
	if err := c.get(fmt.Sprintf("/rest/api/2/issue/%s/comment", issueKey), &result); err != nil {
		return nil, fmt.Errorf("get comments for %s: %w", issueKey, err)
	}
	out := make([]Comment, 0, len(result.Comments))
	for _, c := range result.Comments {
		out = append(out, Comment{
			Author:  c.Author.DisplayName,
			Body:    c.Body,
			Created: c.Created,
		})
	}
	return out, nil
}

// --- HTTP helpers ---

func (c *Client) get(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) post(path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	req.SetBasicAuth(c.email, c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http %s %s: %w", req.Method, req.URL, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %s %s: status %d: %s",
			req.Method, req.URL, resp.StatusCode, truncate(string(respBody), 300))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
