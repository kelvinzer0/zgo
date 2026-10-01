package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/zgo-cli/zgo/internal/config"
	"github.com/zgo-cli/zgo/internal/manifest"
)

type ListOptions struct {
	JSON bool
}

func RunList(cfg *config.Config, opts ListOptions) error {
	manifestStore := manifest.NewStore(filepath.Join(cfg.StateDir, "installed.json"))
	m, err := manifestStore.Load()
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	if len(m.Packages) == 0 {
		if opts.JSON {
			fmt.Println("[]")
			return nil
		}
		fmt.Println("No packages currently installed with zgo.")
		return nil
	}

	// Sort by package name
	var pkgs []manifest.InstalledPackage
	for _, p := range m.Packages {
		pkgs = append(pkgs, p)
	}
	sort.Slice(pkgs, func(i, j int) bool {
		return pkgs[i].Package < pkgs[j].Package
	})

	if opts.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(pkgs)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "PACKAGE\tVERSION\tTARGET\tBINARIES\tINSTALLED")
	for _, p := range pkgs {
		var binNames []string
		for _, b := range p.Binaries {
			binNames = append(binNames, b.Name)
		}
		binsStr := fmt.Sprintf("%v", binNames)
		targetStr := fmt.Sprintf("%s/%s", p.Target.GOOS, p.Target.GOARCH)
		installedAgo := time.Since(p.InstalledAt).Truncate(time.Second).String() + " ago"
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", p.Package, p.Version, targetStr, binsStr, installedAgo)
	}
	return w.Flush()
}
