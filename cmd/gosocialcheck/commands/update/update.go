package update

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

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
	flags := cmd.Flags()
	dirHelp := "database directory; mainly for maintaining pkg/embeddeddb/db"
	if d, err := os.UserCacheDir(); err == nil {
		dirHelp = fmt.Sprintf("database directory (default: %s); mainly for maintaining pkg/embeddeddb/db",
			filepath.Join(d, "gosocialcheck"))
	}
	flags.String("dir", "", dirHelp)
	return cmd
}

func action(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	onProgress := func(ctx context.Context, ev cache.ProgressEvent) {
		slog.InfoContext(ctx, "progress: "+ev.Message)
	}
	opts := []cache.Opt{cache.WithProgressEventHandler(onProgress)}
	flags := cmd.Flags()
	dir, err := flags.GetString("dir")
	if err != nil {
		return err
	}
	if dir != "" {
		opts = append(opts, cache.WithDir(dir))
	}
	c, err := cache.New(opts...)
	if err != nil {
		return err
	}
	return c.Update(ctx)
}
