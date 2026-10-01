package env

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zgo-cli/zgo/internal/canonical"
)

// Snapshot captures the resolved environment parameters according to ZGo v2 specification.
type Snapshot struct {
	// Build-affecting parameters (sent to builder)
	GOOS       string
	GOARCH     string
	GOARM      string
	GOAMD64    string
	GO386      string
	GOMIPS     string
	GOMIPS64   string
	GOPPC64    string
	CGOEnabled bool
	GOFLAGS    []string
	Toolchain  string

	// Local parameters (never sent to builder)
	GOBIN  string
	GOPATH string

	// Self-hosted only parameters
	GOPROXY    string
	GOSUMDB    string
	GOPRIVATE  string
	GONOPROXY  string
	GONOSUMDB  string
	GOVCS      string
	SelfHosted bool

	// Warnings encountered during resolution
	Warnings []string
}

// EnvResolver resolves environment variables taking into account:
// 1. CLI flags (highest priority)
// 2. OS environment variables
// 3. `go env -w` file (`os.UserConfigDir()/go/env`)
// 4. Defaults
type EnvResolver struct {
	goEnvMap map[string]string
}

// NewEnvResolver loads the `go env -w` config file if it exists.
func NewEnvResolver() *EnvResolver {
	resolver := &EnvResolver{
		goEnvMap: make(map[string]string),
	}
	resolver.loadGoEnvFile()
	return resolver
}

func (r *EnvResolver) loadGoEnvFile() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	envPath := filepath.Join(configDir, "go", "env")
	file, err := os.Open(envPath)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Format: KEY=VALUE
		if idx := strings.Index(line, "="); idx != -1 {
			k := strings.TrimSpace(line[:idx])
			v := strings.TrimSpace(line[idx+1:])
			// Strip optional quotes
			v = strings.Trim(v, `"'`)
			r.goEnvMap[k] = v
		}
	}
}

// GetValue resolves a single key following the order:
// Flag override -> OS env -> go env file -> default
func (r *EnvResolver) GetValue(key string, flagVal string, defaultVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	if v, ok := r.goEnvMap[key]; ok && v != "" {
		return v
	}
	return defaultVal
}

// FlagsOption represents CLI flag overrides provided to Snapshot()
type FlagsOption struct {
	GOOS       string
	GOARCH     string
	CGO        *bool
	Tags       string
	Toolchain  string
	SelfHosted bool
}

// ResolveSnapshot computes the complete environment snapshot.
func (r *EnvResolver) ResolveSnapshot(opts FlagsOption) (*Snapshot, error) {
	snap := &Snapshot{
		SelfHosted: opts.SelfHosted,
	}

	// 1. Build-affecting variables
	snap.GOOS = strings.ToLower(r.GetValue("GOOS", opts.GOOS, runtime.GOOS))
	snap.GOARCH = strings.ToLower(r.GetValue("GOARCH", opts.GOARCH, runtime.GOARCH))
	snap.GOARM = r.GetValue("GOARM", "", "")
	snap.GOAMD64 = r.GetValue("GOAMD64", "", "")
	snap.GO386 = r.GetValue("GO386", "", "")
	snap.GOMIPS = r.GetValue("GOMIPS", "", "")
	snap.GOMIPS64 = r.GetValue("GOMIPS64", "", "")
	snap.GOPPC64 = r.GetValue("GOPPC64", "", "")
	snap.Toolchain = r.GetValue("GOTOOLCHAIN", opts.Toolchain, "auto")

	// CGO
	if opts.CGO != nil {
		snap.CGOEnabled = *opts.CGO
	} else {
		cgoStr := strings.ToLower(r.GetValue("CGO_ENABLED", "", "0"))
		snap.CGOEnabled = (cgoStr == "1" || cgoStr == "true")
	}

	// GOFLAGS
	rawGOFLAGS := r.GetValue("GOFLAGS", "", "")
	if opts.Tags != "" {
		tagFlag := "-tags=" + opts.Tags
		if rawGOFLAGS != "" {
			rawGOFLAGS = rawGOFLAGS + " " + tagFlag
		} else {
			rawGOFLAGS = tagFlag
		}
	}

	normalizedFlags, err := canonical.ValidateAndNormalizeGOFLAGS(rawGOFLAGS)
	if err != nil {
		return nil, err
	}
	snap.GOFLAGS = normalizedFlags

	// 2. Local-only variables (never sent to builder)
	snap.GOPATH = r.GetValue("GOPATH", "", "")
	snap.GOBIN = r.resolveGOBIN(snap.GOPATH)

	// 3. Self-hosted vs Public variables
	rawProxy := r.GetValue("GOPROXY", "", "")
	rawSumDB := r.GetValue("GOSUMDB", "", "")
	rawPrivate := r.GetValue("GOPRIVATE", "", "")

	if snap.SelfHosted {
		snap.GOPROXY = rawProxy
		if snap.GOPROXY == "" {
			snap.GOPROXY = "https://proxy.golang.org,direct"
		}
		snap.GOSUMDB = rawSumDB
		snap.GOPRIVATE = rawPrivate
		snap.GONOPROXY = r.GetValue("GONOPROXY", "", "")
		snap.GONOSUMDB = r.GetValue("GONOSUMDB", "", "")
		snap.GOVCS = r.GetValue("GOVCS", "", "")
	} else {
		// In public mode, use Go defaults and warn if custom values were set
		if rawProxy != "" && rawProxy != "https://proxy.golang.org,direct" {
			snap.Warnings = append(snap.Warnings, fmt.Sprintf("Public mode ignores custom GOPROXY=%q (using proxy.golang.org). Use --self-hosted to use custom proxy.", rawProxy))
		}
		if rawPrivate != "" {
			snap.Warnings = append(snap.Warnings, fmt.Sprintf("Public mode ignores GOPRIVATE=%q. Use --self-hosted for private modules.", rawPrivate))
		}
		snap.GOPROXY = "https://proxy.golang.org,direct"
		snap.GOSUMDB = "sum.golang.org"
	}

	return snap, nil
}

// resolveGOBIN implements standard Go GOBIN resolution:
// GOBIN -> first GOPATH/bin -> $HOME/go/bin
func (r *EnvResolver) resolveGOBIN(gopath string) string {
	gobin := r.GetValue("GOBIN", "", "")
	if gobin != "" {
		return filepath.Clean(gobin)
	}

	if gopath != "" {
		paths := filepath.SplitList(gopath)
		if len(paths) > 0 && paths[0] != "" {
			return filepath.Join(paths[0], "bin")
		}
	}

	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return filepath.Join(home, "go", "bin")
	}

	// Fallback to /usr/local/bin if home cannot be found
	return filepath.Clean("/usr/local/bin")
}

// CheckGOBINInPATH checks whether GOBIN is present in the system PATH.
func CheckGOBINInPATH(gobin string) bool {
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		return false
	}
	cleanGobin := filepath.Clean(gobin)
	dirs := filepath.SplitList(pathEnv)
	for _, dir := range dirs {
		if filepath.Clean(dir) == cleanGobin {
			return true
		}
	}
	return false
}
