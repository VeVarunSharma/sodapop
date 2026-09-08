package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

type mascotPose uint8

const (
	mascotSealed mascotPose = iota
	mascotAnticipating
	mascotOpened
	mascotSodapoping
	mascotResting
)

const (
	mascotWidth         = 28
	mascotHeight        = 12
	compactMascotWidth  = 18
	compactMascotHeight = 7
	mascotOpeningX      = 15
	mascotOpeningY      = 1
)

type mascotInk uint8

const (
	mascotBlankInk mascotInk = iota
	mascotOutlineInk
	mascotLidInk
	mascotRimInk
	mascotTabInk
	mascotRingInk
	mascotGlintInk
	mascotGlowInk
	mascotBodyInk
	mascotShadeInk
	mascotShadowInk
	mascotFaceInk
	mascotLabelInk
	mascotAccentInk
	mascotLimbInk
	mascotInkCount
)

type mascotCell struct {
	glyph rune
	ink   mascotInk
}

type mascotDrawing [mascotHeight][mascotWidth]mascotCell

func (d *mascotDrawing) put(x, y int, ink mascotInk, text string) {
	for _, glyph := range text {
		d[y][x] = mascotCell{glyph: glyph, ink: ink}
		x++
	}
}

func mascotFrame(c palette, pose mascotPose, compact bool) string {
	width, height := mascotWidth, mascotHeight
	var drawing mascotDrawing
	if compact {
		width, height = compactMascotWidth, compactMascotHeight
		drawing = smallMascot(pose)
	} else {
		drawing = largeMascot(pose)
	}
	var paints [mascotInkCount]lipgloss.Style
	if !c.noColor {
		paints = mascotPaints(c)
	}
	var frame strings.Builder
	for y := range height {
		if y > 0 {
			frame.WriteByte('\n')
		}
		for x := 0; x < width; {
			ink := drawing[y][x].ink
			var run strings.Builder
			for ; x < width && drawing[y][x].ink == ink; x++ {
				glyph := drawing[y][x].glyph
				if glyph == 0 {
					glyph = ' '
				}
				if c.ascii {
					glyph = mascotASCII(glyph)
				}
				run.WriteRune(glyph)
			}
			if c.noColor || ink == mascotBlankInk {
				frame.WriteString(run.String())
			} else {
				frame.WriteString(paints[ink].Render(run.String()))
			}
		}
	}
	return frame.String()
}

func largeMascot(pose mascotPose) mascotDrawing {
	var d mascotDrawing
	d.put(8, 1, mascotLidInk, "░░░░░░░░░░░░")
	d.put(7, 1, mascotRingInk, "(")
	d.put(20, 1, mascotRingInk, ")")
	d.put(6, 2, mascotRimInk, "╰══════════════╯")
	d.put(6, 2, mascotRingInk, "╰")
	d.put(21, 2, mascotRingInk, "╯")
	for y := 3; y <= 9; y++ {
		d.put(6, y, mascotOutlineInk, "│")
		d.put(7, y, mascotGlintInk, "░")
		d.put(8, y, mascotGlowInk, "▒")
		d.put(9, y, mascotBodyInk, "·········")
		d.put(18, y, mascotShadeInk, "▒")
		d.put(19, y, mascotShadowInk, "▓▓")
		d.put(21, y, mascotOutlineInk, "│")
	}
	d.put(6, 9, mascotOutlineInk, "╲")
	d.put(21, 9, mascotOutlineInk, "╱")
	d.put(7, 10, mascotRimInk, "╰════════════╯")
	d.put(7, 10, mascotRingInk, "╰")
	d.put(20, 10, mascotRingInk, "╯")
	d.put(8, 11, mascotLimbInk, "╰──╯")
	d.put(16, 11, mascotLimbInk, "╰──╯")
	d.put(9, 7, mascotLabelInk, " SODAPOP ")
	d.put(9, 8, mascotAccentInk, "╲───────╱")

	brows, eyes, mouth := " ╭   ╮ ", " ●   ● ", " ╲___╱ "
	switch pose {
	case mascotAnticipating:
		brows, eyes, mouth = " ╱   ╲ ", " ◉   ◉ ", "  ╰─╯  "
	case mascotOpened:
		brows, eyes, mouth = " ╱   ╲ ", " ○   ○ ", "   O   "
	case mascotSodapoping:
		eyes, mouth = " ^   ^ ", " ╲___╱ "
	case mascotResting:
		eyes = " ^   ● "
	}
	d.put(10, 3, mascotFaceInk, brows)
	d.put(10, 4, mascotFaceInk, eyes)
	d.put(10, 5, mascotFaceInk, mouth)

	// Original fixed-cell artwork: the aperture occupies (14,1) and (15,1).
	// Its raised ring folds to the right, leaving that foam origin uncovered.
	if pose >= mascotOpened {
		d.put(14, 1, mascotTabInk, "▄▄")
		d.put(16, 0, mascotRingInk, "╭─╮")
		d.put(16, 1, mascotTabInk, "╰─╯")
	} else {
		d.put(14, 1, mascotTabInk, "[══]")
	}

	d.put(6, 5, mascotOutlineInk, "┤")
	d.put(21, 5, mascotOutlineInk, "├")
	d.put(3, 5, mascotLimbInk, "╭──")
	d.put(3, 6, mascotLimbInk, "╰╯")
	switch pose {
	case mascotAnticipating:
		d.put(18, 0, mascotLimbInk, "╭─────╮")
		d.put(18, 1, mascotTabInk, "╰╯")
		for y := 1; y <= 4; y++ {
			d.put(24, y, mascotLimbInk, "│")
		}
		d.put(22, 5, mascotLimbInk, "──╯")
	case mascotOpened, mascotSodapoping:
		d.put(3, 6, mascotBlankInk, "  ")
		d.put(2, 3, mascotLimbInk, "╲│╱")
		d.put(3, 4, mascotLimbInk, "╰╮")
		d.put(3, 5, mascotLimbInk, " ╰─")
		d.put(23, 3, mascotLimbInk, "╲│╱")
		d.put(23, 4, mascotLimbInk, "╭╯")
		d.put(22, 5, mascotLimbInk, "─╯")
	case mascotResting:
		d.put(23, 3, mascotLimbInk, "╲│╱")
		d.put(24, 4, mascotLimbInk, "│")
		d.put(22, 5, mascotLimbInk, "──╯")
	default:
		d.put(22, 5, mascotLimbInk, "──╮")
		d.put(23, 6, mascotLimbInk, "╰╯")
	}
	return d
}

func smallMascot(pose mascotPose) mascotDrawing {
	var d mascotDrawing
	d.put(5, 1, mascotLidInk, "░░░░░░░░")
	d.put(4, 1, mascotRingInk, "(")
	d.put(13, 1, mascotRingInk, ")")
	for y := 2; y <= 4; y++ {
		d.put(4, y, mascotOutlineInk, "│")
		d.put(5, y, mascotGlintInk, "░")
		d.put(6, y, mascotBodyInk, "·····")
		d.put(11, y, mascotShadeInk, "▒")
		d.put(12, y, mascotShadowInk, "▓")
		d.put(13, y, mascotOutlineInk, "│")
	}
	d.put(4, 5, mascotRimInk, "╰════════╯")
	d.put(4, 5, mascotRingInk, "╰")
	d.put(13, 5, mascotRingInk, "╯")
	d.put(5, 6, mascotLimbInk, "╰─╯")
	d.put(10, 6, mascotLimbInk, "╰─╯")
	d.put(5, 4, mascotLabelInk, "SODAPOP")
	eyes, mouth := "●   ●", "╲___╱"
	switch pose {
	case mascotAnticipating:
		eyes, mouth = "◉   ◉", " ╰─╯ "
	case mascotOpened:
		eyes, mouth = "○   ○", "  O  "
	case mascotSodapoping:
		eyes, mouth = "^   ^", "╲___╱"
	case mascotResting:
		eyes = "^   ●"
	}
	d.put(6, 2, mascotFaceInk, eyes)
	d.put(6, 3, mascotFaceInk, mouth)
	if pose >= mascotOpened {
		d.put(9, 1, mascotTabInk, "▄▄")
		d.put(11, 0, mascotRingInk, "╭╮")
		d.put(11, 1, mascotTabInk, "╰╯")
	} else {
		d.put(9, 1, mascotTabInk, "[══]")
	}
	d.put(4, 3, mascotOutlineInk, "┤")
	d.put(13, 3, mascotOutlineInk, "├")
	d.put(2, 3, mascotLimbInk, "╭─")
	d.put(2, 4, mascotLimbInk, "╰╯")
	switch pose {
	case mascotAnticipating:
		d.put(13, 0, mascotLimbInk, "╭──╮")
		d.put(13, 1, mascotTabInk, "╰")
		for y := 1; y <= 3; y++ {
			d.put(16, y, mascotLimbInk, "│")
		}
		d.put(13, 3, mascotOutlineInk, "│")
		d.put(13, 4, mascotOutlineInk, "├")
		d.put(14, 4, mascotLimbInk, "──╯")
	case mascotOpened, mascotSodapoping:
		d.put(2, 4, mascotBlankInk, "  ")
		d.put(1, 2, mascotLimbInk, "╲│╱")
		d.put(2, 3, mascotLimbInk, "╰─")
		d.put(14, 2, mascotLimbInk, "╲│╱")
		d.put(14, 3, mascotLimbInk, "─╯")
	case mascotResting:
		d.put(14, 2, mascotLimbInk, "╲│╱")
		d.put(14, 3, mascotLimbInk, "─╯")
	default:
		d.put(14, 3, mascotLimbInk, "─╮")
		d.put(14, 4, mascotLimbInk, "╰╯")
	}
	return d
}

func mascotPaints(c palette) [mascotInkCount]lipgloss.Style {
	ink, text := lipgloss.Color(c.ink), lipgloss.Color(c.text)
	muted := lipgloss.Color(c.muted)
	cyan, magenta := lipgloss.Color(c.cyan), lipgloss.Color(c.magenta)
	front := lipgloss.Blend1D(3, magenta, lipgloss.Color(c.red))[1]
	glow := lipgloss.Blend1D(5, front, cyan)[1]
	dark, bright := ink, text
	dr, dg, db, _ := dark.RGBA()
	br, bg, bb, _ := bright.RGBA()
	if dr+dg+db > br+bg+bb {
		dark, bright = bright, dark
	}
	shadow := lipgloss.Blend1D(3, magenta, dark)[1]
	silver := lipgloss.Blend1D(3, bright, muted)[1]
	paint := func(foreground, background color.Color) lipgloss.Style {
		s := lipgloss.NewStyle().Foreground(foreground)
		if background != nil {
			s = s.Background(background)
		}
		return s
	}
	return [mascotInkCount]lipgloss.Style{
		mascotBlankInk:   lipgloss.NewStyle(),
		mascotOutlineInk: paint(magenta, nil),
		mascotLidInk:     paint(muted, silver),
		mascotRimInk:     paint(bright, muted),
		mascotTabInk:     paint(dark, silver),
		mascotRingInk:    paint(text, nil),
		mascotGlintInk:   paint(text, cyan),
		mascotGlowInk:    paint(front, glow),
		mascotBodyInk:    paint(lipgloss.Blend1D(5, front, ink)[1], front),
		mascotShadeInk:   paint(front, magenta),
		mascotShadowInk:  paint(shadow, magenta),
		mascotFaceInk:    paint(ink, front),
		mascotLabelInk:   paint(ink, text).Bold(true),
		mascotAccentInk:  paint(cyan, front),
		mascotLimbInk:    paint(cyan, nil),
	}
}

func mascotASCII(glyph rune) rune {
	switch glyph {
	case '╭', '╯', '╱':
		return '/'
	case '╮', '╰', '╲':
		return '\\'
	case '─':
		return '-'
	case '═':
		return '='
	case '│':
		return '|'
	case '├', '┤':
		return '+'
	case '░', '·':
		return '.'
	case '▒':
		return ':'
	case '▓', '▄':
		return '#'
	case '●':
		return 'o'
	case '◉', '○':
		return 'O'
	default:
		return glyph
	}
}
