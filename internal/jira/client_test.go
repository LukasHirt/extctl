package jira

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServer creates a test server with the given path → handler map.
func newTestServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, h := range handlers {
		mux.HandleFunc(path, h)
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestClient_CreateIssue(t *testing.T) {
	t.Run("success 201", func(t *testing.T) {
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/2/issue": func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]string{
					"id":  "10001",
					"key": "OSPO-1",
					"self": ts_url(r) + "/rest/api/2/issue/10001",
				})
			},
		})
		c := NewClient(ts.URL, "test@test.com", "token")
		got, err := c.CreateIssue(CreateIssueRequest{
			Project: "OSPO", Summary: "Test", Description: "body", Labels: []string{"ext-candidate"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Key != "OSPO-1" {
			t.Errorf("Key = %q, want OSPO-1", got.Key)
		}
		if !strings.HasSuffix(got.URL, "/browse/OSPO-1") {
			t.Errorf("URL = %q, want suffix /browse/OSPO-1", got.URL)
		}
	})

	t.Run("default issue type is Task", func(t *testing.T) {
		var bodyBytes []byte
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/2/issue": func(w http.ResponseWriter, r *http.Request) {
				bodyBytes, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]string{"id": "1", "key": "P-1", "self": ""})
			},
		})
		c := NewClient(ts.URL, "test@test.com", "token")
		_, err := c.CreateIssue(CreateIssueRequest{Project: "P", Summary: "s"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var body map[string]any
		json.Unmarshal(bodyBytes, &body)
		fields, _ := body["fields"].(map[string]any)
		issueType, _ := fields["issuetype"].(map[string]any)
		if issueType["name"] != "Task" {
			t.Errorf("issuetype.name = %v, want Task", issueType["name"])
		}
	})

	t.Run("auth header sent", func(t *testing.T) {
		var authHeader string
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/2/issue": func(w http.ResponseWriter, r *http.Request) {
				authHeader = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]string{"id": "1", "key": "P-1", "self": ""})
			},
		})
		c := NewClient(ts.URL, "user@test.com", "mytoken")
		c.CreateIssue(CreateIssueRequest{Project: "P", Summary: "s"}) //nolint:errcheck
		if !strings.HasPrefix(authHeader, "Basic ") {
			t.Errorf("Authorization header = %q, want Basic ...", authHeader)
		}
	})

	t.Run("content-type header sent", func(t *testing.T) {
		var ct string
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/2/issue": func(w http.ResponseWriter, r *http.Request) {
				ct = r.Header.Get("Content-Type")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]string{"id": "1", "key": "P-1", "self": ""})
			},
		})
		c := NewClient(ts.URL, "u", "t")
		c.CreateIssue(CreateIssueRequest{Project: "P", Summary: "s"}) //nolint:errcheck
		if !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
	})

	t.Run("server 401 returns error", func(t *testing.T) {
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/2/issue": func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			},
		})
		c := NewClient(ts.URL, "u", "t")
		_, err := c.CreateIssue(CreateIssueRequest{Project: "P", Summary: "s"})
		if err == nil {
			t.Error("expected error for 401, got nil")
		}
		if !strings.Contains(err.Error(), "401") {
			t.Errorf("error %q should mention status 401", err.Error())
		}
	})

	t.Run("server 500 returns error", func(t *testing.T) {
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/2/issue": func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "internal error", http.StatusInternalServerError)
			},
		})
		c := NewClient(ts.URL, "u", "t")
		_, err := c.CreateIssue(CreateIssueRequest{Project: "P", Summary: "s"})
		if err == nil {
			t.Error("expected error for 500, got nil")
		}
	})
}

func ts_url(r *http.Request) string {
	return "http://" + r.Host
}

func TestClient_Transition(t *testing.T) {
	t.Run("happy path transitions to Doing", func(t *testing.T) {
		var transitionBody []byte
		callCount := 0
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/2/issue/OSPO-1/transitions": func(w http.ResponseWriter, r *http.Request) {
				callCount++
				if r.Method == http.MethodGet {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{
						"transitions": []map[string]string{
							{"id": "10", "name": "Doing"},
							{"id": "11", "name": "Done"},
						},
					})
					return
				}
				// POST
				transitionBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusNoContent)
			},
		})
		c := NewClient(ts.URL, "u", "t")
		if err := c.Transition("OSPO-1", "Doing"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 2 {
			t.Errorf("expected 2 calls (GET + POST), got %d", callCount)
		}
		var body map[string]any
		json.Unmarshal(transitionBody, &body)
		transition, _ := body["transition"].(map[string]any)
		if transition["id"] != "10" {
			t.Errorf("transition.id = %v, want 10", transition["id"])
		}
	})

	t.Run("status not found returns error", func(t *testing.T) {
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/2/issue/OSPO-1/transitions": func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{
						"transitions": []map[string]string{{"id": "10", "name": "Other"}},
					})
				}
			},
		})
		c := NewClient(ts.URL, "u", "t")
		err := c.Transition("OSPO-1", "Missing")
		if err == nil {
			t.Error("expected error for missing status, got nil")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("error %q should mention 'not found'", err.Error())
		}
	})

	t.Run("GET failure returns error", func(t *testing.T) {
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/2/issue/OSPO-1/transitions": func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "server error", http.StatusInternalServerError)
			},
		})
		c := NewClient(ts.URL, "u", "t")
		if err := c.Transition("OSPO-1", "Doing"); err == nil {
			t.Error("expected error for GET failure, got nil")
		}
	})
}

func TestClient_SearchIssues(t *testing.T) {
	t.Run("results returned", func(t *testing.T) {
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/3/search/jql": func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"issues": []map[string]any{
						{
							"key": "OSPO-1",
							"fields": map[string]any{
								"status": map[string]string{"name": "Doing"},
							},
						},
						{
							"key": "OSPO-2",
							"fields": map[string]any{
								"status": map[string]string{"name": "Done"},
							},
						},
					},
				})
			},
		})
		c := NewClient(ts.URL, "u", "t")
		got, err := c.SearchIssues("project=OSPO", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d issues, want 2", len(got))
		}
		if got[0].Key != "OSPO-1" {
			t.Errorf("got[0].Key = %q, want OSPO-1", got[0].Key)
		}
		if got[1].Status != "Done" {
			t.Errorf("got[1].Status = %q, want Done", got[1].Status)
		}
	})

	t.Run("empty results returns empty slice not error", func(t *testing.T) {
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/3/search/jql": func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"issues": []any{}})
			},
		})
		c := NewClient(ts.URL, "u", "t")
		got, err := c.SearchIssues("nothing", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d issues, want 0", len(got))
		}
	})

	t.Run("default fields in request body", func(t *testing.T) {
		var bodyBytes []byte
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/3/search/jql": func(w http.ResponseWriter, r *http.Request) {
				bodyBytes, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"issues": []any{}})
			},
		})
		c := NewClient(ts.URL, "u", "t")
		c.SearchIssues("jql", nil) //nolint:errcheck
		var body map[string]any
		json.Unmarshal(bodyBytes, &body)
		fields, _ := body["fields"].([]any)
		if len(fields) == 0 || fields[0] != "status" {
			t.Errorf("fields in body = %v, want [status]", fields)
		}
	})

	t.Run("server error returns error", func(t *testing.T) {
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/3/search/jql": func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "server error", http.StatusInternalServerError)
			},
		})
		c := NewClient(ts.URL, "u", "t")
		_, err := c.SearchIssues("jql", nil)
		if err == nil {
			t.Error("expected error for 500, got nil")
		}
	})
}

func TestClient_AddComment(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var bodyBytes []byte
		var authHeader string
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/2/issue/OSPO-1/comment": func(w http.ResponseWriter, r *http.Request) {
				bodyBytes, _ = io.ReadAll(r.Body)
				authHeader = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusCreated)
			},
		})
		c := NewClient(ts.URL, "u", "t")
		if err := c.AddComment("OSPO-1", "comment text"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var body map[string]string
		json.Unmarshal(bodyBytes, &body)
		if body["body"] != "comment text" {
			t.Errorf("request body.body = %q, want %q", body["body"], "comment text")
		}
		if !strings.HasPrefix(authHeader, "Basic ") {
			t.Errorf("Authorization = %q, want Basic ...", authHeader)
		}
	})

	t.Run("server 403 returns error", func(t *testing.T) {
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/2/issue/OSPO-1/comment": func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "forbidden", http.StatusForbidden)
			},
		})
		c := NewClient(ts.URL, "u", "t")
		if err := c.AddComment("OSPO-1", "x"); err == nil {
			t.Error("expected error for 403, got nil")
		}
	})
}

func TestClient_GetComments(t *testing.T) {
	t.Run("two comments", func(t *testing.T) {
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/2/issue/OSPO-1/comment": func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"comments": []map[string]any{
						{
							"author":  map[string]string{"displayName": "Alice"},
							"body":    "hello",
							"created": "2026-01-01T10:00:00.000Z",
						},
						{
							"author":  map[string]string{"displayName": "Bob"},
							"body":    "world",
							"created": "2026-01-02T10:00:00.000Z",
						},
					},
				})
			},
		})
		c := NewClient(ts.URL, "u", "t")
		got, err := c.GetComments("OSPO-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d comments, want 2", len(got))
		}
		if got[0].Author != "Alice" {
			t.Errorf("got[0].Author = %q, want Alice", got[0].Author)
		}
		if got[1].Body != "world" {
			t.Errorf("got[1].Body = %q, want world", got[1].Body)
		}
	})

	t.Run("empty comments", func(t *testing.T) {
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/2/issue/OSPO-1/comment": func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"comments": []any{}})
			},
		})
		c := NewClient(ts.URL, "u", "t")
		got, err := c.GetComments("OSPO-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d comments, want 0", len(got))
		}
	})

	t.Run("server error returns error", func(t *testing.T) {
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/rest/api/2/issue/OSPO-1/comment": func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "fail", http.StatusInternalServerError)
			},
		})
		c := NewClient(ts.URL, "u", "t")
		_, err := c.GetComments("OSPO-1")
		if err == nil {
			t.Error("expected error for 500, got nil")
		}
	})
}

func TestClient_ErrorTruncation(t *testing.T) {
	// Body longer than 300 chars should be truncated in the error message.
	longBody := strings.Repeat("x", 400)
	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/rest/api/2/issue": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, longBody, http.StatusBadRequest)
		},
	})
	c := NewClient(ts.URL, "u", "t")
	_, err := c.CreateIssue(CreateIssueRequest{Project: "P", Summary: "s"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// The truncated error body should be at most 300 chars of 'x' plus the ellipsis.
	errStr := err.Error()
	// Count consecutive x's in the error string.
	xRun := 0
	maxXRun := 0
	for _, ch := range errStr {
		if ch == 'x' {
			xRun++
			if xRun > maxXRun {
				maxXRun = xRun
			}
		} else {
			xRun = 0
		}
	}
	if maxXRun > 300 {
		t.Errorf("error body not truncated: found %d x's in a row (want ≤300)", maxXRun)
	}
	if maxXRun < 10 {
		t.Errorf("error body appears missing (found only %d x's)", maxXRun)
	}
}
