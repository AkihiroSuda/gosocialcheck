# gosocialcheck: social reputation checker for Go modules

gosocialcheck checks whether a Go module is already adopted by a trustworthy project.

List of trusted projects:
- [CNCF Graduated](https://www.cncf.io/projects/) (Kubernetes, containerd, etc.)

## Install
```bash
go install github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck@latest
```

## Usage
```
# Set the token if facing the GitHub API rate limit (see below)
export GITHUB_TOKEN=...

gosocialcheck update

gosocialcheck run ./...
```

This command checks whether the **dependencies** of the current module (`./...`) are used by trusted projects.
This command does not check whether the the current module itself is used by trusted projects.

Example output:
```
/Users/suda/gopath/src/github.com/AkihiroSuda/gosocialcheck/pkg/analyzer/analyzer.go:18:2:
import 'golang.org/x/tools/go/analysis': module 'golang.org/x/tools@v0.33.0' does not seem adopted by a trusted project (negligible if you trust the module)
/Users/suda/gopath/src/github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/commands/run/run.go:5:2:
import 'golang.org/x/tools/go/analysis/singlechecker': module 'golang.org/x/tools@v0.33.0' does not seem adopted by a trusted project (negligible if you trust the module)
/Users/suda/gopath/src/github.com/AkihiroSuda/gosocialcheck/cmd/gosocialcheck/main.go:8:2:
import 'github.com/lmittmann/tint': module 'github.com/lmittmann/tint@v1.0.7' does not seem adopted by a trusted project (negligible if you trust the module)
```

## Hints
### GitHub API rate limit
gosocialcheck uses the GitHub API for the following operations:
- Fetch git tags, via `api.github.com`.
- Fetch `go.mod` and `go.sum`, via `http://raw.githubusercontent.com`.

These API calls often fails unless the API token is set.

To mitigate the API rate limit, set the token as follows:
1. Open <https://github.com/settings/tokens/>.
2. Click `Generate new token`.
3. Generate a token with the following configuration:
  - Token name: (arbitrary name, e.g., `gosocialcheck`)
  - Expiration: (arbitrary lifetime, but 365 days at most)
  - Repository access: `Public repositories`
  - Account permissions: `No access` for all.
4. Set the token as `$GITHUB_TOKEN`.
```bash
export GITHUB_TOKEN=...
```
