package update

import (
	"context"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/cacheopt"
	"github.com/AkihiroSuda/gosocialcheck/pkg/cache"
	"github.com/AkihiroSuda/gosocialcheck/pkg/progress"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "update",
		Short:                 "Update the database",
		Args:                  cobra.NoArgs,
		RunE:                  action,
		DisableFlagsInUseLine: true,
	}
	flags := cmd.Flags()
	flags.String("cache-remote", cache.DefaultRemoteURL,
		"URL of the remote cache repository")
	return cmd
}

func action(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	onProgress := func(ctx context.Context, ev progress.Event) {
		slog.InfoContext(ctx, "progress: "+ev.Message)
	}
	cacheOpts, err := cacheopt.FromCommand(cmd)
	if err != nil {
		return err
	}
	cacheRemote, _ := cmd.Flags().GetString("cache-remote")
	cacheOpts = append(cacheOpts,
		cache.WithRemoteURL(cacheRemote),
		cache.WithProgressEventHandler(onProgress),
	)
	c, err := cache.New(cacheOpts...)
	if err != nil {
		return err
	}
	return c.Update(ctx)
}
