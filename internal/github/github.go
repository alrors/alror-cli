// Package github integrates Alror with GitHub: it reads the GitHub Actions
// environment, posts check runs and a sticky pull-request comment, and writes
// job summaries and step outputs.
//
// Nothing here runs unless Alror is executing inside GitHub Actions (or the
// caller supplies an explicit token and repository), so the CLI stays offline
// everywhere else.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env is the subset of the GitHub Actions environment Alror uses.
type Env struct {
	Token       string // GITHUB_TOKEN (needs checks: write, pull-requests: write)
	APIURL      string // GITHUB_API_URL, e.g. https://api.github.com or GHES
	Owner, Repo string // from GITHUB_REPOSITORY
	SHA         string // head commit to attach the check to
	PR          int    // pull request number, 0 when not a PR event
	BaseRef     string // PR base branch, e.g. "main"
	RunURL      string // link back to this workflow run
	SummaryPath string // GITHUB_STEP_SUMMARY
	OutputPath  string // GITHUB_OUTPUT
}

// InActions reports whether the process runs inside GitHub Actions.
func InActions() bool { return os.Getenv("GITHUB_ACTIONS") == "true" }

// FromEnv reads the Actions environment. For pull_request events the head SHA
// and PR number come from the event payload, because GITHUB_SHA is the merge commit.
func FromEnv() (Env, error) {
	e := Env{
		Token:       firstNonEmpty(os.Getenv("ALROR_GITHUB_TOKEN"), os.Getenv("GITHUB_TOKEN")),
		APIURL:      strings.TrimRight(firstNonEmpty(os.Getenv("GITHUB_API_URL"), "https://api.github.com"), "/"),
		SHA:         os.Getenv("GITHUB_SHA"),
		BaseRef:     os.Getenv("GITHUB_BASE_REF"),
		SummaryPath: os.Getenv("GITHUB_STEP_SUMMARY"),
		OutputPath:  os.Getenv("GITHUB_OUTPUT"),
	}
	if repo := os.Getenv("GITHUB_REPOSITORY"); repo != "" {
		owner, name, ok := strings.Cut(repo, "/")
		if !ok {
			return e, fmt.Errorf("GITHUB_REPOSITORY %q is not owner/name", repo)
		}
		e.Owner, e.Repo = owner, name
	}
	if server, run := os.Getenv("GITHUB_SERVER_URL"), os.Getenv("GITHUB_RUN_ID"); server != "" && run != "" && e.Owner != "" {
		e.RunURL = fmt.Sprintf("%s/%s/%s/actions/runs/%s", server, e.Owner, e.Repo, run)
	}
	if path := os.Getenv("GITHUB_EVENT_PATH"); path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			var ev struct {
				Number      int `json:"number"`
				PullRequest *struct {
					Number int `json:"number"`
					Head   struct {
						SHA string `json:"sha"`
					} `json:"head"`
					Base struct {
						Ref string `json:"ref"`
					} `json:"base"`
				} `json:"pull_request"`
			}
			if json.Unmarshal(raw, &ev) == nil && ev.PullRequest != nil {
				e.PR = ev.PullRequest.Number
				if ev.PullRequest.Head.SHA != "" {
					e.SHA = ev.PullRequest.Head.SHA
				}
				if e.BaseRef == "" {
					e.BaseRef = ev.PullRequest.Base.Ref
				}
			}
		}
	}
	return e, nil
}

// CanPost reports whether there is enough context to call the GitHub API.
func (e Env) CanPost() bool { return e.Token != "" && e.Owner != "" && e.Repo != "" && e.SHA != "" }

// Client is a minimal GitHub REST client.
type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

// NewClient returns a client for an environment.
func NewClient(e Env) *Client {
	return &Client{Base: e.APIURL, Token: e.Token, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// CheckRun is a completed check shown on the commit and pull request.
type CheckRun struct {
	Name       string `json:"name"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status"`     // "completed"
	Conclusion string `json:"conclusion"` // success | neutral | failure | action_required
	DetailsURL string `json:"details_url,omitempty"`
	Output     struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	} `json:"output"`
}

// CreateCheckRun posts a completed check run and returns its HTML URL.
func (c *Client) CreateCheckRun(ctx context.Context, owner, repo string, run CheckRun) (string, error) {
	var res struct {
		HTMLURL string `json:"html_url"`
	}
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/check-runs", owner, repo), run, &res)
	return res.HTMLURL, err
}

// UpsertComment keeps exactly one Alror comment on a pull request: it edits
// the comment that contains marker, or creates it. Returns the comment URL.
func (c *Client) UpsertComment(ctx context.Context, owner, repo string, pr int, marker, body string) (string, error) {
	body = marker + "\n" + body
	type comment struct {
		ID      int64  `json:"id"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
	}
	for page := 1; page <= 10; page++ {
		var list []comment
		path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=100&page=%d", owner, repo, pr, page)
		if err := c.do(ctx, http.MethodGet, path, nil, &list); err != nil {
			return "", err
		}
		for _, cm := range list {
			if strings.Contains(cm.Body, marker) {
				var out comment
				err := c.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/%s/issues/comments/%d", owner, repo, cm.ID),
					map[string]string{"body": body}, &out)
				return out.HTMLURL, err
			}
		}
		if len(list) < 100 {
			break
		}
	}
	var out comment
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, pr),
		map[string]string{"body": body}, &out)
	return out.HTMLURL, err
}

// APIError is a non-2xx response from GitHub.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("github: HTTP %d: %s", e.Status, e.Message) }

// IsForbidden reports a permissions problem (e.g. missing checks: write).
func IsForbidden(err error) bool {
	var api *APIError
	return errors.As(err, &api) && (api.Status == http.StatusForbidden || api.Status == http.StatusNotFound)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "alror-cli")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(res.Body).Decode(&e)
		return &APIError{Status: res.StatusCode, Message: e.Message}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// AppendSummary adds Markdown to the job summary (no-op outside Actions).
func (e Env) AppendSummary(md string) error {
	if e.SummaryPath == "" {
		return nil
	}
	return appendFile(e.SummaryPath, md+"\n")
}

// SetOutputs writes step outputs (no-op outside Actions).
func (e Env) SetOutputs(kv map[string]string) error {
	if e.OutputPath == "" || len(kv) == 0 {
		return nil
	}
	var b strings.Builder
	for k, v := range kv {
		if strings.ContainsAny(v, "\r\n") {
			delim := "ALROR_EOF_" + strconv.FormatInt(time.Now().UnixNano(), 36)
			fmt.Fprintf(&b, "%s<<%s\n%s\n%s\n", k, delim, v, delim)
		} else {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
		}
	}
	return appendFile(e.OutputPath, b.String())
}

func appendFile(path, s string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(s)
	return err
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
