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
	{Name: "help", Summary: "Discover commands and keyboard shortcuts", Usage: "/help [command]", AllowedWhileBusy: true},
	{Name: "login", Summary: "Sign in to GitHub or reconnect Copilot", Usage: "/login", VendingCategory: "Connect & Extend"},
	{Name: "logout", Summary: "Sign out of GitHub while keeping history", Usage: "/logout", AllowedWhileBusy: true},
	{Name: "model", Summary: "Choose an available Copilot model", Usage: "/model [id]", VendingCategory: "Connect & Extend"},
	{Name: "clear", Summary: "Abandon this conversation and start fresh", Usage: "/clear"},
	{Name: "resume", Summary: "Resume this account's project conversation", Usage: "/resume [id]"},
	{Name: "context", Summary: "Show context window token usage and visualization", Usage: "/context", AllowedWhileBusy: true, VendingCategory: "Inspect & Validate"},
	{Name: "compact", Summary: "Summarize conversation context to free space", Usage: "/compact [focus instructions]"},
	{Name: "plan", Summary: "Advisory planning; normal tool approvals still apply", Usage: "/plan [prompt] | /plan off", VendingCategory: "Create"},
	{Name: "fizz", Summary: "Burst into ideas, trade-offs, and a recommended direction", Usage: "/fizz [topic]", VendingCategory: "Create"},
	{Name: "taste-test", Summary: "Validate conversation changes and report the evidence", Usage: "/taste-test [focus]", VendingCategory: "Inspect & Validate"},
	{Name: "vending-machine", Summary: "Browse curated Sodapop workflows and experiences", Usage: "/vending-machine", AllowedWhileBusy: true},
	{Name: "autopilot", Summary: "Automatically approve tool requests in this conversation", Usage: "/autopilot | /autopilot off", AllowedWhileBusy: true},
	{Name: "mcp", Summary: "Manage explicit account-scoped MCP servers", Usage: "/mcp [add|enable|disable|remove|reconnect] ...", VendingCategory: "Connect & Extend"},
	{Name: "skill", Summary: "Manage explicit project skills", Usage: "/skill [trust|add|enable|disable|remove] ...", VendingCategory: "Connect & Extend"},
	{Name: "diff", Summary: "Inspect working-tree or conversation changes", Usage: "/diff [all|staged|unstaged|session]", AllowedWhileBusy: true, VendingCategory: "Inspect & Validate"},
	{Name: "theme", Summary: "Appearance, personality, contrast, and motion preferences", Usage: "/theme [name]", AllowedWhileBusy: true, VendingCategory: "Customize"},
	{Name: "exit", Summary: "Exit gracefully, confirming interruption if necessary", Usage: "/exit", AllowedWhileBusy: true},
}

// All returns independent copies in the registry's display order.
func All() []Command {
	return append([]Command(nil), catalog...)
}

// Vending returns curated commands grouped for the vending-machine launcher.
func Vending() []Command {
	categories := []string{"Create", "Inspect & Validate", "Customize", "Connect & Extend"}
	result := make([]Command, 0, len(catalog))
	for _, category := range categories {
		for _, command := range catalog {
			if command.VendingCategory == category {
				result = append(result, command)
			}
		}
	}
	return result
}

// Lookup accepts a case-insensitive command name, with or without its slash.
func Lookup(name string) (Command, bool) {
	name = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/"))
	if name == "allow-all" {
		name = "autopilot"
	}
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
	if (command.Name == "login" || command.Name == "logout" || command.Name == "clear" || command.Name == "context" || command.Name == "vending-machine" || command.Name == "exit") && args != "" {
		return input, fmt.Errorf("usage: %s", command.Usage)
	}
	if command.Name != "plan" && command.Name != "compact" && command.Name != "fizz" && command.Name != "taste-test" && command.Name != "mcp" && command.Name != "skill" && len(strings.Fields(args)) > 1 {
		return input, fmt.Errorf("usage: %s", command.Usage)
	}
	if command.Name == "diff" {
		args = strings.ToLower(args)
	}
	if command.Name == "diff" && args != "" && args != "all" && args != "staged" && args != "unstaged" && args != "session" {
		return input, fmt.Errorf("usage: %s", command.Usage)
	}
	if command.Name == "autopilot" && args != "" && !strings.EqualFold(args, "off") {
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
	if (command.Name == "compact" || command.Name == "fizz" || command.Name == "taste-test") && args != "" {
		args = rawArgs
	}
	if command.Name == "autopilot" && strings.EqualFold(args, "off") {
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

KEYBOARD AND MOUSE
Enter: send or choose   Ctrl+J / Shift+Enter / Alt+Enter: insert a newline
Tab / Shift+Tab / arrows: complete or navigate the active menu
Shift+Tab in the composer: cycle Chat -> Plan -> Autopilot
Esc / Escape: close a menu or return focus
Ctrl+C: cancel active work; completed edits remain
Ctrl+Q: request exit; a nonempty draft is protected
Ctrl+P: all local actions   F1: help   F2: toggle Autopilot
F3: focus sidebar when visible   F4: focus tool cards
PgUp / PgDown or Alt+Up / Alt+Down: scroll conversation history
Ctrl+Home / Ctrl+End: jump to the oldest / current output
Mouse wheel: scroll conversation, sidebar, or dialog details
Shift+drag: select text for your terminal's copy command

Planning is advisory, not read-only, and is not a safety boundary. Normal edit,
shell, and external-access approvals still apply.

Autopilot approves tool requests without per-action prompts for the current
conversation. Use it only when you trust the requested work and understand that
commands are not sandboxed. Agent questions still require your answer.

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
		if command.Name == "login" {
			text += "\n\nGitHub sign-in and Copilot access are checked separately. If the GitHub account is retained but Copilot is unavailable, this dialog offers recovery actions without replaying a failed prompt."
		}
		if command.Name == "logout" {
			text += "\n\nSigning out removes Sodapop's saved credential and disconnects Copilot. Conversation history and completed working-tree changes are kept."
		}
		if command.Name == "model" {
			text += "\n\nThe picker shows Model, Context, and Thinking columns. Use Up/Down to choose a model, Tab or Shift+Tab to change context size, Left/Right to change thinking effort, Enter to apply every setting together, and Esc to discard the draft."
		}
		if command.Name == "clear" {
			text += "\n\nA fresh conversation starts only when you send its first prompt. Previous history remains available through /resume."
		}
		if command.Name == "resume" {
			text += "\n\nOnly conversations for the signed-in account and canonical project are listed. Resume is explicit and restores normal approval prompts by turning Autopilot off."
		}
		if command.Name == "context" {
			text += "\n\nContext usage becomes available after a conversation starts and only when the selected model exposes a fixed context window."
		}
		if command.Name == "autopilot" {
			text += "\n\nAutopilot approves every tool request in the current conversation without another prompt. It does not answer agent questions and turns off when you start or resume a conversation. The legacy /allow-all command remains an alias."
		}
		if command.Name == "compact" {
			text += "\n\nCompaction uses the current model to summarize older context. It may consume model tokens, keeps the visible transcript, and accepts optional instructions describing what the summary should preserve."
		}
		if command.Name == "diff" {
			text += "\n\n/diff session compares tracked files and newly untracked paths with the current conversation baseline. It reports observed changes and does not infer whether Sodapop, you, or another process made them."
		}
		if command.Name == "mcp" {
			text += "\n\nMCP servers are never imported from ambient Copilot configuration. Add uses /mcp add <name> <JSON>; JSON contains command and optional args, env, tools, and timeout_seconds. Environment entries are variable names, never secret values. Run /mcp reconnect after changes. Every MCP tool call still follows normal approvals."
		}
		if command.Name == "skill" {
			text += "\n\nSkills use Copilot's native SKILL.md format. Trust a local root or public Git repository before installing. Installed content is copied into immutable Sodapop state, and enablement applies only to the current project. Enabling or disabling starts a fresh conversation."
		}
		if command.Name == "vending-machine" {
			text += "\n\nThe vending machine is a curated launcher for built-in workflows and settings. Ctrl+P remains the complete local action palette."
		}
		if command.Name == "fizz" {
			text += "\n\nFizz sends a one-shot ideation prompt to the current model and asks it not to implement. This is prompt guidance, not a read-only safety boundary; normal approvals and Autopilot still apply."
		}
		if command.Name == "taste-test" {
			text += "\n\nTaste test gathers bounded conversation-change evidence, asks the model to run focused checks through normal approvals, and requests a report without edits. Missing or partial baseline evidence is disclosed rather than silently replaced with the whole working tree."
		}
		if command.Name == "theme" {
			text += "\n\nOpen the picker for themes, Quiet/Playful/Extra personality, motion, color, and ASCII or Unicode settings. These presentation choices do not change model capability or approval policy."
		}
		return text + helpFooter, nil
	}
	var out strings.Builder
	out.WriteString("SODAPOP COMMANDS\n\n")
	usageWidth := 0
	for _, command := range catalog {
		usageWidth = max(usageWidth, len(command.Usage))
	}
	for _, command := range catalog {
		fmt.Fprintf(&out, "%-*s  %s\n", usageWidth, command.Usage, command.Summary)
	}
	out.WriteString("\nHelp, context, theme, diff, the vending machine, Autopilot, logout, and exit\nremain available while busy. Curated vending selections retain their own busy-state rules.\nFinish or cancel active work before model workflows, login, conversation, compaction,\nskill, MCP, or planning changes.\n")
	out.WriteString(helpFooter)
	return out.String(), nil
}
