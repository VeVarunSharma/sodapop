package ui

import (
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/x/ansi"
)

const (
	startupDuration   = 2400 * time.Millisecond
	startupSceneWidth = 32
	startupSceneRows  = 16
	startupTextWidth  = 38
	startupParticles  = 20
	startupCanX       = 1
	startupCanY       = 3
	startupSodapopX      = startupCanX + mascotOpeningX
	startupSodapopY      = startupCanY + mascotOpeningY
)

type startupState uint8

const (
	startupWaiting startupState = iota
	startupPlaying
	startupFinished
)

type startupAnimation struct {
	state   startupState
	started time.Time
	elapsed time.Duration
}

func (m *Model) startupFits() bool {
	return !m.layout.compact &&
		m.layout.innerWidth >= startupSceneWidth+2+startupTextWidth &&
		m.layout.timeline >= startupSceneRows
}

func (m *Model) compactStartupFits() bool {
	g := m.layout
	if g.compact {
		return g.innerWidth >= compactMascotWidth && g.timeline >= compactMascotHeight+5
	}
	textWidth := g.innerWidth - compactMascotWidth - 2
	if textWidth < 32 || g.timeline < compactMascotHeight {
		return false
	}
	// Reserve one line for the "Esc skips the intro" hint shown while playing.
	return lipgloss.Height(m.mascotWelcomeCopy(textWidth))+1 <= g.timeline
}

func (m *Model) startupAnimationFits() bool {
	return m.startupFits() || m.compactStartupFits()
}

func (m *Model) reconcileStartup(sized bool) {
	if m.startup.state == startupFinished {
		return
	}
	if m.opts.NoBanner || m.prefs.ReducedMotion || m.quitting || m.notice.error ||
		m.overlay != nil || len(m.entries) > 0 || m.session.ID != "" ||
		m.composer.Value() != "" || m.life.ctx.Err() != nil {
		m.finishStartup()
		return
	}
	if sized {
		if !m.startupAnimationFits() {
			m.finishStartup()
		} else if m.startup.state == startupWaiting {
			m.startup.state = startupPlaying
			m.startup.started = m.now()
		}
	} else if m.startup.state == startupPlaying && !m.startupAnimationFits() {
		m.finishStartup()
	}
}

func (m *Model) finishStartup() {
	if m.startup.state == startupFinished {
		return
	}
	m.startup.state = startupFinished
	m.startup.elapsed = startupDuration
	// Retire an already queued tick as well as the intro. Busy UI can request
	// a fresh tick, but dismissed artwork cannot be revived by an old one.
	m.motionEpoch++
	m.motionPending = false
}

func (m *Model) advanceStartup(at time.Time) {
	if m.startup.state != startupPlaying {
		return
	}
	m.startup.elapsed = max(m.startup.elapsed, at.Sub(m.startup.started))
	if m.startup.elapsed >= startupDuration {
		m.finishStartup()
	}
}

func startupPose(at time.Duration) mascotPose {
	switch {
	case at < 400*time.Millisecond:
		return mascotSealed
	case at < 800*time.Millisecond:
		return mascotAnticipating
	case at < time.Second:
		return mascotOpened
	case at < 1800*time.Millisecond:
		return mascotSodapoping
	default:
		return mascotResting
	}
}

var startupSpring = harmonica.NewSpring(motionInterval.Seconds(), 14, 0.55)

func startupBounce(at time.Duration) int {
	var position, velocity float64
	switch {
	case at < 400*time.Millisecond:
		position, velocity = 1, -18
	case at >= 800*time.Millisecond && at < 1300*time.Millisecond:
		at -= 800 * time.Millisecond
		position, velocity = -1, 18
	default:
		return 0
	}
	// Sample a bounded, fixed-step spring from elapsed time. Slow renders skip
	// ahead instead of stretching startup or changing the particle sequence.
	for steps := int(max(0, at) / motionInterval); steps > 0; steps-- {
		position, velocity = startupSpring.Update(position, velocity, 0)
	}
	return max(-1, min(1, int(math.Round(position))))
}

type sodapopParticle struct {
	x, y  int
	glyph string
}

func sodapopParticles(at time.Duration, ascii bool) []sodapopParticle {
	if at < time.Second || at >= startupDuration {
		return nil
	}
	particles := make([]sodapopParticle, 0, startupParticles)
	glyphs := []string{"·", "°", "○", "o"}
	if ascii {
		glyphs = []string{".", "o", "O", "."}
	}
	for i := range startupParticles {
		birth := time.Second + time.Duration(i%6)*70*time.Millisecond
		age := at - birth
		lifetime := time.Duration(650+i%5*60) * time.Millisecond
		if age < 0 || age >= lifetime {
			continue
		}
		t := age.Seconds()
		direction := float64(1 - 2*(i%2))
		x := startupSodapopX + direction*(1+t*float64(4+i*5%9))
		y := startupSodapopY - t*float64(4+i%4*2)
		if i%4 == 0 {
			y = startupSodapopY - 1 - 6*t + 7*t*t
		}
		column, row := int(math.Round(x)), int(math.Round(y))
		if column >= 0 && column < startupSceneWidth && row >= 0 && row < startupSceneRows {
			particles = append(particles, sodapopParticle{x: column, y: row, glyph: glyphs[i%len(glyphs)]})
		}
	}
	return particles
}

func startupFoam(c palette, at time.Duration) string {
	switch {
	case at < time.Second || at >= 2050*time.Millisecond:
		return ""
	case at < 1150*time.Millisecond || at >= 1800*time.Millisecond:
		return c.paint(c.text, c.glyph("   ▄▄▄   ", "   ooo   "))
	case at < 1350*time.Millisecond || at >= 1650*time.Millisecond:
		return c.paint(c.text, c.glyph("  ▄███▄  \n  ▀███▀  ", "  .ooo.  \n  (OOO)  "))
	default:
		return c.paint(c.muted, c.glyph("  ▄▄▄▄▄  ", "  .ooo.  ")) + "\n" +
			c.paint(c.text, c.glyph(" ▟█████▙ \n ▝▀███▀▘ ", " (oOOOo) \n  `ooo'  "))
	}
}

func startupScene(c palette, at time.Duration, animated bool) string {
	pose, y := mascotResting, startupCanY
	if animated {
		pose, y = startupPose(at), y+startupBounce(at)
	}
	layers := []*lipgloss.Layer{
		lipgloss.NewLayer(c.background(c.panel).Render(fitBlock("", startupSceneWidth, startupSceneRows))),
		lipgloss.NewLayer(mascotFrame(c, pose, false)).X(startupCanX).Y(y).Z(1),
	}
	if animated {
		for _, particle := range sodapopParticles(at, c.ascii) {
			layers = append(layers, lipgloss.NewLayer(c.paint(c.cyan, particle.glyph)).
				X(particle.x).Y(particle.y).Z(2))
		}
		if foam := startupFoam(c, at); foam != "" {
			layers = append(layers, lipgloss.NewLayer(foam).
				X(startupSodapopX-lipgloss.Width(foam)/2).Y(startupSodapopY+1-lipgloss.Height(foam)).Z(3))
		}
		if at >= 800*time.Millisecond && at < 1100*time.Millisecond {
			layers = append(layers, lipgloss.NewLayer(c.paint(c.amber, "POP!")).X(24).Y(2).Z(4))
		}
	}
	return fitBlock(lipgloss.NewCompositor(layers...).Render(), startupSceneWidth, startupSceneRows)
}

func compactStartupMascot(c palette, at time.Duration, animated bool) string {
	pose := mascotResting
	if animated {
		pose = startupPose(at)
	}
	art := mascotFrame(c, pose, true)
	if !animated || at < time.Second || at >= 1800*time.Millisecond {
		return art
	}

	glyphs := []string{"·", "°"}
	if c.ascii {
		glyphs = []string{".", "o"}
	}
	phase := int((at - time.Second) / (150 * time.Millisecond))
	positions := [][2]int{{9 - phase%3, 0}, {14 + phase%2, phase % 2}}
	layers := []*lipgloss.Layer{lipgloss.NewLayer(art).Z(1)}
	for i, position := range positions {
		layers = append(layers, lipgloss.NewLayer(c.paint(c.cyan, glyphs[i])).
			X(position[0]).Y(position[1]).Z(2))
	}
	return fitBlock(lipgloss.NewCompositor(layers...).Render(), compactMascotWidth, compactMascotHeight)
}

func (m *Model) mascotWelcome() (string, bool) {
	c, g := m.color, m.layout
	if m.opts.NoBanner {
		return "", false
	}
	if g.compact {
		if g.innerWidth < compactMascotWidth || g.timeline < compactMascotHeight+5 {
			return "", false
		}
		art := compactStartupMascot(c, m.startup.elapsed, m.startup.state == startupPlaying)
		art = lipgloss.PlaceHorizontal(g.innerWidth, lipgloss.Center, art)
		body := art + "\n\nFresh conversation.\n/login account / F1 help\nType / for commands."
		if m.account.ID != "" {
			body = art + "\n\nFresh conversation.\nType a prompt or /help."
		}
		return fitBlock(body, g.innerWidth, g.timeline), true
	}
	full := m.startupFits()
	artWidth := compactMascotWidth
	if full {
		artWidth = startupSceneWidth
	}
	textWidth := g.innerWidth - artWidth - 2
	if textWidth < 32 || g.timeline < compactMascotHeight {
		return "", false
	}
	copy := m.mascotWelcomeCopy(textWidth)
	if lipgloss.Height(copy) > g.timeline {
		return "", false
	}
	var art string
	if full {
		art = startupScene(c, m.startup.elapsed, m.startup.state != startupFinished)
	} else {
		art = compactStartupMascot(c, m.startup.elapsed, m.startup.state == startupPlaying)
	}
	height := max(lipgloss.Height(art), lipgloss.Height(copy))
	art = lipgloss.PlaceVertical(height, lipgloss.Center, art)
	copy = lipgloss.PlaceVertical(height, lipgloss.Center, copy)
	body := lipgloss.JoinHorizontal(lipgloss.Top, art, "  ", copy)
	body = strings.Repeat("\n", max(0, (g.timeline-height)/2)) + body
	return fitBlock(body, g.innerWidth, g.timeline), true
}

func (m *Model) mascotWelcomeCopy(width int) string {
	c := m.color
	status := ""
	switch {
	case m.identityLoading:
		status = "Checking GitHub in the background."
	case m.account.ID == "":
		status = "/login  Connect your GitHub account."
	case m.connecting:
		status = "Connecting to Copilot. Draft anytime."
	case m.model == "":
		status = "/model  Choose a model to start."
	default:
		status = "Ready when you are. What should we build?"
	}
	lines := []string{
		c.paint(c.magenta, "SODAPOP"),
		c.paint(c.cyan, m.startupTagline()),
		"",
		c.paint(c.muted, clip(m.projectName()+" / fresh conversation", width)),
		"",
		c.paint(c.lime, status),
		"",
		c.paint(c.magenta, "/help") + "    Commands and shortcuts",
		c.paint(c.magenta, "/resume") + "  Return to a conversation",
		c.paint(c.magenta, "/theme") + "   Color, personality, motion and ASCII",
		"",
		c.paint(c.muted, "Nothing is sent until you press Enter."),
	}
	if m.startup.state == startupPlaying {
		lines = append(lines, c.paint(c.muted, "Esc skips the intro."))
	}
	return ansi.Wrap(strings.Join(lines, "\n"), width, "")
}
