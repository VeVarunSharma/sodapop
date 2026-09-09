package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/VeVarunSharma/sodapop/internal/skills"
)

func (m *Model) loadSkills() tea.Cmd {
	if m.opts.ManageSkills == nil {
		return nil
	}
	m.skillGeneration++
	generation, manage, ctx := m.skillGeneration, m.opts.ManageSkills, m.life.ctx
	return func() tea.Msg {
		state, err := manage(ctx, skills.Action{Operation: "load"})
		return skillsMsg{generation: generation, action: "load", state: state, err: err}
	}
}

func (m *Model) manageSkills(args string) (tea.Cmd, bool) {
	fields := strings.Fields(args)
	if len(fields) == 0 || strings.EqualFold(fields[0], "list") || strings.EqualFold(fields[0], "status") {
		m.showSkills()
		return nil, true
	}
	if m.opts.ManageSkills == nil {
		m.report("Skill management is unavailable in this build.", true)
		return nil, false
	}
	operation := strings.ToLower(fields[0])
	if operation != "trust" && operation != "add" && operation != "enable" &&
		operation != "disable" && operation != "remove" {
		return m.skillsUsage()
	}
	value := strings.TrimSpace(args[len(fields[0]):])
	if value == "" || (operation == "enable" || operation == "disable" || operation == "remove") && len(fields) != 2 {
		return m.skillsUsage()
	}
	if operation == "add" {
		operation = "install"
	}
	m.skillGeneration++
	generation, manage, ctx := m.skillGeneration, m.opts.ManageSkills, m.life.ctx
	m.report("Updating project skills; active work is unchanged until the operation succeeds.", false)
	return func() tea.Msg {
		state, err := manage(ctx, skills.Action{Operation: operation, Value: value})
		return skillsMsg{generation: generation, action: operation, state: state, err: err}
	}, true
}

func (m *Model) skillsResult(msg skillsMsg) tea.Cmd {
	if msg.generation != m.skillGeneration || m.quitting {
		return nil
	}
	if msg.err != nil {
		m.report("Skill operation failed: "+safeText(msg.err.Error()), true)
		return nil
	}
	m.setSkillState(msg.state)
	switch msg.action {
	case "load":
		return nil
	case "trust":
		m.report("Skill source trusted. Install from it with /skill add <path-or-git-url>.", false)
	case "install":
		m.report("Skill installed but disabled for this project. Enable it with /skill enable <name>.", false)
	case "remove":
		m.report("Skill removed from the catalog. Immutable content may remain for saved conversations.", false)
	case "enable", "disable":
		m.resetConversation()
		m.report("Project skills changed. Starting a fresh Copilot connection; previous conversation history is kept.", false)
		if m.account.ID != "" {
			return m.connect()
		}
	}
	return nil
}

func (m *Model) setSkillState(state skills.State) {
	m.skillState = cloneSkillState(state)
	active := make(map[string]bool, len(state.Enabled))
	for _, ref := range state.Enabled {
		active[ref.Digest] = true
	}
	m.skills = make([]Capability, 0, len(state.Installed))
	for _, entry := range state.Installed {
		m.skills = append(m.skills, Capability{Name: entry.Name, Active: active[entry.Digest]})
	}
	slices.SortFunc(m.skills, func(a, b Capability) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	m.sidebarCache = ""
}

func (m *Model) showSkills() {
	var body strings.Builder
	body.WriteString("Only skills explicitly installed in Sodapop are shown. Ambient Copilot skills and project discovery remain disabled.\n\n")
	if len(m.skillState.Installed) == 0 {
		body.WriteString("No skills installed.\n\n")
	} else {
		active := make(map[string]bool, len(m.skillState.Enabled))
		for _, ref := range m.skillState.Enabled {
			active[ref.Digest] = true
		}
		for _, entry := range m.skillState.Installed {
			state := "disabled"
			if active[entry.Digest] {
				state = "enabled"
			}
			fmt.Fprintf(&body, "%s  %s\n  %s\n  source: %s", entry.Name, state, entry.Description, entry.Source.Location)
			if entry.Source.Revision != "" {
				fmt.Fprintf(&body, "\n  revision: %.12s", entry.Source.Revision)
			}
			fmt.Fprintf(&body, "\n  digest: %.12s\n\n", entry.Digest)
		}
	}
	body.WriteString("Trust: /skill trust <directory-or-git-url>\n")
	body.WriteString("Install: /skill add <skill-directory-or-git-url[#ref]>\n")
	body.WriteString("Project: /skill enable|disable <name>\n")
	body.WriteString("Remove: /skill remove <name>\n\n")
	body.WriteString("Skill changes start a fresh conversation. Normal tool approvals still apply.")
	m.newDialog(dialogSkills, "SKILLS / explicit and project-scoped", body.String())
}

func (m *Model) skillsUsage() (tea.Cmd, bool) {
	m.report("Usage: /skill, /skill trust <source>, /skill add <source>, or /skill enable|disable|remove <name>.", true)
	return nil, false
}

func cloneSkillState(state skills.State) skills.State {
	return skills.State{
		Installed: append([]skills.Entry(nil), state.Installed...),
		Enabled:   append([]skills.Reference(nil), state.Enabled...),
		Trust: skills.Trust{
			LocalRoots:      append([]string(nil), state.Trust.LocalRoots...),
			GitRepositories: append([]string(nil), state.Trust.GitRepositories...),
		},
	}
}
