package lookup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/spf13/cobra"

	"github.com/AkihiroSuda/gosocialcheck/pkg/cache"
	"github.com/AkihiroSuda/gosocialcheck/pkg/embeddeddb"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "lookup",
		Short:                 "[DEBUG] Lookup the database by h1 sum",
		Args:                  cobra.ExactArgs(1),
		RunE:                  action,
		DisableFlagsInUseLine: true,
	}
	return cmd
}

func action(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	sum := args[0]
	embFS, err := embeddeddb.FS()
	if err != nil {
		return err
	}
	c, err := cache.New(cache.WithExtraFS(embFS))
	if err != nil {
		return err
	}
	if _, err = c.LastUpdated(); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		embEmpty, embErr := embeddeddb.IsEmpty()
		if embErr != nil {
			return embErr
		}
		if embEmpty {
			return fmt.Errorf("the database is not populated yet (hint: run `gosocialcheck update`): %w", err)
		}
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetEscapeHTML(false)
	res, lookupErr := c.Lookup(ctx, sum)
	// res may contain partial contents even on err
	for _, f := range res {
		if err = enc.Encode(f); err != nil {
			return err
		}
	}
	return lookupErr
}
