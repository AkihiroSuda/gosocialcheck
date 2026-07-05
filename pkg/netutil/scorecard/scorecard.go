// Package scorecard fetches OpenSSF Scorecard results.
// https://scorecard.dev/
package scorecard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/AkihiroSuda/gosocialcheck/pkg/netutil"
)

const (
	// DefaultAPIBaseURL is the default base URL of the Scorecard REST API.
	// https://api.scorecard.dev/#/results/getResult
	DefaultAPIBaseURL = "https://api.scorecard.dev"

	// DefaultMinScore is the default minimum acceptable aggregate score.
	// Scores range from 0 to 10. There is no official threshold; 5.0 is an
	// arbitrary midpoint that can be adjusted with --scorecard-min-score.
	DefaultMinScore = 5.0
)

// ErrNotFound indicates that the Scorecard API has no data for the repository.
// The API only covers repositories scanned by the OpenSSF weekly cron job.
var ErrNotFound = errors.New("no OpenSSF Scorecard data found")

// Result is the Scorecard API result for a repository.
// Fields irrelevant to gosocialcheck (per-check scores, etc.) are omitted.
type Result struct {
	Date string `json:"date,omitempty"`
	Repo struct {
		Name   string `json:"name,omitempty"`
		Commit string `json:"commit,omitempty"`
	} `json:"repo"`
	Score float64 `json:"score"`
}

// Client accesses the Scorecard REST API.
type Client struct {
	// BaseURL defaults to [DefaultAPIBaseURL].
	BaseURL string
	// HTTPOpts are passed to [netutil.Get].
	HTTPOpts []netutil.HTTPOpt
}

// Get fetches the Scorecard result for host/owner/repo (e.g. "github.com",
// "kubernetes", "kubernetes"). It returns an error wrapping [ErrNotFound]
// when the repository is not covered by Scorecard.
func (c *Client) Get(ctx context.Context, host, owner, repo string) (*Result, error) {
	base := c.BaseURL
	if base == "" {
		base = DefaultAPIBaseURL
	}
	urlStr := fmt.Sprintf("%s/projects/%s/%s/%s",
		base, url.PathEscape(host), url.PathEscape(owner), url.PathEscape(repo))
	b, err := netutil.Get(ctx, urlStr, c.HTTPOpts...)
	if err != nil {
		var statusErr *netutil.UnexpectedStatusCodeError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %s/%s/%s", ErrNotFound, host, owner, repo)
		}
		return nil, err
	}
	var res Result
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// RepoForModule maps a Go module path to the repository expected by the
// Scorecard API. Module paths that cannot be mapped without fetching the
// go-import meta tag (vanity import paths) return ok=false.
func RepoForModule(modPath string) (host, owner, repo string, ok bool) {
	parts := strings.Split(modPath, "/")
	switch {
	case len(parts) >= 3 && (parts[0] == "github.com" || parts[0] == "gitlab.com"):
		// Extra path elements such as the "/v2" major version suffix or a
		// subdirectory module do not belong to the repository name.
		return parts[0], parts[1], parts[2], true
	case len(parts) >= 3 && parts[0] == "golang.org" && parts[1] == "x":
		return "github.com", "golang", parts[2], true
	}
	return "", "", "", false
}
