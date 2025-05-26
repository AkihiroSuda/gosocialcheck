package run

import (
	"github.com/spf13/cobra"
	"golang.org/x/tools/go/analysis/singlechecker"

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
	ctx := cmd.Context()
	c, err := cache.New()
	if err != nil {
		return err
	}
	if _, err = c.LastUpdated(); err != nil {
		return err
	}
	flags := cmd.Flags()
	goflags := flagutil.PFlagSetToGoFlagSet(flags, []string{"debug"})
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
