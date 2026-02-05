// FIXME: gomodjail support is currently broken
// gomodjail:confined
module github.com/AkihiroSuda/gosocialcheck

go 1.24.0

require (
	github.com/lmittmann/tint v1.1.3 // gomodjail:unconfined
	github.com/spf13/cobra v1.10.2 // gomodjail:unconfined
	github.com/spf13/pflag v1.0.10
	golang.org/x/mod v0.32.0
	golang.org/x/sync v0.19.0 // gomodjail:unconfined
	golang.org/x/tools v0.41.0 // gomodjail:unconfined
	gopkg.in/yaml.v3 v3.0.1
	gotest.tools/v3 v3.5.2
)

require (
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
)
