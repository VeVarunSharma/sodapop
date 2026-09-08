package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/charmbracelet/x/ansi"
)

type contextBucket struct {
	glyph, ascii, label string
	tokens              int64
}

func (m *Model) renderContextUsage(usage engine.ContextUsage, width int) string {
	buckets := []contextBucket{
		{glyph: "○", ascii: "S", label: "System Prompt", tokens: usage.SystemPrompt.Tokens},
	}
	if usage.CustomInstructions.Tokens > 0 {
		buckets = append(buckets, contextBucket{
			glyph: "○", ascii: "I", label: "Custom Instructions", tokens: usage.CustomInstructions.Tokens,
		})
	}
	buckets = append(buckets,
		contextBucket{glyph: "◌", ascii: "T", label: "System Tools", tokens: usage.SystemTools.Tokens},
		contextBucket{glyph: "●", ascii: "M", label: "MCP Tools", tokens: usage.MCPTools.Tokens},
		contextBucket{glyph: "◉", ascii: "C", label: "Messages", tokens: usage.Messages.Tokens},
		contextBucket{glyph: "·", ascii: ".", label: "Free Space", tokens: usage.FreeSpace.Tokens},
		contextBucket{glyph: "◎", ascii: "B", label: "Buffer", tokens: usage.Buffer.Tokens},
	)

	gridBuckets := buckets
	if usage.CustomInstructions.Tokens > 0 {
		gridBuckets = append([]contextBucket(nil), buckets...)
		gridBuckets[0].tokens += usage.CustomInstructions.Tokens
		gridBuckets = append(gridBuckets[:1], gridBuckets[2:]...)
	}
	rows := len(buckets) + 1
	grid := contextGrid(gridBuckets, usage.Limit, rows, m.prefs.ASCII)
	header := fmt.Sprintf("%s · %s/%s tokens (%s)",
		singleLine(usage.Model), formatContextTokensRounded(usage.TotalTokens),
		formatContextTokensRounded(usage.Limit), formatContextPercent(usage.TotalTokens, usage.Limit, false))
	lines := []string{m.color.paint(m.color.cyan, "Context Usage"), ""}

	gridWidth := ansi.StringWidth(grid[0])
	legendWidth := 1 + 1 + 20 + 1 + 7 + 1 + 6
	if width >= gridWidth+3+legendWidth {
		lines = append(lines, grid[0]+"   "+header)
		for i, bucket := range buckets {
			lines = append(lines, grid[i+1]+"   "+m.contextLegendRow(bucket, usage.Limit))
		}
	} else {
		lines = append(lines, header)
		lines = append(lines, grid...)
		for _, bucket := range buckets {
			lines = append(lines, m.contextLegendRow(bucket, usage.Limit))
		}
	}
	if detail := m.renderContextDetails(usage, width); detail != "" {
		lines = append(lines, "", detail)
	}
	for _, warning := range usage.Warnings {
		lines = append(lines, "", m.color.paint(m.color.amber, warning))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) contextLegendRow(bucket contextBucket, limit int64) string {
	glyph := bucket.glyph
	if m.prefs.ASCII {
		glyph = bucket.ascii
	}
	return fmt.Sprintf("%s %-20s %7s %6s", glyph, bucket.label,
		formatContextTokens(bucket.tokens), "("+formatContextPercent(bucket.tokens, limit, true)+")")
}

func contextGrid(buckets []contextBucket, limit int64, rows int, ascii bool) []string {
	const columns = 10
	total := columns * rows
	counts := allocateContextCells(buckets, limit, total)
	cells := make([]string, 0, total)
	for i, bucket := range buckets {
		glyph := bucket.glyph
		if ascii {
			glyph = bucket.ascii
		}
		for range counts[i] {
			cells = append(cells, glyph)
		}
	}
	filler := "·"
	if ascii {
		filler = "."
	}
	for len(cells) < total {
		cells = append(cells, filler)
	}
	cells = cells[:total]
	result := make([]string, 0, rows)
	for row := 0; row < rows; row++ {
		result = append(result, strings.Join(cells[row*columns:(row+1)*columns], " "))
	}
	return result
}

func allocateContextCells(buckets []contextBucket, limit int64, total int) []int {
	counts := make([]int, len(buckets))
	allocated := 0
	for i, bucket := range buckets {
		if bucket.tokens <= 0 || limit <= 0 {
			continue
		}
		exact := float64(bucket.tokens) / float64(limit) * float64(total)
		counts[i] = max(1, min(total, int(exact+0.5)))
		allocated += counts[i]
	}
	for allocated > total {
		chosen := -1
		for i, bucket := range buckets {
			if counts[i] <= 1 || bucket.tokens <= 0 {
				continue
			}
			if chosen == -1 || counts[i] > counts[chosen] ||
				(counts[i] == counts[chosen] && bucket.tokens > buckets[chosen].tokens) {
				chosen = i
			}
		}
		if chosen == -1 {
			break
		}
		counts[chosen]--
		allocated--
	}
	free := -1
	for i, bucket := range buckets {
		if bucket.label == "Free Space" && bucket.tokens > 0 {
			free = i
			break
		}
	}
	for allocated < total {
		chosen := free
		if chosen == -1 {
			for i, bucket := range buckets {
				if bucket.tokens > 0 && (chosen == -1 || bucket.tokens > buckets[chosen].tokens) {
					chosen = i
				}
			}
		}
		if chosen == -1 {
			break
		}
		counts[chosen]++
		allocated++
	}
	return counts
}

func formatContextTokens(tokens int64) string {
	switch {
	case tokens >= 1_000_000:
		return strings.TrimSuffix(strconv.FormatFloat(float64(tokens)/1_000_000, 'f', 1, 64), ".0") + "m"
	case tokens >= 1_000:
		value := strconv.FormatFloat(float64(tokens)/1_000, 'f', 1, 64)
		return strings.TrimSuffix(value, ".0") + "k"
	default:
		return strconv.FormatInt(tokens, 10)
	}
}

func formatContextTokensRounded(tokens int64) string {
	switch {
	case tokens >= 1_000_000:
		return strconv.FormatInt((tokens+500_000)/1_000_000, 10) + "m"
	case tokens >= 1_000:
		return strconv.FormatInt((tokens+500)/1_000, 10) + "k"
	default:
		return strconv.FormatInt(tokens, 10)
	}
}

func formatContextPercent(tokens, limit int64, threshold bool) string {
	if tokens <= 0 || limit <= 0 {
		return "0%"
	}
	percent := float64(tokens) / float64(limit) * 100
	if threshold && percent < 1 {
		return "<1%"
	}
	return strconv.FormatInt(int64(percent+0.5), 10) + "%"
}

func (m *Model) renderContextDetails(usage engine.ContextUsage, width int) string {
	var sections []string
	for _, spec := range []struct {
		kind, title string
	}{
		{"skill", "Skills"},
		{"subagent", "Subagents"},
		{"mcpServer", "MCP servers"},
		{"tool", "Tools"},
		{"plugin", "Plugins"},
	} {
		rows := contextEntriesByKind(usage.Entries, spec.kind)
		if len(rows) > 0 {
			sections = append(sections, m.contextDetailSection(spec.title, rows, usage.TotalTokens, width))
		}
	}
	system := contextEntriesByKind(usage.Entries, "system")
	definitions := contextEntriesByKind(usage.Entries, "toolDefinition")
	if len(system)+len(definitions) > 0 {
		rows := append([]engine.ContextEntry(nil), system...)
		rows = append(rows, definitions...)
		sections = append(sections, m.contextDetailSection("System breakdown", rows, usage.TotalTokens, width))
	}
	if len(usage.HeaviestMessages) > 0 {
		rows := make([]engine.ContextEntry, 0, len(usage.HeaviestMessages))
		for _, message := range usage.HeaviestMessages {
			rows = append(rows, engine.ContextEntry{Label: message.Label, Tokens: message.Tokens})
		}
		sections = append(sections, m.contextDetailSection("Heaviest messages", rows, usage.TotalTokens, width))
	}
	if usage.Compactions > 0 {
		label := "compactions"
		if usage.Compactions == 1 {
			label = "compaction"
		}
		sections = append(sections, fmt.Sprintf("Compactions: %d successful %s", usage.Compactions, label))
	}
	return strings.Join(sections, "\n\n")
}

func contextEntriesByKind(entries []engine.ContextEntry, kind string) []engine.ContextEntry {
	var result []engine.ContextEntry
	for _, entry := range entries {
		if entry.Kind == kind && entry.Tokens > 0 {
			result = append(result, entry)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Tokens > result[j].Tokens })
	return result
}

func (m *Model) contextDetailSection(title string, rows []engine.ContextEntry, total int64, width int) string {
	const barWidth = 10
	shown := min(5, len(rows))
	nameWidth := 12
	for _, row := range rows[:shown] {
		nameWidth = max(nameWidth, min(36, ansi.StringWidth(singleLine(row.Label))))
	}
	nameWidth = min(nameWidth, max(8, width-31))
	lines := []string{m.color.paint(m.color.cyan, title)}
	header := fmt.Sprintf("%-*s %7s  %-10s %s", nameWidth, "", "tokens", "", "% of used context")
	lines = append(lines, m.color.paint(m.color.muted, clip(header, width)))
	for _, row := range rows[:shown] {
		label := clip(singleLine(row.Label), nameWidth)
		filled := 0
		if total > 0 {
			filled = min(barWidth, int(float64(row.Tokens)/float64(total)*barWidth+0.5))
		}
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		if m.prefs.ASCII {
			bar = strings.Repeat("#", filled) + strings.Repeat(".", barWidth-filled)
		}
		line := fmt.Sprintf("%-*s %7s  %s %6s", nameWidth, label,
			formatContextTokens(row.Tokens), bar, formatContextPercent(row.Tokens, total, true))
		lines = append(lines, clip(line, width))
	}
	if len(rows) > shown {
		lines = append(lines, fmt.Sprintf("… and %d more", len(rows)-shown))
	}
	return strings.Join(lines, "\n")
}
