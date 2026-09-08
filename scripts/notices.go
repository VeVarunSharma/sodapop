package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type module struct {
	Path    string
	Version string
	Dir     string
	Main    bool
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/notices.go OUTPUT_DIRECTORY")
		os.Exit(1)
	}
	if err := collect(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func collect(destination string) error {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("list dependency notices: %w: %s", err, stderr.String())
	}
	if err := os.MkdirAll(destination, 0755); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var dependency module
		err := decoder.Decode(&dependency)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if dependency.Main || dependency.Dir == "" {
			continue
		}
		entries, err := os.ReadDir(dependency.Dir)
		if err != nil {
			return fmt.Errorf("read notices for %s: %w", dependency.Path, err)
		}
		found := false
		for _, entry := range entries {
			name := strings.ToUpper(entry.Name())
			if entry.IsDir() || (!strings.HasPrefix(name, "LICENSE") && !strings.HasPrefix(name, "LICENCE") && !strings.HasPrefix(name, "COPYING") && !strings.HasPrefix(name, "NOTICE")) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dependency.Dir, entry.Name()))
			if err != nil {
				return err
			}
			id := strings.NewReplacer("/", "__", "\\", "__").Replace(dependency.Path + "@" + dependency.Version)
			moduleDir := filepath.Join(destination, id)
			if err := os.MkdirAll(moduleDir, 0755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(moduleDir, entry.Name()), data, 0644); err != nil {
				return err
			}
			found = true
		}
		if !found {
			return fmt.Errorf("dependency %s has no root license/notice file; review its distribution terms before packaging", dependency.Path)
		}
	}
}
