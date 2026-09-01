package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AkihiroSuda/gosocialcheck/pkg/netutil"
	"github.com/AkihiroSuda/gosocialcheck/pkg/netutil/scorecard"
)

const (
	scorecardDirName = "_scorecard"

	// ScorecardTTL is how long a cached Scorecard result stays fresh.
	// Upstream results are recomputed roughly weekly by the OpenSSF cron job.
	ScorecardTTL = 24 * time.Hour
)

// ScorecardDir is the directory of the cached OpenSSF Scorecard results.
func (c *Cache) ScorecardDir() string {
	return filepath.Join(c.dir, scorecardDirName)
}

// Scorecard returns a [ScorecardCache] that caches results from the Scorecard
// REST API under [Cache.ScorecardDir].
func (c *Cache) Scorecard() *ScorecardCache {
	return &ScorecardCache{
		dir: c.ScorecardDir(),
		ttl: ScorecardTTL,
		fetcher: &scorecard.Client{
			HTTPOpts: []netutil.HTTPOpt{netutil.WithHTTPClient(c.httpClient)},
		},
	}
}

// scorecardFetcher fetches OpenSSF Scorecard results.
// [*scorecard.Client] implements it.
type scorecardFetcher interface {
	Get(ctx context.Context, host, owner, repo string) (*scorecard.Result, error)
}

// ScorecardCache caches OpenSSF Scorecard results on disk, including negative
// ("no data") results, so repeated runs do not refetch every repository.
// It implements the analyzer's ScorecardResolver interface.
type ScorecardCache struct {
	dir     string
	ttl     time.Duration
	fetcher scorecardFetcher
}

// scorecardEntry is the on-disk cache entry: either a result, or a record
// that the repository is not covered by Scorecard.
type scorecardEntry struct {
	NotFound bool              `json:"not_found,omitempty"`
	Result   *scorecard.Result `json:"result,omitempty"`
}

func (e *scorecardEntry) result(host, owner, repo string) (*scorecard.Result, error) {
	if e.NotFound {
		return nil, fmt.Errorf("%w: %s/%s/%s (cached)", scorecard.ErrNotFound, host, owner, repo)
	}
	return e.Result, nil
}

func (sc *ScorecardCache) entryPath(host, owner, repo string) (string, error) {
	for _, s := range []string{host, owner, repo} {
		if s == "" || s == "." || s == ".." || strings.ContainsAny(s, `/\`) {
			return "", fmt.Errorf("invalid repository component %q", s)
		}
	}
	return filepath.Join(sc.dir, host, owner, repo+".json"), nil
}

func readScorecardEntry(p string) (*scorecardEntry, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var e scorecardEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	if !e.NotFound && e.Result == nil {
		return nil, fmt.Errorf("invalid Scorecard cache entry %q", p)
	}
	return &e, nil
}

func writeScorecardEntry(p string, e *scorecardEntry) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

// Get returns the cached Scorecard result for host/owner/repo, fetching and
// storing it when the cached entry is missing or older than [ScorecardTTL].
// Like [scorecard.Client.Get], it returns an error wrapping
// [scorecard.ErrNotFound] when the repository is not covered by Scorecard.
// When fetching fails (e.g. offline), an expired entry is used as a fallback.
func (sc *ScorecardCache) Get(ctx context.Context, host, owner, repo string) (*scorecard.Result, error) {
	p, err := sc.entryPath(host, owner, repo)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(p); err == nil && time.Since(st.ModTime()) < sc.ttl {
		if e, err := readScorecardEntry(p); err == nil {
			return e.result(host, owner, repo)
		} else {
			slog.DebugContext(ctx, "ignoring unreadable Scorecard cache entry", "path", p, "error", err)
		}
	}
	res, fetchErr := sc.fetcher.Get(ctx, host, owner, repo)
	switch {
	case fetchErr == nil:
		if err := writeScorecardEntry(p, &scorecardEntry{Result: res}); err != nil {
			return nil, err
		}
		return res, nil
	case errors.Is(fetchErr, scorecard.ErrNotFound):
		if err := writeScorecardEntry(p, &scorecardEntry{NotFound: true}); err != nil {
			return nil, err
		}
		return nil, fetchErr
	default:
		if e, err := readScorecardEntry(p); err == nil {
			slog.WarnContext(ctx, "failed to fetch a Scorecard result; using an expired cache entry",
				"repo", host+"/"+owner+"/"+repo, "error", fetchErr)
			return e.result(host, owner, repo)
		}
		return nil, fetchErr
	}
}
