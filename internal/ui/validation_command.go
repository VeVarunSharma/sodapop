package ui

import (
	"encoding/json"
	"strings"
	"unicode"
)

var validationScripts = map[string]bool{
	"build":      true,
	"lint":       true,
	"test":       true,
	"type-check": true,
	"typecheck":  true,
}

func isValidationCommand(toolName, arguments string) bool {
	if toolName != "bash" {
		return false
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &fields); err != nil || fields == nil {
		return false
	}
	raw, ok := fields["command"]
	if !ok {
		return false
	}
	var command string
	if err := json.Unmarshal(raw, &command); err != nil {
		return false
	}

	words, ok := validationCommandWords(command)
	if !ok || len(words) < 2 {
		return false
	}
	switch words[0] {
	case "go":
		return words[1] == "test" || words[1] == "vet"
	case "gofmt":
		return gofmtListsFiles(words[1:])
	case "make":
		return makeValidationTargets(words[1:])
	case "npm":
		return packageManagerValidation(words[1:], true)
	case "pnpm", "yarn", "bun":
		return packageManagerValidation(words[1:], false)
	default:
		return false
	}
}

func validationCommandWords(command string) ([]string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, false
	}

	var words []string
	var word strings.Builder
	var quote rune
	wordStarted := false
	for _, r := range command {
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			if unsafeValidationCommandRune(r) {
				return nil, false
			}
			word.WriteRune(r)
			continue
		}
		switch {
		case unsafeValidationCommandRune(r):
			return nil, false
		case r == '\'' || r == '"':
			quote = r
			wordStarted = true
		case unicode.IsSpace(r):
			if wordStarted {
				words = append(words, word.String())
				word.Reset()
				wordStarted = false
			}
		default:
			word.WriteRune(r)
			wordStarted = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	if wordStarted {
		words = append(words, word.String())
	}
	return words, len(words) > 0
}

func unsafeValidationCommandRune(r rune) bool {
	switch r {
	case '\n', '\r', ';', '|', '&', '<', '>', '`', '$', '\\', '#', '(', ')':
		return true
	default:
		return unicode.IsControl(r)
	}
}

func gofmtListsFiles(arguments []string) bool {
	if len(arguments) == 0 || arguments[0] != "-l" {
		return false
	}
	for _, argument := range arguments[1:] {
		if strings.HasPrefix(argument, "-") {
			return false
		}
	}
	return true
}

func makeValidationTargets(arguments []string) bool {
	targets := 0
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "-") {
			continue
		}
		switch argument {
		case "test", "check", "coverage", "build":
			targets++
		default:
			return false
		}
	}
	return targets > 0
}

func packageManagerValidation(arguments []string, requireRun bool) bool {
	if len(arguments) == 0 {
		return false
	}
	if arguments[0] == "run" {
		arguments = arguments[1:]
	} else if requireRun && arguments[0] != "test" {
		return false
	}
	if len(arguments) == 0 || !validationScripts[arguments[0]] {
		return false
	}
	for _, argument := range arguments[1:] {
		if argument == "--fix" || argument == "--write" {
			return false
		}
	}
	return true
}
