package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"

	"github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/cacheopt"
	"github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/flagutil"
	"github.com/AkihiroSuda/gosocialcheck/pkg/analyzer"
	"github.com/AkihiroSuda/gosocialcheck/pkg/cache"
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
	onProgress := func(ctx context.Context, ev cache.ProgressEvent) {
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
	goflags := flagutil.PFlagSetToGoFlagSet(flags, []string{"debug", "cache-mode", "gha"})
	opts := analyzer.Opts{
		Flags: *goflags,
		Cache: c,
		GHA:   gha,
	}
	a, err := analyzer.New(ctx, opts)
	if err != nil {
		return err
	}

	pkgsCfg := &packages.Config{
		Context: ctx,
		Mode:    packages.LoadAllSyntax | packages.NeedModule,
	}
	initial, err := packages.Load(pkgsCfg, args...)
	if err != nil {
		return fmt.Errorf("failed to load packages: %w", err)
	}
	if len(initial) == 0 {
		return fmt.Errorf("no packages matched %v", args)
	}
	pkgErrors := packages.PrintErrors(initial)

	graph, err := checker.Analyze([]*analysis.Analyzer{a}, initial, nil)
	if err != nil {
		return err
	}
	if err := graph.PrintText(os.Stderr, -1); err != nil {
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
	if rootDiags > 0 {
		return fmt.Errorf("found %d diagnostic(s)", rootDiags)
	}
	return nil
}
