package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type reactionKind string

const (
	reactionSuccess   reactionKind = "success"
	reactionRecovery  reactionKind = "recovery"
	reactionCancelled reactionKind = "cancelled"
)

type transientMoment struct {
	id   uint64
	kind reactionKind
	text string
}

type toolOutcome struct {
	validation bool
	completed  bool
	failed     bool
}

type turnExperience struct {
	turn       uint64
	tools      map[string]toolOutcome
	errored    bool
	aborted    bool
	celebrated bool
}

type momentExpiredMsg struct{ id uint64 }

func (m *Model) personality() string {
	switch m.prefs.Personality {
	case "quiet", "extra":
		return m.prefs.Personality
	default:
		return "playful"
	}
}

func (m *Model) activityCopy(label string) string {
	switch m.personality() {
	case "quiet":
		return label
	case "extra":
		switch {
		case label == "Starting conversation":
			return label + " / popping the cap"
		case label == "Sending":
			return label + " / pouring the prompt"
		case label == "Generating":
			return label + " / bubbles are rising"
		case strings.HasPrefix(label, "Running "):
			return label + " / mixing carefully"
		default:
			return label + " / building extra bubbles"
		}
	default:
		switch {
		case label == "Starting conversation":
			return label + " / uncapping"
		case label == "Sending":
			return label + " / pouring"
		case label == "Generating":
			return label + " / carbonating"
		case strings.HasPrefix(label, "Running "):
			return label + " / mixing"
		default:
			return label + " / building bubbles"
		}
	}
}

func (m *Model) startMoment(kind reactionKind) tea.Cmd {
	text := ""
	switch kind {
	case reactionSuccess:
		switch m.personality() {
		case "quiet":
			text = "Validation completed successfully."
		case "extra":
			text = "Perfect pour! Every observed check came up sparkling."
		default:
			text = "Perfect pour - validation passed."
		}
	case reactionRecovery:
		if m.personality() != "quiet" {
			text = "A few bubbles escaped - recovery guidance is above."
		}
	case reactionCancelled:
		if m.personality() != "quiet" {
			text = "Bubbles settled - the turn is cancelled."
		}
	}
	if text == "" {
		return nil
	}
	m.momentSequence++
	m.moment = transientMoment{id: m.momentSequence, kind: kind, text: text}
	m.renderDirty = true
	id := m.moment.id
	return tea.Tick(1600*time.Millisecond, func(time.Time) tea.Msg {
		return momentExpiredMsg{id: id}
	})
}

func (m *Model) recoveryCopy(text string) string {
	switch m.personality() {
	case "quiet":
		return text
	case "extra":
		return "Sodapop lost a few bubbles. " + text
	default:
		return "That went a little flat. " + text
	}
}

func (m *Model) reactionGlyph() string {
	switch m.moment.kind {
	case reactionSuccess:
		return m.color.glyph("✦", "*")
	case reactionRecovery:
		return m.color.glyph("◌", "o")
	case reactionCancelled:
		return m.color.glyph("○", "o")
	default:
		return m.color.glyph("·", ".")
	}
}

func (m *Model) clearMoment() {
	if m.moment.id == 0 {
		return
	}
	m.moment = transientMoment{}
	m.renderDirty = true
}

func (m *Model) momentView(width int) string {
	if m.moment.id == 0 {
		return ""
	}
	glyph := m.color.glyph("○", "o")
	if !m.prefs.ReducedMotion {
		var frames, asciiFrames []string
		switch m.moment.kind {
		case reactionSuccess:
			frames = []string{"·  ○  ●  ○  ·", "○  ●  ✦  ●  ○", "·  ○  ●  ○  ·", "·  ·  ○  ·  ·"}
			asciiFrames = []string{".  o  O  o  .", "o  O  *  O  o", ".  o  O  o  .", ".  .  o  .  ."}
		case reactionRecovery:
			frames = []string{"●  ○  ·", "○  ·  ·", "·  ·  ·", "○  ·  ·"}
			asciiFrames = []string{"O  o  .", "o  .  .", ".  .  .", "o  .  ."}
		default:
			frames = []string{"●  ○  ·", "○  ·  ○", "·  ○  ·", "·  ·  ·"}
			asciiFrames = []string{"O  o  .", "o  .  o", ".  o  .", ".  .  ."}
		}
		index := (m.frame / 3) % len(frames)
		glyph = m.color.glyph(frames[index], asciiFrames[index])
	}
	accent := m.color.magenta
	if m.moment.kind == reactionSuccess {
		accent = m.color.lime
	} else if m.moment.kind == reactionRecovery {
		accent = m.color.amber
	}
	return clip(m.color.paint(accent, glyph)+" "+m.color.paint(m.color.text, m.moment.text), width)
}

func (m *Model) beginTurnExperience() {
	m.experience = turnExperience{turn: m.turnSequence, tools: make(map[string]toolOutcome)}
	m.clearMoment()
}

func (m *Model) recordToolStart(eventID, name, arguments string) {
	if m.experience.turn == 0 || m.experience.turn != m.turnSequence || eventID == "" {
		return
	}
	outcome := m.experience.tools[eventID]
	outcome.validation = outcome.validation || isValidationCommand(name, arguments)
	m.experience.tools[eventID] = outcome
}

func (m *Model) recordToolEnd(eventID, name, arguments string, failed bool) {
	if m.experience.turn == 0 || m.experience.turn != m.turnSequence || eventID == "" {
		return
	}
	outcome := m.experience.tools[eventID]
	outcome.validation = outcome.validation || isValidationCommand(name, arguments)
	outcome.completed = true
	outcome.failed = failed
	m.experience.tools[eventID] = outcome
}

func (m *Model) perfectPour() bool {
	if m.experience.turn == 0 || m.experience.turn != m.turnSequence || m.experience.errored || m.experience.aborted || m.experience.celebrated {
		return false
	}
	validation := false
	for _, outcome := range m.experience.tools {
		if !outcome.completed || outcome.failed {
			return false
		}
		validation = validation || outcome.validation
	}
	return validation
}

func (m *Model) celebrateValidation() tea.Cmd {
	if !m.perfectPour() {
		return nil
	}
	m.experience.celebrated = true
	return m.startMoment(reactionSuccess)
}

func (m *Model) startupTagline() string {
	switch m.personality() {
	case "quiet":
		return "Terminal coding companion."
	case "extra":
		if m.status.IsRepository && len(m.status.Entries) == 0 {
			return "Fresh branch. Maximum sparkle."
		}
		return "Less setup. Extra spark."
	default:
		if m.status.IsRepository && len(m.status.Entries) == 0 {
			return "Fresh branch. Plenty of bubbles."
		}
		if m.status.IsRepository && len(m.status.Entries) > 0 {
			return fmt.Sprintf("%d changed files. Let's add some bubbles.", len(m.status.Entries))
		}
		return "Less setup. More spark."
	}
}

func (m *Model) exitRecap() string {
	var prompts, tools, succeeded, failed, running int
	for _, entry := range m.entries {
		if entry.history {
			continue
		}
		switch entry.role {
		case "you":
			prompts++
		case "tool":
			tools++
			switch entry.state {
			case "done":
				succeeded++
			case "failed":
				failed++
			default:
				running++
			}
		}
	}
	title := "SESSION SUMMARY / this conversation"
	closing := ""
	if m.personality() != "quiet" {
		title = "BOTTLE CAP / this conversation"
		closing = "\nSodapop kept the useful parts."
		if m.personality() == "extra" {
			closing = "\nCapped, counted, and still sparkling."
		}
	}
	state := "settled"
	if m.busy() || len(m.requests) > 0 {
		state = "active or unresolved"
	}
	changed := "unknown"
	if m.status.IsRepository {
		changed = fmt.Sprintf("%d", len(m.status.Entries))
	}
	return fmt.Sprintf(
		"%s\nPrompts: %d  Tools: %d (%d succeeded, %d failed, %d running)\nCurrent known changed files: %s  State: %s%s",
		title, prompts, tools, succeeded, failed, running, changed, state, closing,
	)
}
