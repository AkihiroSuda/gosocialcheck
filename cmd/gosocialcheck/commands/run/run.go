package run

import (
	"context"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/cacheopt"
	"github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/flagutil"
	"github.com/AkihiroSuda/gosocialcheck/pkg/analyzer"
	"github.com/AkihiroSuda/gosocialcheck/pkg/cache"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "run",
		Short:                 "Run the analyzer",
		RunE:                  action,
		DisableFlagsInUseLine: true,
	}
	return cmd
}

func action(cmd *cobra.Command, args []string) error {
	// Rewrite the global os.Args, as a workaround for:
	// - https://github.com/AkihiroSuda/gosocialcheck/issues/1
	// - https://github.com/golang/go/issues/73875
	//
	// golang.org/x/tools/go/analysis/singlechecker parses the global args
	// rather than flag.FlagSet.Args, and raises an error:
	// `-: package run is not in std (/opt/homebrew/Cellar/go/1.24.3/libexec/src/run`
	os.Args = append([]string{"gosocialcheck-run"}, args...)

	ctx := cmd.Context()
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
	goflags := flagutil.PFlagSetToGoFlagSet(flags, []string{"debug", "cache-mode"})
	if err := goflags.Parse(args); err != nil {
		return err
	}
	opts := analyzer.Opts{
		Flags: *goflags,
		Cache: c,
	}
	a, err := analyzer.New(ctx, opts)
	if err != nil {
		return err
	}
	singlechecker.Main(a)
	// NOTREACHED
	return nil
}
