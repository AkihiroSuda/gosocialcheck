// Package cache manages the cache.

/*
TODO: consider switching to bbolt

~/.cache: the cache home ($XDG_CACHE_HOME)
  gosocialcheck
    _local: rebuilt from upstream sources (CNCF + GitHub APIs)
      github.com
        containerd
          containerd
           fb4c30d4ede3531652d86197bf3fc9515e5276d9
             gosocialcheck-meta.json
             go.mod
             go.sum
    _remote: shallow clone of the preprocessed cache repository
*/

package cache

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"

	"github.com/AkihiroSuda/gosocialcheck/pkg/categories"
	"github.com/AkihiroSuda/gosocialcheck/pkg/netutil"
	"github.com/AkihiroSuda/gosocialcheck/pkg/netutil/github"
	"github.com/AkihiroSuda/gosocialcheck/pkg/progress"
	"github.com/AkihiroSuda/gosocialcheck/pkg/source/cncf"
)

// Mode selects which cache flavor to use.
type Mode string

const (
	// ModeAuto picks remote/local automatically based on which is newer
	// for read operations; for [Cache.Update] it acts as ModeRemote.
	ModeAuto Mode = "auto"
	// ModeRemote uses the cache fetched from [DefaultRemoteURL].
	ModeRemote Mode = "remote"
	// ModeLocal uses the cache rebuilt locally from upstream sources.
	ModeLocal Mode = "local"
)

// ParseMode validates s and returns the corresponding [Mode].
func ParseMode(s string) (Mode, error) {
	m := Mode(s)
	switch m {
	case ModeAuto, ModeRemote, ModeLocal:
		return m, nil
	}
	return "", fmt.Errorf("invalid cache mode %q (must be %q, %q, or %q)",
		s, ModeAuto, ModeRemote, ModeLocal)
}

const (
	localDirName  = "_local"
	remoteDirName = "_remote"

	// DefaultRemoteURL is the default URL for the remote cache repository.
	DefaultRemoteURL = "https://github.com/AkihiroSuda/gosocialcheck-cache.git"
)

type opts struct {
	dir        string
	mode       Mode
	remoteURL  string
	onProgress progress.Handler
	httpClient *http.Client
}

type Opt func(*opts) error

func WithDir(dir string) Opt {
	return func(opts *opts) error {
		opts.dir = dir
		return nil
	}
}

func WithMode(mode Mode) Opt {
	return func(opts *opts) error {
		if mode == "" {
			return nil
		}
		if _, err := ParseMode(string(mode)); err != nil {
			return err
		}
		opts.mode = mode
		return nil
	}
}

func WithRemoteURL(url string) Opt {
	return func(opts *opts) error {
		if url == "" {
			return nil
		}
		opts.remoteURL = url
		return nil
	}
}

func WithProgressEventHandler(onProgress progress.Handler) Opt {
	return func(opts *opts) error {
		opts.onProgress = onProgress
		return nil
	}
}

func WithHTTPClient(httpClient *http.Client) Opt {
	return func(opts *opts) error {
		opts.httpClient = httpClient
		return nil
	}
}

// New instantiates [Cache].
func New(o ...Opt) (*Cache, error) {
	var c Cache
	for _, f := range o {
		if err := f(&c.opts); err != nil {
			return nil, err
		}
	}
	if c.opts.dir == "" {
		cacheHome, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		c.opts.dir = filepath.Join(cacheHome, "gosocialcheck")
	}
	if c.opts.mode == "" {
		c.opts.mode = ModeAuto
	}
	if c.opts.remoteURL == "" {
		c.opts.remoteURL = DefaultRemoteURL
	}
	if c.opts.onProgress == nil {
		c.opts.onProgress = progress.DefaultHandler
	}
	if c.opts.httpClient == nil {
		c.opts.httpClient = http.DefaultClient
	}
	return &c, nil
}

type Cache struct {
	opts
}

func (c *Cache) httpOpts() []netutil.HTTPOpt {
	return []netutil.HTTPOpt{
		netutil.WithHTTPClient(c.httpClient),
		netutil.WithAutoGitHubToken(),
	}
}

// LocalDir is the directory of the locally rebuilt cache.
func (c *Cache) LocalDir() string {
	return filepath.Join(c.dir, localDirName)
}

// RemoteDir is the directory of the cache fetched from the remote.
func (c *Cache) RemoteDir() string {
	return filepath.Join(c.dir, remoteDirName)
}

// ReadMode returns the mode used for read operations.
// It is either [ModeLocal] or [ModeRemote]; [ModeAuto] is resolved
// to whichever of local/remote has the more recent ModTime.
func (c *Cache) ReadMode() Mode {
	if c.opts.mode != ModeAuto {
		return c.opts.mode
	}
	localT, lErr := modTime(c.LocalDir())
	remoteT, rErr := modTime(c.RemoteDir())
	switch {
	case lErr != nil && rErr != nil:
		// Neither exists. Default to local so LastUpdated surfaces
		// the canonical "please run `gosocialcheck update`" error.
		return ModeLocal
	case lErr != nil:
		return ModeRemote
	case rErr != nil:
		return ModeLocal
	case remoteT.After(localT):
		return ModeRemote
	default:
		return ModeLocal
	}
}

// dataDir returns the directory to read cached data from.
func (c *Cache) dataDir() string {
	if c.ReadMode() == ModeRemote {
		return c.RemoteDir()
	}
	return c.LocalDir()
}

func modTime(dir string) (time.Time, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}

// LastUpdated returns the last updated time of the cache selected by [Cache.ReadMode].
// LastUpdated returns [fs.ErrNotExist] on the first run.
func (c *Cache) LastUpdated() (time.Time, error) {
	return modTime(c.dataDir())
}

// EnsureUpdated populates the cache if it has not been updated yet.
// Otherwise it is a no-op.
func (c *Cache) EnsureUpdated(ctx context.Context) error {
	if _, err := c.LastUpdated(); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return c.Update(ctx)
}

// SubStatus describes the state of a single cache flavor.
type SubStatus struct {
	Dir         string    `json:"dir"`
	Exists      bool      `json:"exists"`
	LastUpdated time.Time `json:"last_updated,omitzero"`
}

// RemoteStatus extends [SubStatus] with the configured remote URL.
type RemoteStatus struct {
	SubStatus
	URL string `json:"url"`
}

// Status reports the cache status.
type Status struct {
	// Mode is the configured cache mode (auto/remote/local).
	Mode   Mode         `json:"mode"`
	Local  SubStatus    `json:"local"`
	Remote RemoteStatus `json:"remote"`
}

// Status returns the current cache status.
func (c *Cache) Status() *Status {
	s := &Status{
		Mode: c.opts.mode,
		Local: SubStatus{
			Dir: c.LocalDir(),
		},
		Remote: RemoteStatus{
			SubStatus: SubStatus{
				Dir: c.RemoteDir(),
			},
			URL: c.opts.remoteURL,
		},
	}
	if t, err := modTime(c.LocalDir()); err == nil {
		s.Local.Exists = true
		s.Local.LastUpdated = t
	}
	if t, err := modTime(c.RemoteDir()); err == nil {
		s.Remote.Exists = true
		s.Remote.LastUpdated = t
	}
	return s
}

// Update updates the cache. The target is determined by the configured mode:
// [ModeLocal] rebuilds from upstream sources, [ModeRemote] fetches the latest
// preprocessed cache, and [ModeAuto] is treated as [ModeRemote] (the recommended path).
func (c *Cache) Update(ctx context.Context) error {
	mode := c.opts.mode
	if mode == ModeAuto {
		mode = ModeRemote
	}
	switch mode {
	case ModeLocal:
		return c.updateLocal(ctx)
	case ModeRemote:
		return c.updateRemote(ctx)
	}
	return fmt.Errorf("unsupported cache mode for update: %q", mode)
}

func (c *Cache) updateLocal(ctx context.Context) error {
	dir := c.LocalDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := netutil.Get(ctx, cncf.ProjectsURL, c.httpOpts()...)
	if err != nil {
		return err
	}
	var projects cncf.Projects
	if err = yaml.Unmarshal(b, &projects); err != nil {
		return err
	}
	for _, p := range projects {
		if err = c.updateCNCFProject(ctx, p); err != nil {
			return err
		}
	}
	now := time.Now()
	if err = os.Chtimes(dir, now, now); err != nil {
		return err
	}
	return nil
}

func (c *Cache) updateRemote(ctx context.Context) error {
	dir := c.RemoteDir()
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	gitDir := filepath.Join(dir, ".git")
	_, statErr := os.Stat(gitDir)
	switch {
	case errors.Is(statErr, fs.ErrNotExist):
		c.onProgress(ctx, progress.Event{Message: "cloning " + c.opts.remoteURL})
		args := []string{"clone", "--depth", "1", c.opts.remoteURL, dir}
		if out, err := runGit(ctx, "", args...); err != nil {
			return fmt.Errorf("git clone failed: %w: %s", err, out)
		}
	case statErr != nil:
		return statErr
	default:
		c.onProgress(ctx, progress.Event{Message: "fetching " + c.opts.remoteURL})
		if out, err := runGit(ctx, dir, "fetch", "--depth", "1", "origin"); err != nil {
			return fmt.Errorf("git fetch failed: %w: %s", err, out)
		}
		if out, err := runGit(ctx, dir, "reset", "--hard", "FETCH_HEAD"); err != nil {
			return fmt.Errorf("git reset failed: %w: %s", err, out)
		}
	}
	now := time.Now()
	if err := os.Chtimes(dir, now, now); err != nil {
		return err
	}
	return nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	full := args
	if dir != "" {
		full = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", full...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (c *Cache) updateCNCFProject(ctx context.Context, p cncf.Project) error {
	if p.Maturity != "graduated" {
		return nil
	}
	for _, r := range p.Repositories {
		if err := c.updateCNCFRepo(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cache) updateCNCFRepo(ctx context.Context, r cncf.Repository) error {
	var category string
	// TODO: include repos that belong to the same org as "code".
	//       Most of them should be accidentally ommited out from "code-lite".
	switch {
	case slices.Contains(r.CheckSets, "code"):
		category = categories.CNCFGraduated
	case slices.Contains(r.CheckSets, "code-lite"):
		// TODO: opt-in
		//	category = categories.CNCFGraduatedSub
	}
	if category != "" {
		if err := c.updateGitHubRepo(ctx, r.URL, category); err != nil {
			return err
		}
	}
	return nil
}

func filterPrelease(tags []github.Tag) []github.Tag {
	var res []github.Tag
	for _, tag := range tags {
		if semver.Prerelease(tag.Name) == "" {
			res = append(res, tag)
		}
	}
	return res
}

// dedupTagsBySHA deduplicates tags that point to the same commit SHA.
// When multiple tags share a SHA, the one with the lexicographically
// smallest name is kept, so the result is deterministic regardless of
// the input order returned by the GitHub API.
func dedupTagsBySHA(tags []github.Tag) []github.Tag {
	idx := make(map[string]int, len(tags))
	res := make([]github.Tag, 0, len(tags))
	for _, t := range tags {
		if i, ok := idx[t.Commit.SHA]; ok {
			if t.Name < res[i].Name {
				res[i] = t
			}
			continue
		}
		idx[t.Commit.SHA] = len(res)
		res = append(res, t)
	}
	return res
}

// updateGitHubRepo expects url to be "https://github.com/<ORG>/<REPO>".
func (c *Cache) updateGitHubRepo(ctx context.Context, urlStr, category string) error {
	repo, err := github.NewRepo(urlStr)
	if err != nil {
		return err
	}
	tags, err := repo.Tags(ctx, c.httpOpts()...)
	if err != nil {
		return err
	}
	tags = filterPrelease(tags)
	tags = dedupTagsBySHA(tags)
	const maxTags = 10
	if len(tags) > maxTags {
		tags = tags[:maxTags]
	}
	g, ctx := errgroup.WithContext(ctx)
	for _, tag := range tags {
		g.Go(func() error {
			return c.updateGitHubRepoTag(ctx, repo, tag, category)
		})
	}
	return g.Wait()
}

func (c *Cache) updateGitHubRepoTag(ctx context.Context, repo *github.Repo, tag github.Tag, category string) error {
	dir := filepath.Join(c.LocalDir(), "github.com", repo.Owner, repo.Repo, tag.Commit.SHA)
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, p := range []string{"go.mod", "go.sum"} {
		urlStr := repo.ContentURL(tag.Commit.SHA, p)
		b, err := netutil.Get(ctx, urlStr, c.httpOpts()...)
		if err != nil {
			var err2 *netutil.UnexpectedStatusCodeError
			if errors.As(err, &err2) && err2.StatusCode == 404 {
				// Not Go code
				break
			}
			return err
		}
		f := filepath.Join(dir, p)
		if err = os.WriteFile(f, b, 0o644); err != nil {
			return err
		}
	}
	meta := &Meta{
		Repo:     *repo,
		Tag:      tag.Compact(),
		Category: category,
	}
	metaB, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	metaF := filepath.Join(dir, MetaFilename)
	if err := os.WriteFile(metaF, metaB, 0o644); err != nil {
		return err
	}
	c.onProgress(ctx, progress.Event{
		Message: fmt.Sprintf("%s/%s %s %s (%s)",
			repo.Owner, repo.Repo, tag.Name, tag.Commit.SHA, category),
	})
	return nil
}

const MetaFilename = "gosocialcheck-meta.json"

type Meta struct {
	Repo     github.Repo `json:"repo"`
	Tag      github.Tag  `json:"tag"`
	Category string      `json:"category"`
}

func (c *Cache) Lookup(ctx context.Context, sum string) ([]Meta, error) {
	if !strings.HasPrefix(sum, "h1:") || !strings.HasSuffix(sum, "=") {
		return nil, fmt.Errorf("expected h1 sum, got %q", sum)
	}
	dataDir := c.dataDir()
	goSumFiles, err := c.lookupGoSumFiles(ctx, dataDir, sum)
	if err != nil {
		return nil, err
	}
	var res []Meta
	for _, goSumFile := range goSumFiles {
		dir := filepath.Join(dataDir, filepath.Clean(filepath.Dir(goSumFile)))
		f := filepath.Join(dir, MetaFilename)
		b, err := os.ReadFile(f)
		if err != nil {
			return res, err
		}
		var m Meta
		if err = json.Unmarshal(b, &m); err != nil {
			return res, err
		}
		res = append(res, m)
	}
	return res, nil
}

func (c *Cache) lookupGoSumFiles(ctx context.Context, dataDir, sum string) ([]string, error) {
	var stdout, stderr bytes.Buffer
	// The remote cache directory is a real git working tree, so plain
	// `git grep` confines the search to tracked files (and skips .git/).
	// The local cache directory is not a git repo, so we need --no-index.
	args := []string{"-C", dataDir, "grep", "--name-only"}
	if c.ReadMode() == ModeLocal {
		args = append(args, "--no-index")
	}
	args = append(args, sum)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		if exitCode == 1 && stderr.String() == "" {
			// failed successfully
			return nil, nil
		}
		return nil, fmt.Errorf("failed to run %v (stderr=%q)", cmd.Args, stderr.String())
	}
	sc := bufio.NewScanner(&stdout)
	var res []string
	for sc.Scan() {
		line := sc.Text()
		line = strings.TrimSpace(line)
		res = append(res, line)
	}
	return res, sc.Err()
}
