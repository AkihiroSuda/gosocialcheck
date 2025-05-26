package netutil

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type httpOpts struct {
	client      *http.Client
	maxBytes    int64
	bearerToken string
}

type HTTPOpt func(opts *httpOpts, urlStr string) error

func WithHTTPClient(client *http.Client) HTTPOpt {
	return func(opts *httpOpts, _ string) error {
		opts.client = client
		return nil
	}
}

const DefaultHTTPMaxBytes = 64 * 1024 * 1024 // 64 MiB

func WithHTTPMaxBytes(maxBytes int64) HTTPOpt {
	return func(opts *httpOpts, _ string) error {
		opts.maxBytes = maxBytes
		return nil
	}
}

func WithBearerToken(bearerToken string) HTTPOpt {
	return func(opts *httpOpts, _ string) error {
		opts.bearerToken = bearerToken
		return nil
	}
}

func isGitHubDomain(urlStr string) (bool, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false, err
	}
	hostname := u.Hostname()
	hostname = strings.TrimSuffix(hostname, ".")
	switch hostname {
	case "github.com", "api.github.com", "raw.githubusercontent.com":
		return true, nil
	}
	return false, nil
}

// WithAutoGitHubToken automatically sends $GITHUB_TOKEN so as to relax the API rate limit.
// https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api
func WithAutoGitHubToken() HTTPOpt {
	return func(opts *httpOpts, urlStr string) error {
		isGH, err := isGitHubDomain(urlStr)
		if err != nil {
			return err
		}
		if isGH {
			token := os.Getenv("GITHUB_TOKEN")
			if token == "" {
				// `gh` prioritizes $GH_TOKEN over $GITHUB_TOKEN
				token = os.Getenv("GH_TOKEN")
			}
			if token != "" {
				opts.bearerToken = token
			}
		}
		return nil
	}
}

type UnexpectedStatusCodeError struct {
	URL        *url.URL
	StatusCode int
	Body       string
}

func (e *UnexpectedStatusCodeError) Error() string {
	return fmt.Sprintf("%s: unexpected status code %d: %s", e.URL.Redacted(), e.StatusCode, e.Body)
}

func Get(ctx context.Context, urlStr string, o ...HTTPOpt) ([]byte, error) {
	var opts httpOpts
	for _, f := range o {
		if err := f(&opts, urlStr); err != nil {
			return nil, err
		}
	}
	if opts.client == nil {
		opts.client = http.DefaultClient
	}
	if opts.maxBytes == 0 {
		opts.maxBytes = DefaultHTTPMaxBytes
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	if opts.bearerToken != "" {
		req.Header.Add("Authorization", "Bearer "+opts.bearerToken)
	}
	resp, err := opts.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	lr := &io.LimitedReader{
		R: resp.Body,
		N: opts.maxBytes,
	}
	body, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, &UnexpectedStatusCodeError{
			URL:        req.URL,
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}
	return body, nil
}
