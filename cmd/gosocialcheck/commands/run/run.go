package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"

	"github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/cacheopt"
	"github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/flagutil"
	"github.com/AkihiroSuda/gosocialcheck/pkg/analyzer"
	"github.com/AkihiroSuda/gosocialcheck/pkg/cache"
	"github.com/AkihiroSuda/gosocialcheck/pkg/netutil/scorecard"
	"github.com/AkihiroSuda/gosocialcheck/pkg/progress"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "run [package ...]",
		Short:                 "Run the analyzer",
		RunE:                  action,
		DisableFlagsInUseLine: true,
	}
	flags := cmd.Flags()
	flags.Bool("gha", false,
		"Emit diagnostics as GitHub Actions workflow commands and always exit 0")
	flags.Bool("scorecard", false,
		"Check OpenSSF Scorecard scores of the dependencies (opt-in, as not every repo is covered by Scorecard)")
	flags.Float64("scorecard-min-score", scorecard.DefaultMinScore,
		"Minimum acceptable OpenSSF Scorecard score (0-10), for --scorecard")
	flags.String("modfile", "",
		"Use the specified file (e.g. vendor.mod) instead of go.mod, like \"go -modfile\". "+
			"The corresponding go.sum is the file with the .mod suffix replaced by .sum (e.g. vendor.sum). "+
			"Unlike \"go -modfile\", go.mod does not need to exist next to the file")
	return cmd
}

func action(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if len(args) == 0 {
		return errors.New("at least one package pattern is required (e.g. ./...)")
	}
	cacheOpts, err := cacheopt.FromCommand(cmd)
	if err != nil {
		return err
	}
	onProgress := func(ctx context.Context, ev progress.Event) {
		slog.InfoContext(ctx, "progress: "+ev.Message)
	}
	cacheOpts = append(cacheOpts, cache.WithProgressEventHandler(onProgress))
	c, err := cache.New(cacheOpts...)
	if err != nil {
		return err
	}
	if err = c.EnsureUpdated(ctx); err != nil {
		return err
	}
	flags := cmd.Flags()
	gha, err := flags.GetBool("gha")
	if err != nil {
		return err
	}
	scorecardEnabled, err := flags.GetBool("scorecard")
	if err != nil {
		return err
	}
	scorecardMinScore, err := flags.GetFloat64("scorecard-min-score")
	if err != nil {
		return err
	}
	modFile, err := flags.GetString("modfile")
	if err != nil {
		return err
	}
	goflags := flagutil.PFlagSetToGoFlagSet(flags, []string{"debug", "cache-mode", "gha", "scorecard", "scorecard-min-score", "modfile"})
	opts := analyzer.Opts{
		Flags:      *goflags,
		Cache:      c,
		GHA:        gha,
		OnProgress: onProgress,
	}
	if scorecardEnabled {
		opts.Scorecard = c.Scorecard()
		opts.ScorecardMinScore = scorecardMinScore
	}
	a, err := analyzer.New(ctx, opts)
	if err != nil {
		return err
	}

	pkgsCfg := &packages.Config{
		Context: ctx,
		Mode:    packages.LoadAllSyntax | packages.NeedModule,
	}
	if modFile != "" {
		if err = configureModFile(pkgsCfg, modFile); err != nil {
			return err
		}
	}
	initial, err := packages.Load(pkgsCfg, args...)
	if err != nil {
		return fmt.Errorf("failed to load packages: %w", err)
	}
	if len(initial) == 0 {
		return fmt.Errorf("no packages matched %v", args)
	}
	pkgErrors := packages.PrintErrors(initial)

	graph, err := checker.Analyze([]*analysis.Analyzer{a.Analyzer}, initial, nil)
	if err != nil {
		return err
	}
	// The analysis pass only sees direct (imported) dependencies; print those
	// findings first.
	if err := graph.PrintText(os.Stderr, -1); err != nil {
		return err
	}
	// Then check indirect dependencies (and, in --gha mode, emit the buffered
	// direct findings prioritized and capped to fit GitHub's annotation limit).
	indirectDiags, err := a.Flush(ctx)
	if err != nil {
		return err
	}

	var analyzerErrors, rootDiags int
	for act := range graph.All() {
		if act.Err != nil {
			analyzerErrors++
		} else if act.IsRoot {
			rootDiags += len(act.Diagnostics)
		}
	}
	if pkgErrors > 0 || analyzerErrors > 0 {
		return fmt.Errorf("analysis failed: %d package error(s), %d analyzer error(s)", pkgErrors, analyzerErrors)
	}
	if total := rootDiags + indirectDiags; total > 0 {
		return fmt.Errorf("found %d diagnostic(s)", total)
	}
	return nil
}

// configureModFile configures pkgsCfg to load packages with `go -modfile=modFile`.
//
// `go -modfile` still requires go.mod to exist in the module root, so an empty
// go.mod is overlaid next to modFile when it does not exist, for repositories
// that only have e.g. vendor.mod and vendor.sum.
func configureModFile(pkgsCfg *packages.Config, modFile string) error {
	if !strings.HasSuffix(modFile, ".mod") {
		return fmt.Errorf("--modfile=%s: file must have .mod extension", modFile)
	}
	modFile, err := filepath.Abs(modFile)
	if err != nil {
		return err
	}
	if _, err = os.Stat(modFile); err != nil {
		return fmt.Errorf("--modfile: %w", err)
	}
	pkgsCfg.BuildFlags = append(pkgsCfg.BuildFlags, "-modfile="+modFile)
	goMod := filepath.Join(filepath.Dir(modFile), "go.mod")
	if _, err = os.Stat(goMod); errors.Is(err, os.ErrNotExist) {
		slog.Debug("overlaying an empty go.mod for --modfile", "goMod", goMod, "modFile", modFile)
		pkgsCfg.Overlay = map[string][]byte{goMod: {}}
	} else if err != nil {
		return err
	}
	return nil
}
