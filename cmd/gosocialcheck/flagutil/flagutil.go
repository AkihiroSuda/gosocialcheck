package flagutil

import (
	"flag"
	"slices"

	"github.com/spf13/pflag"
)

func PFlagSetToGoFlagSet(pf *pflag.FlagSet, excludes []string) *flag.FlagSet {
	fs := flag.NewFlagSet(pf.Name(), flag.ContinueOnError)
	pf.VisitAll(func(f *pflag.Flag) {
		if slices.Contains(excludes, f.Name) {
			return
		}
		fs.Var(f.Value, f.Name, f.Usage)
		if g := fs.Lookup(f.Name); g != nil {
			g.DefValue = f.DefValue
		}
	})
	return fs
}
