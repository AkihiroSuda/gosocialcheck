// FIXME: gomodjail support is currently broken
// gomodjail:confined
module github.com/AkihiroSuda/gosocialcheck

go 1.23.0

require (
	github.com/lmittmann/tint v1.1.2 // gomodjail:unconfined
	github.com/spf13/cobra v1.9.1 // gomodjail:unconfined
	github.com/spf13/pflag v1.0.6
	golang.org/x/mod v0.26.0
	golang.org/x/sync v0.16.0 // gomodjail:unconfined
	golang.org/x/tools v0.35.0 // gomodjail:unconfined
	gopkg.in/yaml.v3 v3.0.1
	gotest.tools/v3 v3.5.2
)

require (
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
)
