// Package commands provides Sodapop's local command grammar and palette metadata.
// It never invokes a model, an API, or a command handler.
package commands

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var catalog = []Command{
	{"help", "Discover commands and keyboard shortcuts", "/help [command]", true},
	{"login", "Sign in to GitHub or reconnect Copilot", "/login", false},
	{"logout", "Sign out of GitHub while keeping history", "/logout", true},
	{"model", "Choose an available Copilot model", "/model [id]", false},
	{"clear", "Abandon this conversation and start fresh", "/clear", false},
	{"resume", "Resume this account's project conversation", "/resume [id]", false},
	{"context", "Show context window token usage and visualization", "/context", true},
	{"compact", "Summarize conversation context to free space", "/compact [focus instructions]", false},
	{"plan", "Advisory planning; normal tool approvals still apply", "/plan [prompt] | /plan off", false},
	{"allow-all", "Allow every tool request in this conversation", "/allow-all | /allow-all off", true},
	{"diff", "Inspect the whole working tree without modifying it", "/diff [all|staged|unstaged]", true},
	{"theme", "Appearance, personality, contrast, and motion preferences", "/theme [name]", true},
	{"exit", "Exit gracefully, confirming interruption if necessary", "/exit", true},
}

// All returns independent copies in the registry's display order.
func All() []Command {
	return append([]Command(nil), catalog...)
}

// Lookup accepts a case-insensitive command name, with or without its slash.
func Lookup(name string) (Command, bool) {
	name = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/"))
	for _, command := range catalog {
		if name == command.Name {
			return command, true
		}
	}
	return Command{}, false
}

// Match ranks name prefixes, fuzzy subsequences, and small spelling mistakes.
func Match(query string) []Command {
	query = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(query), "/"))
	if i := strings.IndexFunc(query, unicode.IsSpace); i >= 0 {
		query = query[:i]
	}
	type match struct {
		command Command
		score   int
	}
	var matches []match
	for _, command := range catalog {
		score := fuzzyScore(query, command.Name)
		if score >= 0 {
			matches = append(matches, match{command, score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	result := make([]Command, len(matches))
	for i, match := range matches {
		result[i] = match.command
	}
	return result
}

func fuzzyScore(query, text string) int {
	if query == "" {
		return 0
	}
	if text == query {
		return 200
	}
	if strings.HasPrefix(text, query) {
		return 100 - len(text)
	}
	at := 0
	for _, character := range text {
		if at < len(query) && character == rune(query[at]) {
			at++
		}
	}
	if at == len(query) {
		return 50 - len(text)
	}
	if len(query) >= 3 && len(query) <= len(text)+2 && len(text) <= len(query)+2 {
		limit := 1
		if len(query) >= 4 {
			limit = 2
		}
		if distance := editDistance(query, text); distance <= limit {
			return 10 - distance
		}
	}
	return -1
}

func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for i := range previous {
		previous[i] = i
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}

// Parse recognizes commands only at byte zero. Ordinary prompts are preserved,
// and // removes exactly one leading slash. Freeform /plan and /compact input
// starts after one separating whitespace rune; additional whitespace is kept.
func Parse(text string) (Input, error) {
	if strings.TrimSpace(text) == "" {
		return Input{}, errors.New("enter a message or type / for commands")
	}
	if strings.HasPrefix(text, "//") {
		return Input{Text: text[1:]}, nil
	}
	if !strings.HasPrefix(text, "/") {
		return Input{Text: text}, nil
	}
	word, args, rawArgs := text[1:], "", ""
	if index := strings.IndexFunc(word, unicode.IsSpace); index >= 0 {
		_, width := utf8.DecodeRuneInString(word[index:])
		word, rawArgs = word[:index], word[index+width:]
		args = strings.TrimSpace(rawArgs)
	}
	input := Input{IsCommand: true, Command: strings.ToLower(word), Args: args}
	command, ok := Lookup(word)
	if !ok {
		return input, unknownCommand(word)
	}
	if (command.Name == "login" || command.Name == "logout" || command.Name == "clear" || command.Name == "context" || command.Name == "exit") && args != "" {
		return input, fmt.Errorf("usage: %s", command.Usage)
	}
	if command.Name != "plan" && command.Name != "compact" && len(strings.Fields(args)) > 1 {
		return input, fmt.Errorf("usage: %s", command.Usage)
	}
	if command.Name == "diff" {
		args = strings.ToLower(args)
	}
	if command.Name == "diff" && args != "" && args != "all" && args != "staged" && args != "unstaged" {
		return input, fmt.Errorf("usage: %s", command.Usage)
	}
	if command.Name == "allow-all" && args != "" && !strings.EqualFold(args, "off") {
		return input, fmt.Errorf("usage: %s", command.Usage)
	}
	if command.Name == "help" && args != "" {
		if _, ok := Lookup(args); !ok {
			return input, unknownCommand(args)
		}
	}
	if command.Name == "plan" && args != "" {
		if strings.EqualFold(args, "off") {
			args = "off"
		} else {
			args = rawArgs
		}
	}
	if command.Name == "compact" && args != "" {
		args = rawArgs
	}
	if command.Name == "allow-all" && strings.EqualFold(args, "off") {
		args = "off"
	}
	return Input{IsCommand: true, Command: command.Name, Args: args, Text: text}, nil
}

func unknownCommand(name string) error {
	name = strings.TrimPrefix(strings.TrimSpace(name), "/")
	matches := Match(name)
	if len(matches) > 0 && name != "" {
		return fmt.Errorf("unknown command %q; did you mean /%s? Use /help for commands or // for a literal slash", "/"+name, matches[0].Name)
	}
	return errors.New("unknown command; type /help to discover commands, or use // for a literal slash")
}

const helpFooter = `

KEYS
Enter: send   Ctrl+J: multiline newline   Tab: complete   Arrows: navigate
Mouse wheel: scroll conversation or dialog details
Esc / Escape: close menu   Ctrl+C: cancel active work
Ctrl+P: local action palette
F3: focus sidebar when visible   F4: focus tool cards

Planning is advisory, not read-only, and is not a safety boundary. Normal edit,
shell, and external-access approvals still apply.

Allow-all removes those per-action prompts for the current conversation. Use it
only when you trust the requested work and understand that commands are not sandboxed.

Commands start only at the very beginning of the composer. Use // to send a
literal leading slash: //path sends /path. Slashes later in a prompt, on later
lines, or inside pasted code are untouched. Cancellation does not undo edits.
`

// Help uses the same registry as parsing and completion and works without login.
func Help(name string) (string, error) {
	if strings.TrimSpace(name) != "" {
		command, ok := Lookup(name)
		if !ok {
			return "", unknownCommand(name)
		}
		text := command.Usage + "\n\n" + command.Summary
		if command.Name == "plan" {
			text += "\n\nPlanning is advisory, not read-only. Edits and commands still require normal approval."
		}
		if command.Name == "allow-all" {
			text += "\n\nAllow-all approves every tool request in the current conversation without another prompt. It does not answer agent questions and turns off when you start or resume a conversation."
		}
		if command.Name == "compact" {
			text += "\n\nCompaction uses the current model to summarize older context. It may consume model tokens, keeps the visible transcript, and accepts optional instructions describing what the summary should preserve."
		}
		return text + helpFooter, nil
	}
	var out strings.Builder
	out.WriteString("SODAPOP COMMANDS\n\n")
	for _, command := range catalog {
		fmt.Fprintf(&out, "%-30s %s\n", command.Usage, command.Summary)
	}
	out.WriteString("\nHelp, theme, diff, logout, and exit can open local UI while busy. Finish or\ncancel active work before login, model, conversation, compaction, or planning changes.\n")
	out.WriteString(helpFooter)
	return out.String(), nil
}
