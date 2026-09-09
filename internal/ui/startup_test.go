package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/config"
	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/charmbracelet/x/ansi"
)

func startupModel(t *testing.T) *Model {
	t.Helper()
	options := testOptions()
	options.Preferences.ReducedMotion = false
	m := testModel(t, options)
	m.now = func() time.Time { return time.Unix(1000, 0) }
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.startup.state != startupPlaying {
		t.Fatal("normal-sized startup did not begin the intro")
	}
	return m
}

func startupTick(m *Model, elapsed time.Duration) {
	m.Update(motionMsg{epoch: m.motionEpoch, at: m.startup.started.Add(elapsed)})
}

func TestStartupWaitsForWindowSizeWithoutBlockingDiscovery(t *testing.T) {
	options := testOptions()
	options.Preferences.ReducedMotion = false
	a, w := &fakeAuth{currentErr: auth.ErrNotSignedIn}, &fakeWorkspace{}
	options.Auth, options.Workspace = a, w
	m := testModel(t, options)
	m.now = func() time.Time { return time.Unix(1000, 0) }
	if m.startup.state != startupWaiting || m.ensureMotion() != nil {
		t.Fatal("intro started against the placeholder terminal dimensions")
	}
	m.Update(tea.WindowSizeMsg{})
	if m.startup.state != startupWaiting || m.motionPending {
		t.Fatal("an unavailable terminal size consumed the one-shot intro")
	}
	cmd := m.Init()
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 3 {
		t.Fatal("startup did not independently schedule identity and workspace discovery")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	startupTick(m, 1400*time.Millisecond)
	if startupPose(m.startup.elapsed) != mascotSodapoping || !m.identityLoading {
		t.Fatal("intro waited for identity before advancing")
	}
	if a.currentCalls.Load() != 0 || w.statusCalls.Load() != 0 {
		t.Fatal("a view or animation update performed blocking service work")
	}
	for _, work := range batch {
		m.Update(work())
	}
	if a.currentCalls.Load() != 1 || w.statusCalls.Load() != 1 || m.identityLoading {
		t.Fatal("discovery did not complete independently of the running intro")
	}
	if m.startup.state != startupPlaying || m.session.ID != "" {
		t.Fatal("discovery restarted the intro or eagerly created a conversation")
	}
}

func TestStartupStoryUsesElapsedTimeAndEndsAtDeadline(t *testing.T) {
	m := startupModel(t)
	initial := m.welcomeView()
	m.now = func() time.Time { return m.startup.started.Add(time.Hour) }
	if m.welcomeView() != initial || m.startup.elapsed != 0 {
		t.Fatal("rendering advanced animation state")
	}
	cases := []struct {
		at   time.Duration
		pose mascotPose
	}{
		{399 * time.Millisecond, mascotSealed},
		{400 * time.Millisecond, mascotAnticipating},
		{799 * time.Millisecond, mascotAnticipating},
		{800 * time.Millisecond, mascotOpened},
		{999 * time.Millisecond, mascotOpened},
		{time.Second, mascotSodapoping},
		{1799 * time.Millisecond, mascotSodapoping},
		{1800 * time.Millisecond, mascotResting},
		{startupDuration - time.Nanosecond, mascotResting},
	}
	renderCount := m.renderCount
	for _, tc := range cases {
		startupTick(m, tc.at)
		if m.startup.elapsed != tc.at || startupPose(m.startup.elapsed) != tc.pose {
			t.Fatalf("wrong pose at %s: elapsed=%s pose=%d", tc.at, m.startup.elapsed, startupPose(m.startup.elapsed))
		}
		if m.startup.state != startupPlaying {
			t.Fatalf("intro ended before its deadline at %s", tc.at)
		}
	}
	startupTick(m, startupDuration)
	if m.startup.state != startupFinished || m.startup.elapsed != startupDuration {
		t.Fatal("intro did not finish at its exact deadline")
	}
	if m.canAnimate() || m.motionPending || m.ensureMotion() != nil {
		t.Fatal("resting welcome screen kept a decorative tick loop running")
	}
	if m.renderCount != renderCount {
		t.Fatal("startup animation reparsed the conversation timeline")
	}
	if !strings.Contains(m.welcomeView(), "Checking GitHub") || strings.Contains(m.welcomeView(), "Ready when you are") {
		t.Fatal("animation completion incorrectly implied connection readiness")
	}

	late := startupModel(t)
	startupTick(late, time.Minute)
	if late.startup.state != startupFinished || late.startup.elapsed != startupDuration {
		t.Fatal("a delayed first tick stretched the startup duration")
	}
}

func TestStartupIgnoresStaleAndOutOfOrderTicks(t *testing.T) {
	m := startupModel(t)
	startupTick(m, 1200*time.Millisecond)
	startupTick(m, 900*time.Millisecond)
	if m.startup.elapsed != 1200*time.Millisecond {
		t.Fatal("an out-of-order timestamp rewound the intro")
	}
	old := motionMsg{epoch: m.motionEpoch, at: m.startup.started.Add(1500 * time.Millisecond)}
	m.prefs.Theme = "light"
	m.applyAppearance()
	m.Update(old)
	if m.startup.elapsed != 1200*time.Millisecond || m.startup.state != startupPlaying {
		t.Fatal("a theme change replayed the intro or accepted an obsolete tick")
	}
	startupTick(m, startupDuration)
	view, frame := m.welcomeView(), m.frame
	m.Update(old)
	if m.welcomeView() != view || m.frame != frame || m.motionPending {
		t.Fatal("an obsolete tick revived finished artwork")
	}
	m.connecting = true
	if m.ensureMotion() == nil || !m.motionPending {
		t.Fatal("finishing startup disabled ordinary busy-state motion")
	}
	m.Update(old)
	if !m.motionPending {
		t.Fatal("an obsolete startup tick consumed the busy UI's newer tick")
	}
}

func TestStartupDismissalPreservesKeysAndPaste(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.Msg
		want string
	}{
		{"typing", tea.KeyPressMsg{Code: 'h', Text: "h"}, "h"},
		{"paste", tea.PasteMsg{Content: "draft\x1b[2J\ncontinued"}, "draft\ncontinued"},
		{"escape", keyPress("esc"), ""},
		{"command", tea.KeyPressMsg{Code: '/', Text: "/"}, "/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := startupModel(t)
			tick := motionMsg{epoch: m.motionEpoch, at: m.startup.started.Add(time.Second)}
			m.Update(tc.msg)
			if m.startup.state != startupFinished || m.composer.Value() != tc.want {
				t.Fatalf("dismissal ate or altered input: state=%d draft=%q", m.startup.state, m.composer.Value())
			}
			m.Update(tick)
			if m.startup.state != startupFinished || m.composer.Value() != tc.want {
				t.Fatal("a late tick changed dismissed startup or its draft")
			}
			if tc.name == "command" && !m.paletteOpen {
				t.Fatal("intro swallowed command-palette activation")
			}
		})
	}
}

func TestStartupDoesNotSwallowCtrlCOrDialogs(t *testing.T) {
	m := startupModel(t)
	m.Update(keyPress("ctrl+c"))
	if !m.quitting || m.startup.state != startupFinished {
		t.Fatal("Ctrl+C only skipped the intro instead of retaining idle exit")
	}

	m, _ = readyModel(t)
	m.prefs.ReducedMotion = false
	m.startup = startupAnimation{state: startupPlaying}
	m.turn = true
	m.session = engine.Session{ID: "active"}
	m.Update(keyPress("ctrl+c"))
	if !m.canceling || m.turn || m.quitting || m.startup.state != startupFinished {
		t.Fatal("intro changed the active-turn cancellation protocol")
	}

	dialog := startupModel(t)
	dialog.Update(tea.KeyPressMsg{Code: tea.KeyF1})
	if dialog.overlay == nil || dialog.overlay.kind != dialogHelp || dialog.startup.state != startupFinished {
		t.Fatal("help did not take priority over startup")
	}
	dialog.closeDialog()
	dialog.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if dialog.startup.state != startupFinished || dialog.canAnimate() {
		t.Fatal("closing a dialog replayed the intro")
	}
}

func TestStartupErrorsAndConversationChangesRetireTheIntro(t *testing.T) {
	for _, action := range []string{"error", "conversation", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			m := startupModel(t)
			switch action {
			case "error":
				m.report("A visible startup failure", true)
				if !strings.Contains(m.View().Content, "A visible startup failure") {
					t.Fatal("startup obscured an actionable failure")
				}
			case "conversation":
				m.session = engine.Session{ID: "restored"}
				m.Update(statusMsg{})
			case "shutdown":
				if err := m.Shutdown(); err != nil {
					t.Fatal(err)
				}
				startupTick(m, time.Second)
			}
			if m.startup.state != startupFinished || m.ensureMotion() != nil {
				t.Fatal("intro survived a lifecycle boundary")
			}
			m.resetConversation()
			m.notice = notice{}
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			if m.startup.state != startupFinished {
				t.Fatal("a fresh conversation restarted the process-scoped intro")
			}
		})
	}
}

func TestStartupResizeUsesAdaptiveFallbackWithoutRestarting(t *testing.T) {
	m := startupModel(t)
	startupTick(m, time.Second)
	started := m.startup.started
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	if m.startup.state != startupPlaying || m.startup.started != started || m.startup.elapsed != time.Second {
		t.Fatal("a fitting resize restarted the timeline")
	}
	m.Update(tea.WindowSizeMsg{})
	if m.startup.state != startupPlaying || m.width != 140 || m.height != 30 {
		t.Fatal("an unavailable terminal size discarded a valid layout or stopped the intro")
	}
	m.Update(tea.WindowSizeMsg{Width: 44, Height: 24})
	if m.startup.state != startupPlaying || m.startup.started != started || m.startup.elapsed != time.Second {
		t.Fatal("a compact fallback stopped or restarted the intro")
	}
	compact := m.welcomeView()
	startupTick(m, 1400*time.Millisecond)
	if compact == m.welcomeView() || !strings.Contains(compact, "SODAPOP") || strings.Contains(compact, "POP!") {
		t.Fatal("narrow terminal did not use the animated compact mascot")
	}
	m.Update(tea.WindowSizeMsg{Width: 8, Height: 3})
	if m.startup.state != startupFinished || m.ensureMotion() != nil {
		t.Fatal("a terminal too small for the mascot continued decorative animation")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.startup.state != startupFinished {
		t.Fatal("expanding the terminal replayed a skipped intro")
	}

	options := testOptions()
	options.Preferences.ReducedMotion = false
	small := testModel(t, options)
	small.Update(tea.WindowSizeMsg{Width: 8, Height: 3})
	if small.startup.state != startupFinished || small.motionPending {
		t.Fatal("initially tiny terminal began decorative animation")
	}
	assertBounds(t, small.View(), 8, 3)
}

func TestCompactStartupBeginsAndSodapopesAtInitialSize(t *testing.T) {
	options := testOptions()
	options.Preferences.ReducedMotion = false
	m := testModel(t, options)
	m.now = func() time.Time { return time.Unix(1000, 0) }
	m.Update(tea.WindowSizeMsg{Width: 44, Height: 24})
	if m.startup.state != startupPlaying || !m.compactStartupFits() || m.startupFits() {
		t.Fatal("compact-capable startup did not begin its adaptive animation")
	}
	sealed := m.welcomeView()
	startupTick(m, 1400*time.Millisecond)
	sodapoping := m.welcomeView()
	if sealed == sodapoping || !strings.Contains(sodapoping, "SODAPOP") {
		t.Fatal("compact startup did not visibly advance through its sodapoping pose")
	}
	startupTick(m, startupDuration)
	if m.startup.state != startupFinished || m.canAnimate() {
		t.Fatal("compact startup did not settle at the normal deadline")
	}
}

func TestStartupAppearanceAndNoBannerControls(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	for _, tc := range []struct {
		name     string
		noBanner bool
		reduced  bool
	}{
		{"reduced-motion", false, true},
		{"no-banner", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := testOptions()
			options.NoBanner = tc.noBanner
			options.Preferences.ReducedMotion = tc.reduced
			m := testModel(t, options)
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			if m.startup.state != startupFinished || m.ensureMotion() != nil {
				t.Fatal("an explicit startup opt-out scheduled animation")
			}
			_, shown := m.mascotWelcome()
			if shown == tc.noBanner {
				t.Fatal("no-banner and reduced-motion did not retain their distinct artwork behavior")
			}
			m.prefs.ReducedMotion = false
			m.applyAppearance()
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			if m.startup.state != startupFinished || m.ensureMotion() != nil {
				t.Fatal("changing preferences replayed an opted-out intro")
			}
		})
	}
	m := startupModel(t)
	startupTick(m, time.Second)
	old := motionMsg{epoch: m.motionEpoch, at: m.startup.started.Add(1400 * time.Millisecond)}
	m.prefs.ReducedMotion = true
	m.applyAppearance()
	m.Update(old)
	if m.startup.state != startupFinished || m.motionPending || m.frame != 0 {
		t.Fatal("enabling reduced motion failed to retire in-flight animation")
	}
}

func TestStartupParticlesAndFoamFollowTheOpening(t *testing.T) {
	c := colors(config.Preferences{NoColor: true, ASCII: true})
	for _, at := range []time.Duration{0, 800 * time.Millisecond, time.Second - 1, startupDuration, time.Hour} {
		if len(sodapopParticles(at, true)) != 0 || startupFoam(c, at) != "" {
			t.Fatalf("sodapop appeared before opening or after settling at %s", at)
		}
	}
	if !strings.Contains(startupScene(c, 800*time.Millisecond, true), "POP!") {
		t.Fatal("opening pose lost the pop beat")
	}
	if startupFoam(c, 1400*time.Millisecond) == "" || len(sodapopParticles(1400*time.Millisecond, true)) == 0 {
		t.Fatal("opening did not produce both foam and rising bubbles")
	}
	for at := time.Duration(0); at <= startupDuration; at += motionInterval {
		particles := sodapopParticles(at, true)
		if len(particles) > startupParticles || !reflect.DeepEqual(particles, sodapopParticles(at, true)) {
			t.Fatal("particle system is unbounded or nondeterministic")
		}
		for _, particle := range particles {
			if particle.x < 0 || particle.x >= startupSceneWidth || particle.y < 0 || particle.y >= startupSceneRows {
				t.Fatalf("particle escaped scene bounds: %+v", particle)
			}
		}
		if bounce := startupBounce(at); bounce < -1 || bounce > 1 {
			t.Fatalf("can recoil escaped its reserved space at %s: %d", at, bounce)
		}
	}
	if startupBounce(0) != 1 || startupBounce(800*time.Millisecond) != -1 || startupBounce(startupDuration) != 0 {
		t.Fatal("arrival, opening recoil, and resting position are not distinct")
	}
	if startupScene(c, 0, false) != startupScene(c, 1400*time.Millisecond, false) {
		t.Fatal("resting scene depends on animation time")
	}
}

func TestCompactStartupEffectsAreBoundedAndAccessible(t *testing.T) {
	for _, mode := range []struct{ noColor, ascii bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
		c := colors(config.Preferences{NoColor: mode.noColor, ASCII: mode.ascii})
		sealed := compactStartupMascot(c, 0, true)
		sodapoping := compactStartupMascot(c, 1400*time.Millisecond, true)
		if sealed == sodapoping {
			t.Fatalf("compact animation did not change frames: %+v", mode)
		}
		for _, frame := range []string{sealed, sodapoping, compactStartupMascot(c, startupDuration, false)} {
			if lipgloss.Width(frame) != compactMascotWidth || lipgloss.Height(frame) != compactMascotHeight {
				t.Fatalf("compact frame escaped bounds: %+v\n%s", mode, frame)
			}
			if mode.noColor && strings.ContainsRune(frame, '\x1b') {
				t.Fatal("no-color compact animation emitted ANSI escapes")
			}
			if mode.ascii {
				for _, char := range ansi.Strip(frame) {
					if char > unicode.MaxASCII {
						t.Fatalf("ASCII compact animation emitted %q", char)
					}
				}
			}
		}
	}
}

func TestStartupSodapopIsAnchoredToTheDrinkingAperture(t *testing.T) {
	c := colors(config.Preferences{NoColor: true, ASCII: true})
	can := strings.Split(mascotFrame(c, mascotOpened, false), "\n")
	if can[mascotOpeningY][mascotOpeningX-1:mascotOpeningX+1] != "##" {
		t.Fatal("the sodapop origin no longer names the can's drinking aperture")
	}
	scene := strings.Split(startupScene(c, time.Second, true), "\n")
	if scene[startupSodapopY][startupSodapopX-1:startupSodapopX+2] != "ooo" {
		t.Fatal("the first foam cap does not emerge from the opened lid")
	}
	for _, p := range sodapopParticles(time.Second, true) {
		if p.x < startupSodapopX-1 || p.x > startupSodapopX+1 || p.y < startupSodapopY-1 || p.y > startupSodapopY {
			t.Fatal("new particles did not originate at the drinking aperture")
		}
	}
}

func TestStartupScenesAreBoundedAcrossThemesAndCharacterModes(t *testing.T) {
	for _, theme := range []string{"arcade", "graphite", "midnight", "high-contrast", "light"} {
		for _, mode := range []struct{ noColor, ascii bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
			c := colors(config.Preferences{Theme: theme, NoColor: mode.noColor, ASCII: mode.ascii})
			for _, at := range []time.Duration{0, 400, 800, 1000, 1200, 1400, 1700, 1900, 2400} {
				scene := startupScene(c, at*time.Millisecond, true)
				lines := strings.Split(scene, "\n")
				if len(lines) != startupSceneRows {
					t.Fatalf("scene height changed: %s %+v %s", theme, mode, at)
				}
				for _, line := range lines {
					if ansi.StringWidth(line) != startupSceneWidth {
						t.Fatalf("scene width changed: %q", line)
					}
				}
				if mode.noColor && strings.ContainsRune(scene, '\x1b') {
					t.Fatal("no-color scene emitted ANSI escapes")
				}
				if mode.ascii {
					for _, char := range ansi.Strip(scene) {
						if char > unicode.MaxASCII {
							t.Fatalf("ASCII scene emitted %q", char)
						}
					}
				}
			}
		}
	}
}

func TestStartupWelcomeKeepsComposerAndAccountGuidanceVisible(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 32}, {64, 24}, {64, 14}, {44, 24}, {20, 6}, {1, 1}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := startupModel(t)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.identityLoading = false
			m.composer.SetValue("visible draft")
			view := m.View()
			assertBounds(t, view, size[0], size[1])
			if size[0] >= 44 && (!strings.Contains(view.Content, "visible draft") || !strings.Contains(view.Content, "/login")) {
				t.Fatal("mascot displaced the composer or sign-in guidance")
			}
			if strings.ContainsRune(view.Content, '\x1b') || view.ForegroundColor != nil || view.BackgroundColor != nil {
				t.Fatal("no-color welcome retained terminal styling")
			}
		})
	}
	m := startupModel(t)
	m.finishStartup()
	m.identityLoading = false
	m.account = auth.Account{ID: "account", Login: "octocat"}
	m.connecting = true
	if copy := m.mascotWelcomeCopy(42); !strings.Contains(copy, "Connecting to Copilot.") || strings.Contains(copy, "Ready when you are") {
		t.Fatal("resting mascot hid an incomplete connection")
	}
	m.connecting = false
	if !strings.Contains(m.mascotWelcomeCopy(42), "/model") {
		t.Fatal("welcome omitted model selection")
	}
	m.model = "model-a"
	if !strings.Contains(m.mascotWelcomeCopy(42), "Ready when you are") {
		t.Fatal("welcome never reflected actual readiness")
	}
	m.opts.Project = "/" + strings.Repeat("a", 200)
	if copy := m.mascotWelcomeCopy(38); lipgloss.Height(copy) > startupSceneRows {
		t.Fatal("long project name displaced welcome actions")
	}
}

type waitingStartupAuth struct {
	auth.Service
	started chan struct{}
}

func (a waitingStartupAuth) Current(ctx context.Context) (auth.Account, error) {
	close(a.started)
	<-ctx.Done()
	return auth.Account{}, ctx.Err()
}

func TestStartupProgramAcceptsInputWhileIdentityIsBlocked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	authStarted, showing, drafted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	options := testOptions()
	options.Context = ctx
	options.Preferences.ReducedMotion = false
	options.Auth = waitingStartupAuth{Service: &fakeAuth{}, started: authStarted}
	m := testModel(t, options)
	var showingOnce, draftedOnce sync.Once
	observed := &observingModel{Model: m, observe: func() {
		if m.startup.state == startupPlaying && m.identityLoading {
			showingOnce.Do(func() { close(showing) })
		}
		if m.composer.Value() == "an immediate draft" && m.startup.state == startupFinished && m.identityLoading {
			draftedOnce.Do(func() { close(drafted) })
		}
	}}
	program := tea.NewProgram(observed, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(io.Discard),
		tea.WithoutRenderer(), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	wait := func(ch <-chan struct{}, phase string) {
		t.Helper()
		select {
		case <-ch:
		case err := <-done:
			t.Fatalf("program ended during %s: %v", phase, err)
		case <-ctx.Done():
			t.Fatalf("startup blocked %s", phase)
		}
	}

	wait(authStarted, "identity discovery")
	program.Send(tea.WindowSizeMsg{Width: 80, Height: 24})
	wait(showing, "intro display")
	program.Send(tea.PasteMsg{Content: "an immediate draft"})
	wait(drafted, "input")
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, tea.ErrProgramKilled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("program did not release startup and identity work")
	}
}

func BenchmarkStartupWelcome(b *testing.B) {
	m := New(Options{Project: "/project", Preferences: config.DefaultPreferences()})
	b.Cleanup(func() { _ = m.Shutdown() })
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.startup.elapsed = 1400 * time.Millisecond
	b.ReportAllocs()
	for b.Loop() {
		m.View()
	}
}
