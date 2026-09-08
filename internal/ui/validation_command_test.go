package ui

import (
	"encoding/json"
	"testing"
)

func TestIsValidationCommandRecognizesDirectValidation(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		command string
	}{
		{name: "go test", tool: "bash", command: "go test ./..."},
		{name: "go test quoted argument", tool: "bash", command: `go test -run "Test Model" ./internal/ui`},
		{name: "go vet", tool: "bash", command: "go vet ./..."},
		{name: "gofmt list", tool: "bash", command: "gofmt -l cmd internal scripts"},
		{name: "make test", tool: "bash", command: "make test"},
		{name: "make options and check", tool: "bash", command: "make -j2 check"},
		{name: "make multiple validation targets", tool: "bash", command: "make test build"},
		{name: "npm test", tool: "bash", command: "npm test"},
		{name: "npm run build", tool: "bash", command: "npm run build"},
		{name: "npm run type check", tool: "bash", command: "npm run type-check"},
		{name: "pnpm lint", tool: "bash", command: "pnpm lint"},
		{name: "pnpm run typecheck", tool: "bash", command: "pnpm run typecheck"},
		{name: "yarn test", tool: "bash", command: "yarn test"},
		{name: "yarn run build", tool: "bash", command: "yarn run build"},
		{name: "bun test", tool: "bash", command: "bun test"},
		{name: "bun run lint", tool: "bash", command: "bun run lint"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			arguments := `{"command":` + quotedJSON(t, tc.command) + `,"description":"redacted event"}`
			if !isValidationCommand(tc.tool, arguments) {
				t.Fatalf("isValidationCommand(%q, %q) = false, want true", tc.tool, arguments)
			}
		})
	}
}

func TestIsValidationCommandRejectsUnsafeOrAmbiguousCommands(t *testing.T) {
	tests := []struct {
		name      string
		tool      string
		arguments string
	}{
		{name: "unknown tool", tool: "shell", arguments: `{"command":"go test ./..."}`},
		{name: "empty tool", arguments: `{"command":"go test ./..."}`},
		{name: "malformed JSON", tool: "bash", arguments: `{"command":"go test ./..."`},
		{name: "trailing JSON", tool: "bash", arguments: `{"command":"go test"} true`},
		{name: "null JSON", tool: "bash", arguments: `null`},
		{name: "array JSON", tool: "bash", arguments: `["go test"]`},
		{name: "missing command", tool: "bash", arguments: `{"script":"go test ./..."}`},
		{name: "nested command", tool: "bash", arguments: `{"command":{"command":"go test ./..."}}`},
		{name: "non-string command", tool: "bash", arguments: `{"command":["go","test"]}`},
		{name: "empty command", tool: "bash", arguments: `{"command":"   "}`},
		{name: "ordinary command", tool: "bash", arguments: `{"command":"echo go test"}`},
		{name: "executable path", tool: "bash", arguments: `{"command":"/usr/bin/go test ./..."}`},
		{name: "environment prefix", tool: "bash", arguments: `{"command":"CI=1 go test ./..."}`},
		{name: "sudo wrapper", tool: "bash", arguments: `{"command":"sudo go test ./..."}`},
		{name: "env wrapper", tool: "bash", arguments: `{"command":"env CI=1 go test ./..."}`},
		{name: "time wrapper", tool: "bash", arguments: `{"command":"time go test ./..."}`},
		{name: "shell wrapper", tool: "bash", arguments: `{"command":"sh -c 'go test ./...'"}`},
		{name: "command wrapper", tool: "bash", arguments: `{"command":"command go test ./..."}`},
		{name: "empty executable", tool: "bash", arguments: `{"command":"'' go test ./..."}`},
		{name: "go global option", tool: "bash", arguments: `{"command":"go -C internal/ui test"}`},
		{name: "pipeline", tool: "bash", arguments: `{"command":"go test ./... | tee results.txt"}`},
		{name: "and compound", tool: "bash", arguments: `{"command":"go test ./... && echo done"}`},
		{name: "or compound", tool: "bash", arguments: `{"command":"go test ./... || true"}`},
		{name: "semicolon compound", tool: "bash", arguments: `{"command":"go test ./...; echo done"}`},
		{name: "newline compound", tool: "bash", arguments: "{\"command\":\"go test ./...\\necho done\"}"},
		{name: "output redirection", tool: "bash", arguments: `{"command":"go test ./... > results.txt"}`},
		{name: "input redirection", tool: "bash", arguments: `{"command":"go test ./... < input.txt"}`},
		{name: "backgrounding", tool: "bash", arguments: `{"command":"go test ./... &"}`},
		{name: "command substitution", tool: "bash", arguments: `{"command":"go test $(go list ./...)"}`},
		{name: "backtick substitution", tool: "bash", arguments: "{\"command\":\"go test `go list ./...`\"}"},
		{name: "variable expansion", tool: "bash", arguments: `{"command":"go test $PACKAGES"}`},
		{name: "comment suffix", tool: "bash", arguments: `{"command":"go test ./... # validation"}`},
		{name: "escaped executable", tool: "bash", arguments: `{"command":"g\\o test ./..."}`},
		{name: "unterminated quote", tool: "bash", arguments: `{"command":"go test ' ./..."}`},
		{name: "go build", tool: "bash", arguments: `{"command":"go build ./..."}`},
		{name: "gofmt write", tool: "bash", arguments: `{"command":"gofmt -w internal/ui"}`},
		{name: "gofmt list and write", tool: "bash", arguments: `{"command":"gofmt -l -w internal/ui"}`},
		{name: "make unknown target", tool: "bash", arguments: `{"command":"make deploy"}`},
		{name: "make assignment", tool: "bash", arguments: `{"command":"make MODE=test build"}`},
		{name: "npm bare build", tool: "bash", arguments: `{"command":"npm build"}`},
		{name: "npm exec wrapper", tool: "bash", arguments: `{"command":"npm exec tsc --noEmit"}`},
		{name: "package install", tool: "bash", arguments: `{"command":"pnpm install"}`},
		{name: "unknown package script", tool: "bash", arguments: `{"command":"yarn deploy"}`},
		{name: "script name suffix", tool: "bash", arguments: `{"command":"npm run test:unit"}`},
		{name: "lint fix", tool: "bash", arguments: `{"command":"npm run lint -- --fix"}`},
		{name: "formatter write", tool: "bash", arguments: `{"command":"pnpm lint --write"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if isValidationCommand(tc.tool, tc.arguments) {
				t.Fatalf("isValidationCommand(%q, %q) = true, want false", tc.tool, tc.arguments)
			}
		})
	}
}

func quotedJSON(t *testing.T, value string) string {
	t.Helper()
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(result)
}
