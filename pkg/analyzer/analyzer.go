package analyzer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/tools/go/analysis"

	"github.com/AkihiroSuda/gosocialcheck/pkg/cache"
)

type Opts struct {
	Flags flag.FlagSet
	Cache *cache.Cache
}

func New(ctx context.Context, opts Opts) (*analysis.Analyzer, error) {
	if !reflect.DeepEqual(opts.Flags.Args(), []string{"./..."}) {
		// because only go.{mod,sum} in the current directories are supported
		return nil, errors.New("currently the argument must be './...' (FIXME)")
	}
	a := &analysis.Analyzer{
		Name:             "gosocialcheck",
		Doc:              "Social reputation checker",
		URL:              "https://github.com/AkihiroSuda/gosocialcheck",
		Flags:            opts.Flags,
		Run:              run(ctx, opts),
		RunDespiteErrors: false,
	}
	return a, nil
}

func run(ctx context.Context, opts Opts) func(*analysis.Pass) (any, error) {
	return func(pass *analysis.Pass) (any, error) {
		// TODO: cache go.mod
		// TODO: support multi-module mono repo
		goModFilename := "go.mod"
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
		if len(goMod.Replace) != 0 {
			slog.WarnContext(ctx, "replace is not supported yet")
		}

		// TODO: cache go.sum
		goSumFilename := "go.sum"
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
				h1 := goSum[modV.Path+" "+modV.Version]
				slog.DebugContext(ctx, "module", "path", p, "modpath", modV.Path, "modver", modV.Version, "h1", h1)
				hit, err := opts.Cache.Lookup(ctx, h1)
				if err != nil {
					return nil, err
				}
				if len(hit) == 0 {
					diag := analysis.Diagnostic{
						Pos: imp.Pos(),
						End: imp.End(),
						Message: fmt.Sprintf("import '%s': module '%s' does not seem adopted by a trusted project "+
							"(negligible if you trust the module)",
							p, modV.String()),
					}
					pass.Report(diag)
				} else {
					slog.DebugContext(ctx, "cache hit", "path", p, "hit[0]", hit[0])
				}
			}
		}
		return nil, nil
	}
}

func moduleVersion(goMod *modfile.File, imp string) *module.Version {
	for _, r := range goMod.Require {
		// TODO: check multiple matches
		if r.Mod.Path == imp || strings.HasPrefix(imp, r.Mod.Path+"/") {
			return &r.Mod
		}
	}
	return nil
}

func parseGoSum(r io.Reader) (map[string]string, error) {
	sc := bufio.NewScanner(r)
	res := make(map[string]string)
	for sc.Scan() {
		line := sc.Text()
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return res, fmt.Errorf("expected 3 fields, got %v", fields)
		}
		res[fields[0]+" "+fields[1]] = fields[2]
	}
	return res, sc.Err()
}
