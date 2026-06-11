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
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	gomoddirectivecomments "github.com/AkihiroSuda/gomoddirectivecomments"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/tools/go/analysis"

	"github.com/AkihiroSuda/gosocialcheck/pkg/cache"
)

type Opts struct {
	Flags flag.FlagSet
	Cache *cache.Cache
	// GHA emits diagnostics as GitHub Actions workflow commands on stdout
	// instead of via [analysis.Pass.Report], so the process exits 0 even
	// when there are findings. See:
	// https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-commands
	GHA bool
}

const (
	directivePolicyUntrusted = "untrusted"
	directivePolicyTrusted   = "trusted"
)

func New(ctx context.Context, opts Opts) (*analysis.Analyzer, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	inst := &instance{
		Opts:          opts,
		processedSums: make(map[string]struct{}),
		cwd:           cwd,
	}
	a := &analysis.Analyzer{
		Name:             "gosocialcheck",
		Doc:              "Social reputation checker",
		URL:              "https://github.com/AkihiroSuda/gosocialcheck",
		Flags:            opts.Flags,
		Run:              run(ctx, inst),
		RunDespiteErrors: false,
	}
	return a, nil
}

type instance struct {
	Opts
	processedSums   map[string]struct{}
	processedSumsMu sync.RWMutex
	cwd             string
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
						emitGHAWarning(inst.cwd,
							pass.Fset.Position(imp.Pos()),
							pass.Fset.Position(imp.End()),
							msg)
						if goSumE.Line > 0 {
							sumPosn := token.Position{
								Filename: goSumFilename,
								Line:     goSumE.Line,
								Column:   1,
							}
							emitGHAWarning(inst.cwd, sumPosn, sumPosn, msg)
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

	// Check for replace directive
	for _, r := range goMod.Replace {
		// Match if: same path AND (replace has no version OR versions match)
		if r.Old.Path == reqMod.Path && (r.Old.Version == "" || r.Old.Version == reqMod.Version) {
			// Local path replacements have no go.sum entry
			if isLocalPath(r.New.Path) {
				return nil
			}
			return &r.New
		}
	}

	return reqMod
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
