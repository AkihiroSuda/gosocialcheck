package analyzer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"go/token"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	gomoddirectivecomments "github.com/AkihiroSuda/gomoddirectivecomments"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/tools/go/analysis"

	"github.com/AkihiroSuda/gosocialcheck/pkg/cache"
	"github.com/AkihiroSuda/gosocialcheck/pkg/progress"
)

// Resolver resolves a module's h1 go.sum hash to the trusted projects that have
// adopted it. [*cache.Cache] implements it.
type Resolver interface {
	Lookup(ctx context.Context, sum string) ([]cache.Meta, error)
}

type Opts struct {
	Flags flag.FlagSet
	Cache Resolver
	// GHA emits diagnostics as GitHub Actions workflow commands on stdout
	// instead of via [analysis.Pass.Report], so the process exits 0 even
	// when there are findings. See:
	// https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-commands
	GHA bool
	// OnProgress, if set, receives progress events (e.g. while fetching the base
	// branch in --gha mode).
	OnProgress progress.Handler
}

const (
	directivePolicyUntrusted = "untrusted"
	directivePolicyTrusted   = "trusted"
)

// Analyzer wraps an [analysis.Analyzer]. The analysis pass only sees imported
// (direct) dependencies, so indirect dependencies are checked by [Analyzer.Flush]
// once analysis has completed. In --gha mode direct-dependency diagnostics are
// also buffered during analysis and emitted (prioritized and capped) by Flush.
type Analyzer struct {
	*analysis.Analyzer
	inst *instance
}

// Flush checks the indirect dependencies recorded during analysis and emits any
// findings. In --gha mode it also emits the buffered direct-dependency findings.
// It must be called after analysis has completed and returns the number of
// findings that should count toward the diagnostic exit code (always 0 in --gha
// mode, which exits 0 by design).
func (a *Analyzer) Flush(ctx context.Context) (int, error) {
	return a.inst.flush(ctx)
}

func New(ctx context.Context, opts Opts) (*Analyzer, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	inst := &instance{
		Opts:                opts,
		processedSums:       make(map[string]struct{}),
		cwd:                 cwd,
		changedSumLines:     make(map[string]map[int]struct{}),
		changedSumLinesDone: make(map[string]bool),
		mods:                make(map[string]*modInfo),
	}
	a := &analysis.Analyzer{
		Name:             "gosocialcheck",
		Doc:              "Social reputation checker",
		URL:              "https://github.com/AkihiroSuda/gosocialcheck",
		Flags:            opts.Flags,
		Run:              run(ctx, inst),
		RunDespiteErrors: false,
	}
	return &Analyzer{Analyzer: a, inst: inst}, nil
}

type instance struct {
	Opts
	processedSums   map[string]struct{}
	processedSumsMu sync.RWMutex
	cwd             string

	// changedSumLines caches, per go.sum file, the set of 1-based line numbers
	// added or modified in the current pull request. changedSumLinesDone records
	// whether the computation has run (a nil set with done=true means the change
	// set could not be determined, so no finding is treated as PR-changed).
	changedSumLinesMu   sync.Mutex
	changedSumLines     map[string]map[int]struct{}
	changedSumLinesDone map[string]bool

	// ghaFindings buffers findings in --gha mode so they can be prioritized and
	// capped before being emitted by flushGHA.
	ghaFindingsMu sync.Mutex
	ghaFindings   []ghaFinding

	// mods records the parsed go.mod of each analyzed module, keyed by its go.mod
	// filename. Flush iterates these require lists to check indirect dependencies,
	// which never appear as imports in the analyzed source.
	modsMu sync.Mutex
	mods   map[string]*modInfo
}

// modInfo holds the parsed state of a single module needed to check its
// (indirect) dependencies during Flush.
type modInfo struct {
	goModFilename string
	goSumFilename string
	goMod         *modfile.File
	goSum         map[string]goSumEntry
	policies      map[string]string
}

type ghaFinding struct {
	msg            string
	sumPosn        token.Position // go.sum line to annotate (--gha mode)
	modPosn        token.Position // go.mod line to report (non-gha indirect findings)
	changedInPR    bool           // go.sum line was added/changed in the pull request
	changeSetKnown bool           // the pull request change set for the go.sum file was determinable
}

func run(ctx context.Context, inst *instance) func(*analysis.Pass) (any, error) {
	return func(pass *analysis.Pass) (any, error) {
		// TODO: cache go.mod
		// TODO: support multi-module mono repo
		goModFilename := pass.Module.GoMod
		if goModFilename == "" {
			return nil, nil
		}
		// pass.ReadFile does not support go.mod
		goModB, err := os.ReadFile(goModFilename)
		if err != nil {
			return nil, fmt.Errorf("failed to read %q: %w", goModFilename, err)
		}
		goMod, err := modfile.Parse(goModFilename, goModB, nil)
		if err != nil {
			return nil, err
		}
		if goMod.Module.Mod.Path != pass.Module.Path {
			return nil, fmt.Errorf("%s: expected %q, got %q", goModFilename, pass.Module.Path, goMod.Module.Mod.Path)
		}
		policies, err := gomoddirectivecomments.Parse(goMod, "gosocialcheck", directivePolicyUntrusted)
		if err != nil {
			return nil, fmt.Errorf("failed to parse gosocialcheck directives in %q: %w", goModFilename, err)
		}
		// TODO: cache go.sum
		goSumFilename := filepath.Join(filepath.Dir(goModFilename), "go.sum")
		// pass.ReadFile does not support go.sum
		goSumB, err := os.ReadFile(goSumFilename)
		if err != nil {
			return nil, fmt.Errorf("failed to read %q: %w", goSumFilename, err)
		}
		goSum, err := parseGoSum(bytes.NewReader(goSumB))
		if err != nil {
			return nil, err
		}

		// Record the module so Flush can check its indirect dependencies, which
		// are listed in go.mod but never imported by the analyzed source.
		inst.recordModule(&modInfo{
			goModFilename: goModFilename,
			goSumFilename: goSumFilename,
			goMod:         goMod,
			goSum:         goSum,
			policies:      policies,
		})

		for _, file := range pass.Files {
			for _, imp := range file.Imports {
				if imp.Path == nil {
					return nil, errors.New("got nil ast.ImportSpec.Path")
				}
				p, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return nil, err
				}
				modV := moduleVersion(goMod, p)
				if modV == nil {
					slog.DebugContext(ctx, "module entry not found (negligible for stdlib and local imports)", "path", p)
					continue
				}
				if policies[modV.Path] == directivePolicyTrusted {
					slog.DebugContext(ctx, "module marked as trusted via gosocialcheck:trusted directive", "path", modV.Path)
					continue
				}
				goSumE := goSum[modV.Path+" "+modV.Version]
				h1 := goSumE.H1
				inst.processedSumsMu.RLock()
				_, h1Processed := inst.processedSums[h1]
				inst.processedSumsMu.RUnlock()
				if h1Processed {
					continue
				}
				inst.processedSumsMu.Lock()
				inst.processedSums[h1] = struct{}{}
				inst.processedSumsMu.Unlock()
				slog.DebugContext(ctx, "module", "path", p, "modpath", modV.Path, "modver", modV.Version, "h1", h1)
				hit, err := inst.Opts.Cache.Lookup(ctx, h1)
				if err != nil {
					return nil, err
				}
				if len(hit) == 0 {
					msg := fmt.Sprintf("import '%s': module '%s' does not seem adopted by a trusted project "+
						"(negligible if you trust the module)",
						p, modV.String())
					if inst.Opts.GHA {
						// Annotate the go.sum line only. If the module has no
						// go.sum entry there is nothing to annotate.
						if goSumE.Line > 0 {
							f := ghaFinding{
								msg: msg,
								sumPosn: token.Position{
									Filename: goSumFilename,
									Line:     goSumE.Line,
									Column:   1,
								},
							}
							if changed, ok := inst.changedGoSumLines(ctx, goSumFilename); ok {
								f.changeSetKnown = true
								_, f.changedInPR = changed[goSumE.Line]
							}
							inst.addGHAFinding(f)
						} else {
							slog.DebugContext(ctx, "no go.sum line for module; skipping GHA annotation",
								"path", p, "modpath", modV.Path)
						}
					} else {
						pass.Report(analysis.Diagnostic{
							Pos:     imp.Pos(),
							End:     imp.End(),
							Message: msg,
						})
					}
				} else {
					slog.DebugContext(ctx, "cache hit", "path", p, "hit[0]", hit[0])
				}
			}
		}
		return nil, nil
	}
}

// ghaMaxAnnotations bounds how many GitHub Actions annotations --gha emits, to
// stay within GitHub's per-run limit (50). On a pull request, findings for
// untouched modules are dropped entirely (see flushGHA); the cap is a backstop
// for events where the change set is unknown. Findings whose go.sum line changed
// in the pull request are emitted first, so the most relevant ones survive.
const ghaMaxAnnotations = 50

func (inst *instance) recordModule(mi *modInfo) {
	inst.modsMu.Lock()
	if _, ok := inst.mods[mi.goModFilename]; !ok {
		inst.mods[mi.goModFilename] = mi
	}
	inst.modsMu.Unlock()
}

// flush checks indirect dependencies and emits findings. In --gha mode the
// indirect findings join the buffered direct findings before being prioritized,
// capped, and emitted as workflow commands; the returned count is 0 because
// --gha always exits 0. Outside --gha mode the indirect findings are printed and
// the returned count feeds the diagnostic exit code (direct findings are emitted
// separately by the analysis framework).
func (inst *instance) flush(ctx context.Context) (int, error) {
	findings, err := inst.collectIndirect(ctx)
	if inst.Opts.GHA {
		for _, f := range findings {
			inst.addGHAFinding(f)
		}
		inst.flushGHA(ctx)
		return 0, err
	}
	for _, f := range findings {
		fmt.Fprintf(os.Stderr, "%s:%d:%d: %s\n", f.modPosn.Filename, f.modPosn.Line, f.modPosn.Column, f.msg)
	}
	return len(findings), err
}

// collectIndirect iterates the require list of every recorded module and returns
// a finding for each non-trusted module that is not adopted by a trusted project
// and was not already reported via an import site (tracked in processedSums).
// This covers indirect dependencies, which never appear as imports in the
// analyzed source.
func (inst *instance) collectIndirect(ctx context.Context) ([]ghaFinding, error) {
	inst.modsMu.Lock()
	names := make([]string, 0, len(inst.mods))
	for name := range inst.mods {
		names = append(names, name)
	}
	sort.Strings(names)
	mods := make([]*modInfo, 0, len(names))
	for _, name := range names {
		mods = append(mods, inst.mods[name])
	}
	inst.modsMu.Unlock()

	var res []ghaFinding
	for _, mi := range mods {
		for _, r := range mi.goMod.Require {
			modV := resolveReplace(mi.goMod, r.Mod)
			if modV == nil {
				continue
			}
			if mi.policies[modV.Path] == directivePolicyTrusted {
				slog.DebugContext(ctx, "module marked as trusted via gosocialcheck:trusted directive", "path", modV.Path)
				continue
			}
			goSumE := mi.goSum[modV.Path+" "+modV.Version]
			h1 := goSumE.H1
			if h1 == "" {
				slog.DebugContext(ctx, "no go.sum entry for required module; skipping", "path", modV.Path, "version", modV.Version)
				continue
			}
			inst.processedSumsMu.Lock()
			_, h1Processed := inst.processedSums[h1]
			if !h1Processed {
				inst.processedSums[h1] = struct{}{}
			}
			inst.processedSumsMu.Unlock()
			if h1Processed {
				// Already reported via an import site (direct dependency).
				continue
			}
			hit, err := inst.Opts.Cache.Lookup(ctx, h1)
			if err != nil {
				return res, err
			}
			if len(hit) > 0 {
				slog.DebugContext(ctx, "cache hit", "path", modV.Path, "hit[0]", hit[0])
				continue
			}
			kind := "dependency"
			if r.Indirect {
				kind = "indirect dependency"
			}
			msg := fmt.Sprintf("module '%s' (%s) does not seem adopted by a trusted project "+
				"(negligible if you trust the module)", modV.String(), kind)
			f := ghaFinding{
				msg: msg,
				// Non-GHA findings point at the go.mod require line; GHA findings
				// annotate the go.sum line (set below when available).
				modPosn: token.Position{
					Filename: mi.goModFilename,
					Line:     requireLine(r),
					Column:   1,
				},
			}
			if inst.Opts.GHA {
				if goSumE.Line == 0 {
					slog.DebugContext(ctx, "no go.sum line for module; skipping GHA annotation", "path", modV.Path)
					continue
				}
				f.sumPosn = token.Position{
					Filename: mi.goSumFilename,
					Line:     goSumE.Line,
					Column:   1,
				}
				if changed, ok := inst.changedGoSumLines(ctx, mi.goSumFilename); ok {
					f.changeSetKnown = true
					_, f.changedInPR = changed[goSumE.Line]
				}
			}
			res = append(res, f)
		}
	}
	return res, nil
}

// requireLine returns the 1-based go.mod line of a require entry, or 0 if it is
// not available.
func requireLine(r *modfile.Require) int {
	if r.Syntax != nil {
		return r.Syntax.Start.Line
	}
	return 0
}

func (inst *instance) addGHAFinding(f ghaFinding) {
	inst.ghaFindingsMu.Lock()
	inst.ghaFindings = append(inst.ghaFindings, f)
	inst.ghaFindingsMu.Unlock()
}

func (inst *instance) flushGHA(ctx context.Context) {
	if !inst.Opts.GHA {
		return
	}
	inst.ghaFindingsMu.Lock()
	findings := inst.ghaFindings
	inst.ghaFindings = nil
	inst.ghaFindingsMu.Unlock()

	// On a pull request, annotate only the modules actually added/changed in the
	// PR: warnings for untouched modules are just noise and waste the limited
	// annotation budget (issue #69). When the change set for a finding's go.sum
	// file could not be determined (e.g. not a pull_request event, or the base
	// branch could not be fetched), keep the finding and fall back to the
	// prioritize-and-cap behavior below.
	kept := findings[:0]
	for _, f := range findings {
		if f.changeSetKnown && !f.changedInPR {
			continue
		}
		kept = append(kept, f)
	}
	findings = kept

	// Deterministic order regardless of how passes were scheduled: PR-changed
	// findings first, then by go.sum line and message.
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.changedInPR != b.changedInPR {
			return a.changedInPR
		}
		if a.sumPosn.Line != b.sumPosn.Line {
			return a.sumPosn.Line < b.sumPosn.Line
		}
		return a.msg < b.msg
	})

	shown := len(findings)
	if shown > ghaMaxAnnotations {
		shown = ghaMaxAnnotations
	}
	for _, f := range findings[:shown] {
		emitGHAWarning(inst.cwd, f.sumPosn, f.sumPosn, f.msg)
	}
	if shown < len(findings) {
		slog.WarnContext(ctx, "suppressed findings to stay within the GitHub Actions annotation limit",
			"shown", shown, "total", len(findings), "limit", ghaMaxAnnotations)
	}
}

// changedGoSumLines returns the set of 1-based line numbers in goSumFilename
// that were added or modified in the current pull request. The second return
// value reports whether the change set was determined: it is false when not
// running on a pull_request event or when the diff could not be computed, in
// which case no finding is treated as PR-changed. The result is computed once
// per file and cached.
func (inst *instance) changedGoSumLines(ctx context.Context, goSumFilename string) (map[int]struct{}, bool) {
	inst.changedSumLinesMu.Lock()
	defer inst.changedSumLinesMu.Unlock()
	if inst.changedSumLinesDone[goSumFilename] {
		set := inst.changedSumLines[goSumFilename]
		return set, set != nil
	}
	set := inst.computeChangedGoSumLines(ctx, goSumFilename)
	inst.changedSumLinesDone[goSumFilename] = true
	inst.changedSumLines[goSumFilename] = set
	return set, set != nil
}

func (inst *instance) progress(ctx context.Context, msg string) {
	if inst.Opts.OnProgress != nil {
		inst.Opts.OnProgress(ctx, progress.Event{Message: msg})
	}
}

func (inst *instance) computeChangedGoSumLines(ctx context.Context, goSumFilename string) map[int]struct{} {
	baseRef := os.Getenv("GITHUB_BASE_REF")
	if baseRef == "" {
		// Not a pull_request event; nothing is treated as PR-changed.
		return nil
	}
	dir := filepath.Dir(goSumFilename)
	base := filepath.Base(goSumFilename)
	baseTip, ok := inst.resolveBaseTip(ctx, dir, baseRef)
	if !ok {
		return nil
	}
	// Diff the base tip against the working tree, so the reported line
	// numbers match the go.sum the analyzer read.
	out, err := runGitDiff(ctx, dir, baseTip, base)
	if err != nil {
		slog.WarnContext(ctx, "could not diff go.sum against base branch for --gha",
			"baseRef", baseRef, "error", err)
		return nil
	}
	return parseDiffNewLines(out)
}

// resolveBaseTip returns the git ref to diff the working tree against for the
// pull_request base branch. It prefers a local branch when it is newer than the
// remote-tracking ref (e.g. when the base branch is checked out and ahead of
// origin), then the remote-tracking ref (e.g. with fetch-depth: 0). When the
// base branch is absent locally (actions/checkout's default shallow clone omits
// it), it fetches the tip on demand into FETCH_HEAD.
func (inst *instance) resolveBaseTip(ctx context.Context, dir, baseRef string) (string, bool) {
	localRef := "refs/heads/" + baseRef
	remoteRef := "origin/" + baseRef
	localOK := gitRefExists(ctx, dir, localRef)
	remoteOK := gitRefExists(ctx, dir, remoteRef)
	switch {
	case localOK && remoteOK:
		// Prefer the local branch only when the remote tip is an ancestor of
		// it (i.e. the local branch is newer); otherwise the remote tip is at
		// least as up to date.
		if _, err := runGit(ctx, dir, "merge-base", "--is-ancestor", remoteRef, localRef); err == nil {
			return localRef, true
		}
		return remoteRef, true
	case localOK:
		return localRef, true
	case remoteOK:
		return remoteRef, true
	}
	inst.progress(ctx, fmt.Sprintf("fetching the base ref %q", baseRef))
	if out, err := runGit(ctx, dir, "fetch", "--no-tags", "--depth=1", "origin", baseRef); err != nil {
		slog.WarnContext(ctx, "could not fetch base branch for --gha; not prioritizing PR-changed go.sum lines",
			"baseRef", baseRef, "error", err, "output", out)
		return "", false
	}
	return "FETCH_HEAD", true
}

func gitRefExists(ctx context.Context, dir, ref string) bool {
	_, err := runGit(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func runGitDiff(ctx context.Context, dir, base, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "diff", "--unified=0", "--no-color", base, "--", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// hunkHeaderRe matches unified-diff hunk headers, capturing the new-side start
// line and optional line count, e.g. "@@ -1,2 +3,4 @@".
var hunkHeaderRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

func parseDiffNewLines(diff string) map[int]struct{} {
	res := make(map[int]struct{})
	sc := bufio.NewScanner(strings.NewReader(diff))
	for sc.Scan() {
		m := hunkHeaderRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		start, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		count := 1
		if m[2] != "" {
			count, err = strconv.Atoi(m[2])
			if err != nil {
				continue
			}
		}
		for i := 0; i < count; i++ {
			res[start+i] = struct{}{}
		}
	}
	return res
}

func moduleVersion(goMod *modfile.File, imp string) *module.Version {
	// Find the require entry for this import
	var reqMod *module.Version
	for _, r := range goMod.Require {
		// TODO: check multiple matches
		if r.Mod.Path == imp || strings.HasPrefix(imp, r.Mod.Path+"/") {
			reqMod = &r.Mod
			break
		}
	}
	if reqMod == nil {
		return nil
	}
	return resolveReplace(goMod, *reqMod)
}

// resolveReplace applies any matching replace directive to reqMod and returns
// the effective module version to check. It returns nil when the module is
// replaced by a local path (which has no go.sum entry).
func resolveReplace(goMod *modfile.File, reqMod module.Version) *module.Version {
	for _, r := range goMod.Replace {
		// Match if: same path AND (replace has no version OR versions match)
		if r.Old.Path == reqMod.Path && (r.Old.Version == "" || r.Old.Version == reqMod.Version) {
			// Local path replacements have no go.sum entry
			if isLocalPath(r.New.Path) {
				return nil
			}
			newMod := r.New
			return &newMod
		}
	}
	return &reqMod
}

func isLocalPath(path string) bool {
	return strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") || strings.HasPrefix(path, "/")
}

// Per GitHub Actions workflow-command spec: property values escape
// `%`, `\r`, `\n`, `:`, `,`; message data escapes `%`, `\r`, `\n`.
var (
	ghaPropEscaper = strings.NewReplacer(
		"%", "%25",
		"\r", "%0D",
		"\n", "%0A",
		":", "%3A",
		",", "%2C",
	)
	ghaMsgEscaper = strings.NewReplacer(
		"%", "%25",
		"\r", "%0D",
		"\n", "%0A",
	)
)

func emitGHAWarning(cwd string, posn, endPosn token.Position, msg string) {
	file := posn.Filename
	if rel, err := filepath.Rel(cwd, file); err == nil {
		file = rel
	}
	fmt.Printf("::warning file=%s,line=%d,col=%d,endLine=%d,endColumn=%d,title=%s::%s\n",
		ghaPropEscaper.Replace(file),
		posn.Line,
		posn.Column,
		endPosn.Line,
		endPosn.Column,
		ghaPropEscaper.Replace("gosocialcheck"),
		ghaMsgEscaper.Replace(msg),
	)
}

type goSumEntry struct {
	H1   string
	Line int // 1-based line number in go.sum
}

func parseGoSum(r io.Reader) (map[string]goSumEntry, error) {
	sc := bufio.NewScanner(r)
	res := make(map[string]goSumEntry)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return res, fmt.Errorf("expected 3 fields, got %v", fields)
		}
		res[fields[0]+" "+fields[1]] = goSumEntry{H1: fields[2], Line: lineNo}
	}
	return res, sc.Err()
}
