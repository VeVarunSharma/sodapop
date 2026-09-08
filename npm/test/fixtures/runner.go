//go:build ignore

// This is a subprocess fixture, never a native Sodapop smoke test.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "--wait-signal" {
		ch := make(chan os.Signal, 2)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		fmt.Println("fixture-ready")
		<-ch
		fmt.Fprintln(os.Stderr, "fixture-interrupted")
		os.Exit(42)
	}
	status := 0
	if len(args) >= 2 && args[0] == "--exit" {
		var err error
		status, err = strconv.Atoi(args[1])
		if err != nil {
			panic(err)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"fixture": true, "args": args, "cwd": cwd, "stdin": string(input),
	}); err != nil {
		panic(err)
	}
	fmt.Fprintln(os.Stderr, "fixture-stderr")
	os.Exit(status)
}
