package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/zgo-cli/zgo/internal/commands"
	"github.com/zgo-cli/zgo/internal/config"
)

var (
	Version   = "2.0.0"
	GitCommit = "devel"
	BuildDate = "2026-10-01"
)

func printUsage() {
	fmt.Printf(`ZGo v%s — Remote 'go install', Cache-First

Usage:
  zgo <command> [arguments] [flags]

Commands:
  install      Install a Go binary without local Go compiler (cache-first)
  list         List packages installed by ZGo
  upgrade      Upgrade one or all installed packages to latest version
  uninstall    Uninstall a package and remove its binaries
  verify       Verify binary integrity and Sigstore attestation
  doctor       Run diagnostic checks on your environment
  cache        Inspect or clean local ZGo cache
  version      Show ZGo CLI version

Install Flags:
  --os string          Target operating system (e.g. linux, darwin, windows)
  --arch string        Target architecture (e.g. amd64, arm64)
  --cgo                Enable CGO compilation (default false)
  --tags string        Build tags to pass to go install
  --timeout duration   Build timeout (default 10m)
  --no-cache           Bypass local cache and check remote
  --self-hosted        Use self-hosted builder repository
  --skip-attestation   Skip attestation check (for offline/dry-run)

Global Flags:
  -h, --help           Show help
  -v, --version        Show version

Examples:
  zgo install github.com/charmbracelet/glow@latest
  zgo install github.com/junegunn/fzf@v0.50.0 --os linux --arch arm64
  zgo list
  zgo upgrade github.com/charmbracelet/glow
  zgo uninstall github.com/charmbracelet/glow
  zgo verify github.com/charmbracelet/glow
  zgo doctor
`, Version)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	cfg := config.LoadConfig()
	ctx := context.Background()

	switch cmd {
	case "install":
		installCmd := flag.NewFlagSet("install", flag.ExitOnError)
		osFlag := installCmd.String("os", "", "Target operating system (linux, darwin, windows)")
		archFlag := installCmd.String("arch", "", "Target architecture (amd64, arm64)")
		cgoFlag := installCmd.Bool("cgo", false, "Enable CGO")
		tagsFlag := installCmd.String("tags", "", "Build tags")
		timeoutFlag := installCmd.Duration("timeout", 10*time.Minute, "Build timeout")
		noCacheFlag := installCmd.Bool("no-cache", false, "Bypass local cache")
		selfHostedFlag := installCmd.Bool("self-hosted", false, "Use self-hosted builder repository")
		skipAttestFlag := installCmd.Bool("skip-attestation", false, "Skip attestation verification")

		// Reorder flags if package was passed first (e.g. zgo install pkg --os linux)
		var reordered []string
		var pkgArg string
		for i := 0; i < len(args); i++ {
			arg := args[i]
			if strings.HasPrefix(arg, "-") {
				reordered = append(reordered, arg)
				// If flag has value as next arg (not using =)
				if !strings.Contains(arg, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					// Check boolean flags
					if arg != "--cgo" && arg != "-cgo" && arg != "--no-cache" && arg != "--self-hosted" && arg != "--skip-attestation" {
						i++
						reordered = append(reordered, args[i])
					}
				}
			} else {
				if pkgArg == "" {
					pkgArg = arg
				} else {
					reordered = append(reordered, arg)
				}
			}
		}

		_ = installCmd.Parse(reordered)

		if pkgArg == "" && len(installCmd.Args()) > 0 {
			pkgArg = installCmd.Args()[0]
		}

		var cgoPtr *bool
		installCmd.Visit(func(f *flag.Flag) {
			if f.Name == "cgo" {
				cgoPtr = cgoFlag
			}
		})

		opts := commands.InstallOptions{
			Target:          pkgArg,
			GOOS:            *osFlag,
			GOARCH:          *archFlag,
			CGO:             cgoPtr,
			Tags:            *tagsFlag,
			Timeout:         *timeoutFlag,
			NoCache:         *noCacheFlag,
			SelfHosted:      *selfHostedFlag,
			SkipAttestation: *skipAttestFlag,
		}

		if err := commands.RunInstall(ctx, cfg, opts); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
			os.Exit(1)
		}

	case "list":
		listCmd := flag.NewFlagSet("list", flag.ExitOnError)
		jsonFlag := listCmd.Bool("json", false, "Output in JSON format")
		_ = listCmd.Parse(args)

		if err := commands.RunList(cfg, commands.ListOptions{JSON: *jsonFlag}); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
			os.Exit(1)
		}

	case "upgrade":
		upgradeCmd := flag.NewFlagSet("upgrade", flag.ExitOnError)
		_ = upgradeCmd.Parse(args)
		pkg := ""
		if len(upgradeCmd.Args()) > 0 {
			pkg = upgradeCmd.Args()[0]
		}
		if err := commands.RunUpgrade(ctx, cfg, pkg); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
			os.Exit(1)
		}

	case "uninstall":
		uninstallCmd := flag.NewFlagSet("uninstall", flag.ExitOnError)
		_ = uninstallCmd.Parse(args)
		if len(uninstallCmd.Args()) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: zgo uninstall <pkg>")
			os.Exit(1)
		}
		if err := commands.RunUninstall(cfg, uninstallCmd.Args()[0]); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
			os.Exit(1)
		}

	case "verify":
		verifyCmd := flag.NewFlagSet("verify", flag.ExitOnError)
		_ = verifyCmd.Parse(args)
		if len(verifyCmd.Args()) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: zgo verify <pkg>")
			os.Exit(1)
		}
		if err := commands.RunVerify(ctx, cfg, verifyCmd.Args()[0]); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
			os.Exit(1)
		}

	case "doctor":
		if err := commands.RunDoctor(ctx, cfg); err != nil {
			os.Exit(1)
		}

	case "cache":
		action := "size"
		if len(args) > 0 {
			action = args[0]
		}
		if err := commands.RunCache(cfg, action); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
			os.Exit(1)
		}

	case "version", "-v", "--version":
		fmt.Printf("zgo v%s (%s/%s, commit %s, built %s)\n", Version, runtime.GOOS, runtime.GOARCH, GitCommit, BuildDate)

	case "help", "-h", "--help":
		printUsage()

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\nRun 'zgo --help' for usage.\n", cmd)
		os.Exit(1)
	}
}
