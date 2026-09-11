// releasectl is a packaging-time tool; installed Sodapop does not need Go.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/VeVarunSharma/sodapop/internal/distribution"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "releasectl:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: releasectl {manifest|verify|extract|archive|validate} [flags]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	var dir, version, commit, platforms, manifest, platform, output, stage string
	switch args[0] {
	case "manifest":
		flags.StringVar(&dir, "dir", "", "release directory (required)")
		flags.StringVar(&version, "version", "", "SemVer without v prefix (required)")
		flags.StringVar(&commit, "commit", "", "full Git commit (required)")
		flags.StringVar(&platforms, "platforms", "", "comma-separated exact platform set; defaults to all six")
	case "verify", "extract":
		flags.StringVar(&dir, "dir", "", "release directory (required)")
		flags.StringVar(&manifest, "manifest", "", "manifest path (required)")
		flags.StringVar(&platform, "platform", "", "verify only this native target")
		if args[0] == "extract" {
			flags.StringVar(&output, "output", "", "empty or new output directory with an existing parent (required)")
		} else {
			flags.StringVar(&platforms, "platforms", "", "required exact manifest platform set")
		}
	case "archive":
		flags.StringVar(&dir, "dir", "", "existing archive output directory (required)")
		flags.StringVar(&stage, "stage", "", "explicit payload directory named sodapop-VERSION-OS-ARCH (required)")
		flags.StringVar(&version, "version", "", "SemVer without v prefix (required)")
		flags.StringVar(&platform, "platform", "", "native Go target (required)")
	case "validate":
		flags.StringVar(&version, "version", "", "SemVer without v prefix (required)")
		flags.StringVar(&platform, "platform", "", "native Go target (required)")
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	var expected []string
	explicitPlatforms := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "platforms" {
			explicitPlatforms = true
		}
	})
	if explicitPlatforms {
		var err error
		expected, err = distribution.ParsePlatforms(platforms)
		if err != nil {
			return err
		}
	}
	if args[0] == "validate" {
		if err := distribution.ValidateVersion(version); err != nil {
			return err
		}
		return distribution.ValidatePlatform(platform)
	}
	if dir == "" {
		return fmt.Errorf("--dir is required")
	}
	switch args[0] {
	case "manifest":
		if !explicitPlatforms {
			expected = distribution.DefaultPlatforms()
		}
		if _, err := distribution.Generate(dir, version, commit, ".", expected); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Generated %s/sodapop-%s-manifest.json\n", dir, version)
		return nil
	case "archive":
		if stage == "" || platform == "" {
			return fmt.Errorf("--stage and --platform are required")
		}
		artifact, err := distribution.Archive(stage, dir, version, platform)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Packaged %s/%s\n", dir, artifact.Archive)
		return nil
	}
	if manifest == "" {
		return fmt.Errorf("--manifest is required")
	}
	m, err := distribution.ReadManifest(manifest)
	if err != nil {
		return err
	}
	if args[0] == "extract" {
		return distribution.Extract(dir, m, platform, output)
	}
	if !explicitPlatforms && platform == "" {
		expected = distribution.DefaultPlatforms()
	}
	return distribution.Verify(dir, m, platform, expected)
}
