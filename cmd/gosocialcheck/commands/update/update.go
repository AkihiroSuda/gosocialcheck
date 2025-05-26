package update

import (
	"context"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/AkihiroSuda/gosocialcheck/pkg/cache"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "update",
		Short:                 "Update the database",
		Args:                  cobra.NoArgs,
		RunE:                  action,
		DisableFlagsInUseLine: true,
	}
	return cmd
}

func action(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	onProgress := func(ctx context.Context, ev cache.ProgressEvent) {
		slog.InfoContext(ctx, "progress: "+ev.Message)
	}
	c, err := cache.New(cache.WithProgressEventHandler(onProgress))
	if err != nil {
		return err
	}
	return c.Update(ctx)
}
