// The recording launcher uses the production UI without account or runtime access.
// It is development tooling, not part of the distributed sodapop executable.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/config"
	"github.com/VeVarunSharma/sodapop/internal/ui"
	"github.com/VeVarunSharma/sodapop/internal/workspace"
)

var errRecordingSignIn = errors.New("sign-in is disabled in the local-only recording launcher")

type signedOut struct{}

func recordingAuthError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return auth.ErrNotSignedIn
}

func (signedOut) Current(ctx context.Context) (auth.Account, error) {
	return auth.Account{}, recordingAuthError(ctx)
}

func (signedOut) Token(ctx context.Context) (string, error) {
	return "", recordingAuthError(ctx)
}

func (signedOut) Login(ctx context.Context, _ bool, _ func(auth.DeviceCode)) (auth.Account, error) {
	if err := ctx.Err(); err != nil {
		return auth.Account{}, err
	}
	return auth.Account{}, errRecordingSignIn
}

func (signedOut) SignOut(ctx context.Context) error {
	return recordingAuthError(ctx)
}

func recordingOptions(ctx context.Context, project string, reducedMotion bool) (ui.Options, error) {
	absolute, err := filepath.Abs(project)
	if err != nil {
		return ui.Options{}, fmt.Errorf("locate demo project: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return ui.Options{}, fmt.Errorf("resolve demo project: %w", err)
	}
	files, err := workspace.New(canonical)
	if err != nil {
		return ui.Options{}, err
	}
	prefs := config.DefaultPreferences()
	prefs.ReducedMotion = reducedMotion
	return ui.Options{
		Context: ctx, Project: canonical, Version: "demo",
		Preferences: prefs, Auth: signedOut{}, Workspace: files,
		// Nil engine/persistence callbacks intentionally keep this UI local and ephemeral.
	}, nil
}

func run(ctx context.Context, args []string, project string, output io.Writer, start func(context.Context, *ui.Model) error) (err error) {
	flags := flag.NewFlagSet("sodapop-recording", flag.ContinueOnError)
	flags.SetOutput(output)
	reducedMotion := flags.Bool("reduced-motion", false, "show the resting mascot and disable decorative motion")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("the recording launcher accepts no positional arguments")
	}
	options, err := recordingOptions(ctx, project, *reducedMotion)
	if err != nil {
		return err
	}
	model := ui.New(options)
	defer func() {
		err = errors.Join(err, model.Shutdown())
	}()
	err = start(ctx, model)
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, tea.ErrProgramKilled)) {
		return nil
	}
	return err
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	err := run(ctx, os.Args[1:], ".", os.Stderr, func(ctx context.Context, model *ui.Model) error {
		_, err := tea.NewProgram(model, tea.WithContext(ctx)).Run()
		return err
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sodapop-recording:", err)
		os.Exit(1)
	}
}
