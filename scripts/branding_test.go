package main

import (
	"encoding/json"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnimatedTitleUsesBrandedMediaWithCompleteLoopAndStaticFallback(t *testing.T) {
	file, err := os.Open("../images/sodapop-title.gif")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	animation, err := gif.DecodeAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if animation.Config.Width != 1000 || animation.Config.Height != 560 ||
		animation.LoopCount != 0 || len(animation.Image) < 2 ||
		len(animation.Delay) != len(animation.Image) || len(animation.Disposal) != len(animation.Image) {
		t.Fatalf("invalid title animation: %+v", animation.Config)
	}
	duration := 0
	for index, delay := range animation.Delay {
		if delay < 2 || animation.Disposal[index] != gif.DisposalNone {
			t.Fatalf("frame %d flashes or clears its predecessor: delay=%d disposal=%d", index, delay, animation.Disposal[index])
		}
		duration += delay
	}
	if duration != 400 {
		t.Fatalf("title duration is %d centiseconds, want 400", duration)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 2*1024*1024 {
		t.Fatalf("title exceeds its 2 MiB README budget: %d bytes", info.Size())
	}
	still, err := os.Open("../images/sodapop-title.png")
	if err != nil {
		t.Fatal(err)
	}
	defer still.Close()
	config, err := png.DecodeConfig(still)
	if err != nil || config.Width != 1000 || config.Height != 560 {
		t.Fatalf("missing or incorrect still: %+v, %v", config, err)
	}
	data, err := os.ReadFile("../images/brand-assets.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Brand  string
		Assets []struct {
			Name       string
			GIF        string `json:"gif"`
			PNG        string `json:"png"`
			Width      int
			Height     int
			Frames     int
			DurationMS int `json:"durationMs"`
			GIFBytes   int64
			Inputs     []string
		}
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Brand != "Sodapop" {
		t.Fatalf("incorrect brand: %q", catalog.Brand)
	}
	matches := 0
	for _, asset := range catalog.Assets {
		if asset.Name != "sodapop-title" {
			continue
		}
		matches++
		if asset.GIF != "sodapop-title.gif" || asset.PNG != "sodapop-title.png" ||
			asset.Width != 1000 || asset.Height != 560 || asset.DurationMS != 4000 ||
			asset.Frames != len(animation.Image) || asset.GIFBytes != info.Size() ||
			strings.Join(asset.Inputs, ",") != "sodapop-wordmark.png,sodapop-mascot.png" {
			t.Fatalf("title metadata does not describe the published branded media: %+v", asset)
		}
	}
	if matches != 1 {
		t.Fatalf("expected one title entry, found %d", matches)
	}
}

func TestTitleHeadsREADMEAndPreservesFeatureDemos(t *testing.T) {
	data, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(data)
	title := strings.Index(readme, `src="images/sodapop-title.gif"`)
	demo := strings.Index(readme, `src="docs/assets/demos/overview.gif"`)
	if title < 0 || demo < 0 || title >= demo {
		t.Fatal("README must place the brand title before the separate product overview")
	}
	for _, required := range []string{
		`media="(prefers-reduced-motion: reduce)"`,
		`srcset="images/sodapop-title.png"`,
		`href="images/sodapop-title.png"`,
		`alt="Sodapop`,
	} {
		if !strings.Contains(readme[:demo], required) {
			t.Errorf("title is missing %s", required)
		}
	}
	if !strings.Contains(readme, "make brand") {
		t.Error("README is missing the title regeneration command")
	}
}

func TestMakeBrandTestsBeforeRenderingAndDoesNotLaunchApplication(t *testing.T) {
	for _, fail := range []bool{false, true} {
		root := scriptFixture(t)
		writeFixtureFile(t, root, "fake-bin/python3", `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> brand-commands.log
if [[ -f stop-brand-tests && "$*" == *unittest* ]]; then
  printf 'fixture renderer tests failed\n' >&2
  exit 1
fi
`, 0700)
		if fail {
			writeFixtureFile(t, root, "stop-brand-tests", "", 0600)
		}
		output, err := runScriptFixture(t, root, []string{"make", "--no-print-directory", "brand"})
		if (err != nil) != fail {
			t.Fatalf("brand workflow failure propagation: %s, %v", output, err)
		}
		want := []string{"-B -m unittest discover -s images/source -p test_animate.py"}
		if !fail {
			want = append(want, "-B images/source/animate.py")
		}
		assertFixtureLines(t, root, "brand-commands.log", want)
		for _, path := range []string{"go-build.log", "go-tool.log"} {
			if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
				t.Fatalf("brand rendering invoked application/runtime build: %s, %v", path, err)
			}
		}
	}
}
