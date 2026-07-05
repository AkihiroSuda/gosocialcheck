package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/AkihiroSuda/gosocialcheck/pkg/netutil/scorecard"
)

type fakeScorecardFetcher struct {
	calls int
	// scores maps "host/owner/repo" to a score; missing entries behave like
	// repositories not covered by Scorecard.
	scores map[string]float64
	// err, if set, fails every fetch (e.g. to simulate being offline).
	err error
}

func (f *fakeScorecardFetcher) Get(_ context.Context, host, owner, repo string) (*scorecard.Result, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	s, ok := f.scores[host+"/"+owner+"/"+repo]
	if !ok {
		return nil, scorecard.ErrNotFound
	}
	return &scorecard.Result{Score: s}, nil
}

func TestScorecardCache(t *testing.T) {
	ctx := context.Background()

	t.Run("caches results", func(t *testing.T) {
		fetcher := &fakeScorecardFetcher{scores: map[string]float64{"github.com/foo/bar": 6.5}}
		sc := &ScorecardCache{dir: t.TempDir(), ttl: time.Hour, fetcher: fetcher}
		for i := 0; i < 2; i++ {
			res, err := sc.Get(ctx, "github.com", "foo", "bar")
			assert.NilError(t, err)
			assert.Equal(t, 6.5, res.Score)
		}
		assert.Equal(t, 1, fetcher.calls)
	})

	t.Run("caches negative results", func(t *testing.T) {
		fetcher := &fakeScorecardFetcher{}
		sc := &ScorecardCache{dir: t.TempDir(), ttl: time.Hour, fetcher: fetcher}
		for i := 0; i < 2; i++ {
			_, err := sc.Get(ctx, "github.com", "foo", "unscored")
			assert.Assert(t, errors.Is(err, scorecard.ErrNotFound), "expected ErrNotFound, got %v", err)
		}
		assert.Equal(t, 1, fetcher.calls)
	})

	t.Run("refetches expired entries", func(t *testing.T) {
		fetcher := &fakeScorecardFetcher{scores: map[string]float64{"github.com/foo/bar": 6.5}}
		sc := &ScorecardCache{dir: t.TempDir(), ttl: 0, fetcher: fetcher}
		for i := 0; i < 2; i++ {
			_, err := sc.Get(ctx, "github.com", "foo", "bar")
			assert.NilError(t, err)
		}
		assert.Equal(t, 2, fetcher.calls)
	})

	t.Run("falls back to an expired entry when fetching fails", func(t *testing.T) {
		fetcher := &fakeScorecardFetcher{scores: map[string]float64{"github.com/foo/bar": 6.5}}
		sc := &ScorecardCache{dir: t.TempDir(), ttl: 0, fetcher: fetcher}
		_, err := sc.Get(ctx, "github.com", "foo", "bar")
		assert.NilError(t, err)
		fetcher.err = errors.New("offline")
		res, err := sc.Get(ctx, "github.com", "foo", "bar")
		assert.NilError(t, err)
		assert.Equal(t, 6.5, res.Score)
	})

	t.Run("propagates fetch errors without a cached entry", func(t *testing.T) {
		fetcher := &fakeScorecardFetcher{err: errors.New("offline")}
		sc := &ScorecardCache{dir: t.TempDir(), ttl: time.Hour, fetcher: fetcher}
		_, err := sc.Get(ctx, "github.com", "foo", "bar")
		assert.ErrorContains(t, err, "offline")
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		fetcher := &fakeScorecardFetcher{}
		sc := &ScorecardCache{dir: t.TempDir(), ttl: time.Hour, fetcher: fetcher}
		for _, comp := range [][3]string{
			{"github.com", "..", "bar"},
			{"github.com", "foo", "a/b"},
			{"", "foo", "bar"},
		} {
			_, err := sc.Get(ctx, comp[0], comp[1], comp[2])
			assert.ErrorContains(t, err, "invalid repository component")
		}
		assert.Equal(t, 0, fetcher.calls)
	})
}
