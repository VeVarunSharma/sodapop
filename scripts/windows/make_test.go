package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsMakeHelpAndDevelopmentShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows command wrappers require Windows")
	}
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Fatal(err)
	}

	output, err := exec.Command(
		powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", "Make.ps1", "help",
	).CombinedOutput()
	if err != nil || !strings.Contains(string(output), "make start") {
		t.Fatalf("make help failed: %v\n%s", err, output)
	}

	developmentShell, err := os.ReadFile(filepath.Join("..", "..", "dev.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"-ExecutionPolicy Bypass", "Set-Alias -Name make", "'make.cmd'"} {
		if !strings.Contains(string(developmentShell), required) {
			t.Fatalf("dev.cmd is missing %q", required)
		}
	}
}

func TestWindowsMakeBuildUsesLocalToolsAndEnvironment(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows command wrappers require Windows")
	}
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts", "windows"), 0755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("Make.ps1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "windows", "Make.ps1"), script, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".sodapop.env"),
		[]byte("SODAPOP_GITHUB_CLIENT_ID=fixture.public-client\n"), 0600); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(fakeBin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "go.cmd"), []byte("@exit /b 0\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "build.log")
	bash := "@echo off\r\necho %SODAPOP_GITHUB_CLIENT_ID%^|%* > \"" + logPath + "\"\r\n"
	bashPath := filepath.Join(fakeBin, "bash.cmd")
	if err := os.WriteFile(bashPath, []byte(bash), 0600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(
		powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", filepath.Join(root, "scripts", "windows", "Make.ps1"), "build",
	)
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+";"+os.Getenv("PATH"),
		"SODAPOP_BASH="+bashPath,
		"SODAPOP_GITHUB_CLIENT_ID=",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("make build failed: %v\n%s", err, output)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(logged)); got != "fixture.public-client|scripts/build.sh" {
		t.Fatalf("build invocation = %q", got)
	}
}
