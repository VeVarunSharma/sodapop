package ui

import (
	"fmt"
	"image/color"
	"math"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/VeVarunSharma/sodapop/internal/config"
	"github.com/charmbracelet/x/ansi"
)

func TestMascotFramesHaveStableDimensions(t *testing.T) {
	if mascotWidth != 28 || mascotHeight != 12 || compactMascotWidth != 18 || compactMascotHeight != 7 {
		t.Fatal("mascot dimensions changed the scene integration contract")
	}
	sgr := regexp.MustCompile(`\x1b\[[0-9;:]*m`)
	words := regexp.MustCompile(`[A-Za-z]{2,}`)
	for _, theme := range []string{"arcade", "graphite", "midnight", "high-contrast", "light"} {
		for _, noColor := range []bool{false, true} {
			for _, ascii := range []bool{false, true} {
				for _, size := range []struct {
					name          string
					compact       bool
					width, height int
				}{{"full", false, 28, 12}, {"compact", true, 18, 7}} {
					for pose := mascotSealed; pose <= mascotResting; pose++ {
						name := fmt.Sprintf("%s/mono=%t/ascii=%t/%s/pose=%d", theme, noColor, ascii, size.name, pose)
						t.Run(name, func(t *testing.T) {
							t.Parallel()
							c := colors(config.Preferences{Theme: theme, NoColor: noColor, ASCII: ascii})
							frame := mascotFrame(c, pose, size.compact)
							if again := mascotFrame(c, pose, size.compact); again != frame {
								t.Fatal("rendering is not deterministic")
							}
							plain := ansi.Strip(frame)
							if noColor && strings.ContainsRune(frame, '\x1b') {
								t.Fatal("NoColor emitted an escape sequence")
							}
							if !noColor && !strings.Contains(frame, "\x1b[") {
								t.Fatal("color rendering lost its ANSI shading")
							}
							if !noColor {
								paints := mascotPaints(c)
								shade := c.glyph("▓", "#")
								if !size.compact {
									shade += shade
								}
								for _, band := range []struct {
									ink  mascotInk
									text string
								}{{mascotGlintInk, c.glyph("░", ".")}, {mascotShadowInk, shade}} {
									paint := paints[band.ink]
									_, _, _, alpha := paint.GetBackground().RGBA()
									if alpha == 0 || !strings.Contains(frame, paint.Render(band.text)) {
										t.Fatal("can enamel became an unfilled outline")
									}
								}
							}
							if sgr.ReplaceAllString(frame, "") != plain {
								t.Fatal("rendering emitted a non-SGR control sequence")
							}
							for _, glyph := range frame {
								if ascii && glyph > unicode.MaxASCII {
									t.Fatalf("ASCII rendering emitted %q", glyph)
								}
							}
							for _, glyph := range plain {
								if glyph == '\n' {
									continue
								}
								if unicode.IsControl(glyph) || ansi.StringWidth(string(glyph)) != 1 {
									t.Fatalf("mascot has a non-cell glyph %q", glyph)
								}
							}
							lines := strings.Split(frame, "\n")
							if len(lines) != size.height {
								t.Fatalf("height = %d, want %d", len(lines), size.height)
							}
							for row, line := range lines {
								if width := ansi.StringWidth(line); width != size.width {
									t.Fatalf("row %d width = %d, want %d: %q", row, width, size.width, line)
								}
							}
							if labels := words.FindAllString(plain, -1); len(labels) != 1 || labels[0] != "SODAPOP" {
								t.Fatalf("mascot must have one SODAPOP label and no extra tagline:\n%s", plain)
							}
							c.noColor = true
							if mono := mascotFrame(c, pose, size.compact); plain != mono {
								t.Fatal("monochrome rendering lost the color sprite's detail")
							}
						})
					}
				}
			}
		}
	}
}

func TestMascotTabOpeningAndBodyStayAnchored(t *testing.T) {
	c := colors(config.Preferences{NoColor: true, ASCII: true})
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("compact=%t", compact), func(t *testing.T) {
			openingX, ringX, ringTop, ringBottom := 14, 16, "/-\\", "\\-/"
			bottomRow := 9
			if compact {
				openingX, ringX, ringTop, ringBottom = 9, 11, "/\\", "\\/"
				bottomRow = 5
			}
			sealed := strings.Split(mascotFrame(c, mascotSealed, compact), "\n")
			resting := strings.Split(mascotFrame(c, mascotResting, compact), "\n")
			for pose := mascotSealed; pose <= mascotResting; pose++ {
				lines := strings.Split(mascotFrame(c, pose, compact), "\n")
				if pose < mascotOpened {
					if tab := lines[1][openingX : openingX+4]; tab != "[==]" {
						t.Fatalf("pose %d opened the sealed pull tab: %q", pose, tab)
					}
					if strings.Contains(lines[1], "##") {
						t.Fatalf("pose %d already has a drinking aperture", pose)
					}
				} else {
					if lines[1][openingX:openingX+2] != "##" {
						t.Fatalf("pose %d moved or closed the aperture", pose)
					}
					if lines[0][ringX:ringX+len(ringTop)] != ringTop ||
						lines[1][ringX:ringX+len(ringBottom)] != ringBottom {
						t.Fatalf("pose %d did not retain the raised pull ring", pose)
					}
					if lines[0] != resting[0] || lines[1] != resting[1] {
						t.Fatalf("pose %d moved the opened lid", pose)
					}
				}
				if strings.Join(lines[bottomRow:], "\n") != strings.Join(sealed[bottomRow:], "\n") {
					t.Fatalf("pose %d moved the base or feet", pose)
				}
			}
			if strings.TrimSpace(sealed[0]) != "" {
				t.Fatal("sealed can uses the raised-ring/foam space")
			}
		})
	}
}

func TestMascotCompactArtworkIsNotClipped(t *testing.T) {
	for pose := mascotSealed; pose <= mascotResting; pose++ {
		drawing := smallMascot(pose)
		for y, row := range drawing {
			for x, cell := range row {
				if (x >= compactMascotWidth || y >= compactMascotHeight) && cell.glyph != 0 {
					t.Fatalf("pose %d clips an authored cell at (%d,%d)", pose, x, y)
				}
			}
		}
	}
}

func TestMascotSodapopLabelStaysCenteredInsideCan(t *testing.T) {
	for _, size := range []struct {
		name              string
		compact           bool
		x, y, faceRow     int
		label             string
		leftInk, rightInk mascotInk
	}{
		{"full", false, 9, 7, 4, " SODAPOP ", mascotGlowInk, mascotShadeInk},
		{"compact", true, 5, 4, 2, "SODAPOP", mascotOutlineInk, mascotShadowInk},
	} {
		for pose := mascotSealed; pose <= mascotResting; pose++ {
			t.Run(fmt.Sprintf("%s/pose=%d", size.name, pose), func(t *testing.T) {
				drawing := largeMascot(pose)
				if size.compact {
					drawing = smallMascot(pose)
				}
				for column, want := range size.label {
					cell := drawing[size.y][size.x+column]
					if cell.glyph != want || cell.ink != mascotLabelInk {
						t.Fatalf("label cell %d = %+v; want %q in label ink", column, cell, want)
					}
				}
				faceLeft, faceRight := -1, -1
				for x, cell := range drawing[size.faceRow] {
					if cell.ink == mascotFaceInk {
						if faceLeft < 0 {
							faceLeft = x
						}
						faceRight = x
					}
				}
				if faceLeft < 0 || 2*size.x+len(size.label)-1 != faceLeft+faceRight {
					t.Fatal("label is not centered below the face")
				}
				if drawing[size.y][size.x-1].ink != size.leftInk ||
					drawing[size.y][size.x+len(size.label)].ink != size.rightInk {
					t.Fatal("longer label overwrote the can's edge or side shading")
				}
				if !size.compact {
					var accent strings.Builder
					for _, cell := range drawing[8][9:18] {
						if cell.ink != mascotAccentInk {
							t.Fatal("label underline no longer spans the brand")
						}
						accent.WriteRune(cell.glyph)
					}
					if accent.String() != "╲───────╱" {
						t.Fatalf("label underline lost its centered shape: %q", accent.String())
					}
				}
			})
		}
	}
}

func TestMascotMetalLidStaysRoundedWithAnOpenOrClosedTab(t *testing.T) {
	c := colors(config.Preferences{NoColor: true})
	for _, compact := range []bool{false, true} {
		left, right := 7, 20
		if compact {
			left, right = 4, 13
		}
		for _, pose := range []mascotPose{mascotSealed, mascotResting} {
			lines := strings.Split(mascotFrame(c, pose, compact), "\n")
			lid := []rune(lines[1])
			if lid[left] != '(' || lid[right] != ')' {
				t.Fatalf("compact=%t pose=%d lost the rounded lid", compact, pose)
			}
			if !compact {
				rim := []rune(lines[2])
				if rim[6] != '╰' || rim[21] != '╯' {
					t.Fatalf("pose=%d lost the curved rolled rim", pose)
				}
			}
		}
	}
}

func TestMascotExpressionsAndHandsTellTheOpeningStory(t *testing.T) {
	c := colors(config.Preferences{NoColor: true, ASCII: true})
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("compact=%t", compact), func(t *testing.T) {
			eyeRow, eyeX, mouthRow, faceX, faceWidth := 4, 11, 5, 10, 7
			mouths := []string{" \\___/ ", "  \\-/  ", "   O   ", " \\___/ ", " \\___/ "}
			if compact {
				eyeRow, eyeX, mouthRow, faceX, faceWidth = 2, 6, 3, 6, 5
				mouths = []string{"\\___/", " \\-/ ", "  O  ", "\\___/", "\\___/"}
			}
			eyes := []string{"o   o", "O   O", "O   O", "^   ^", "^   o"}
			seen := make(map[string]mascotPose)
			for pose := mascotSealed; pose <= mascotResting; pose++ {
				frame := mascotFrame(c, pose, compact)
				lines := strings.Split(frame, "\n")
				if lines[eyeRow][eyeX:eyeX+5] != eyes[pose] ||
					lines[mouthRow][faceX:faceX+faceWidth] != mouths[pose] {
					t.Fatalf("pose %d lost its facial expression:\n%s", pose, frame)
				}
				if other, exists := seen[frame]; exists {
					t.Fatalf("poses %d and %d are identical without color", other, pose)
				}
				seen[frame] = pose
			}
			anticipating := strings.Split(mascotFrame(c, mascotAnticipating, compact), "\n")
			resting := strings.Split(mascotFrame(c, mascotResting, compact), "\n")
			if compact {
				if anticipating[0][13:17] != "/--\\" || anticipating[1][13] != '\\' ||
					anticipating[3][16] != '|' || anticipating[4][14:17] != "--/" {
					t.Fatal("compact hand does not reach and return from the pull tab")
				}
				if resting[2][14:17] != "\\|/" || resting[3][14:16] != "-/" {
					t.Fatal("compact resting can does not wave")
				}
			} else {
				if anticipating[0][18:25] != "/-----\\" || anticipating[1][18:20] != "\\/" ||
					anticipating[4][24] != '|' || anticipating[5][22:25] != "--/" {
					t.Fatal("hand does not reach and return from the pull tab")
				}
				if resting[3][23:26] != "\\|/" || resting[4][24] != '|' ||
					resting[5][22:25] != "--/" {
					t.Fatal("resting can does not wave")
				}
			}
		})
	}
}

func TestMascotShadingPreservesTheCylinderWithoutColor(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		c := colors(config.Preferences{NoColor: true, ASCII: ascii})
		for _, compact := range []bool{false, true} {
			frame := mascotFrame(c, mascotResting, compact)
			lines := strings.Split(frame, "\n")
			left, right := "░▒", "▒▓▓"
			if compact {
				left, right = "░", "▒▓"
			}
			if ascii {
				left, right = ".:", ":##"
				if compact {
					left, right = ".", ":#"
				}
			}
			row := 4
			if compact {
				row = 2
			}
			if !strings.Contains(lines[row], left) || !strings.Contains(lines[row], right) {
				t.Fatalf("ascii=%t compact=%t lost the highlight or shaded side:\n%s", ascii, compact, frame)
			}
		}
	}
}

func TestMascotPaletteKeepsFaceLabelAndApertureReadable(t *testing.T) {
	for _, theme := range []string{"arcade", "graphite", "midnight", "high-contrast", "light"} {
		t.Run(theme, func(t *testing.T) {
			paints := mascotPaints(colors(config.Preferences{Theme: theme}))
			for _, ink := range []mascotInk{mascotFaceInk, mascotLabelInk, mascotTabInk} {
				paint := paints[ink]
				foreground := mascotTestLuminance(paint.GetForeground())
				background := mascotTestLuminance(paint.GetBackground())
				contrast := (max(foreground, background) + 0.05) / (min(foreground, background) + 0.05)
				if contrast < 4.5 {
					t.Fatalf("ink %d contrast = %.2f, want at least 4.5", ink, contrast)
				}
			}
			if mascotTestLuminance(paints[mascotTabInk].GetForeground()) >=
				mascotTestLuminance(paints[mascotTabInk].GetBackground()) {
				t.Fatal("drinking aperture is not dark against its silver lid")
			}
			if mascotTestLuminance(paints[mascotShadowInk].GetForeground()) >=
				mascotTestLuminance(paints[mascotBodyInk].GetBackground()) {
				t.Fatal("purple side shadow must be darker than the can's front")
			}
		})
	}
}

func mascotTestLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	linear := func(channel uint32) float64 {
		v := float64(channel) / 65535
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}
