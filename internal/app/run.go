package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/config"
	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/VeVarunSharma/sodapop/internal/runtimebundle"
	"github.com/VeVarunSharma/sodapop/internal/skills"
	"github.com/VeVarunSharma/sodapop/internal/ui"
	"github.com/VeVarunSharma/sodapop/internal/workspace"
)

var (
	Version       = "dev"
	OAuthClientID string
)

type applicationModel interface {
	tea.Model
	Shutdown() error
}

type applicationAuth interface {
	auth.Service
	TokenForAccount(context.Context, string) (string, error)
}

type runDependencies struct {
	isTerminal      func(uintptr) bool
	checkRuntime    func(context.Context) (string, error)
	resolvePaths    func() (config.Paths, error)
	loadPreferences func(string) (config.Preferences, error)
	savePreferences func(string, config.Preferences) error
	loadMCP         func(string) (config.MCPRegistry, error)
	saveMCP         func(string, config.MCPRegistry) error
	getwd           func() (string, error)
	evalSymlinks    func(string) (string, error)
	newWorkspace    func(string) (ui.Workspace, error)
	newAuth         func(string) applicationAuth
	newEngine       func(engine.Config) (engine.Engine, error)
	newModel        func(ui.Options) applicationModel
	runProgram      func(context.Context, applicationModel, *os.File, io.Writer) error
}

func defaultRunDependencies() runDependencies {
	return runDependencies{
		isTerminal:      term.IsTerminal,
		checkRuntime:    runtimebundle.Check,
		resolvePaths:    config.ResolvePaths,
		loadPreferences: config.Load,
		savePreferences: config.Save,
		loadMCP:         config.LoadMCP,
		saveMCP:         config.SaveMCP,
		getwd:           os.Getwd,
		evalSymlinks:    filepath.EvalSymlinks,
		newWorkspace: func(project string) (ui.Workspace, error) {
			return workspace.New(project)
		},
		newAuth: func(clientID string) applicationAuth {
			return auth.New(clientID)
		},
		newEngine: func(cfg engine.Config) (engine.Engine, error) {
			return engine.New(cfg)
		},
		newModel: func(options ui.Options) applicationModel {
			return ui.New(options)
		},
		runProgram: func(ctx context.Context, model applicationModel, input *os.File, output io.Writer) error {
			_, err := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output)).Run()
			return err
		},
	}
}

func Run(ctx context.Context, args []string, input *os.File, output io.Writer) (err error) {
	return run(ctx, args, input, output, defaultRunDependencies())
}

func run(ctx context.Context, args []string, input *os.File, output io.Writer, deps runDependencies) (err error) {
	flags := flag.NewFlagSet("sodapop", flag.ContinueOnError)
	flags.SetOutput(output)
	var help, version, noColor, reducedMotion, ascii, noBanner, checkRuntime bool
	flags.BoolVar(&help, "help", false, "show help without starting the agent")
	flags.BoolVar(&help, "h", false, "show help")
	flags.BoolVar(&version, "version", false, "show Sodapop, SDK, and runtime versions")
	flags.BoolVar(&version, "v", false, "show versions")
	flags.BoolVar(&noColor, "no-color", false, "disable terminal colors")
	flags.BoolVar(&reducedMotion, "reduced-motion", false, "disable decorative animation")
	flags.BoolVar(&ascii, "ascii", false, "use ASCII terminal presentation")
	flags.BoolVar(&noBanner, "no-banner", false, "skip the startup mascot banner")
	flags.BoolVar(&checkRuntime, "check-runtime", false, "check the bundled runtime without signing in or calling a model")
	flags.Usage = func() {
		fmt.Fprintln(output, "Sodapop - a neon terminal coding companion")
		fmt.Fprintln(output, "\nUsage: sodapop [options]")
		fmt.Fprintln(output, "\nRun sodapop from your project directory. Type / for commands or /login for your account.")
		fmt.Fprintln(output, "\nCommands: /help /login /logout /model /clear /resume /compact /plan /autopilot /mcp /skill /diff /theme /exit")
		fmt.Fprintln(output, "\nSign-in uses Sodapop's own GitHub OAuth device flow. Development builds need")
		fmt.Fprintln(output, "SODAPOP_GITHUB_CLIENT_ID set to a registered, device-flow-enabled public client ID.")
		fmt.Fprintln(output, "\nOptions:")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("Sodapop accepts no positional arguments; launch it in your project directory")
	}
	if help {
		flags.Usage()
		return nil
	}
	if version {
		fmt.Fprintf(output, "sodapop %s\nCopilot SDK %s / runtime %s\n", Version, runtimebundle.SDKVersion, runtimebundle.Version)
		return nil
	}
	if checkRuntime {
		checkCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		result, err := deps.checkRuntime(checkCtx)
		if err != nil {
			return err
		}
		fmt.Fprintln(output, result)
		return nil
	}
	terminal, ok := output.(*os.File)
	if !ok || input == nil || !deps.isTerminal(input.Fd()) || !deps.isTerminal(terminal.Fd()) {
		return errors.New("Sodapop needs an interactive terminal; use --help, --version, or --check-runtime for non-interactive output")
	}
	paths, err := deps.resolvePaths()
	if err != nil {
		return err
	}
	prefs, err := deps.loadPreferences(paths.ConfigFile)
	if err != nil {
		return fmt.Errorf("%s: %w", paths.ConfigFile, err)
	}
	saved := prefs
	noColor = noColor || os.Getenv("NO_COLOR") != ""
	prefs.NoColor = prefs.NoColor || noColor
	prefs.ReducedMotion = prefs.ReducedMotion || reducedMotion
	prefs.ASCII = prefs.ASCII || ascii || os.Getenv("TERM") == "dumb"
	cwd, err := deps.getwd()
	if err != nil {
		return fmt.Errorf("locate current project directory: %w", err)
	}
	project, err := deps.evalSymlinks(cwd)
	if err != nil {
		return fmt.Errorf("resolve current project directory: %w", err)
	}
	projectFiles, err := deps.newWorkspace(project)
	if err != nil {
		return err
	}
	skillManager, err := skills.New(paths.StateDir, project)
	if err != nil {
		return err
	}
	clientID := strings.TrimSpace(OAuthClientID)
	if value, present := os.LookupEnv("SODAPOP_GITHUB_CLIENT_ID"); present {
		clientID = strings.TrimSpace(value)
	}
	identity := deps.newAuth(clientID)
	model := deps.newModel(ui.Options{
		Context:     ctx,
		Project:     project,
		Version:     Version,
		NoBanner:    noBanner,
		Preferences: prefs,
		Auth:        identity,
		Workspace:   projectFiles,
		SavePreferences: func(next config.Preferences) error {
			if noColor {
				next.NoColor = saved.NoColor
			}
			if reducedMotion {
				next.ReducedMotion = saved.ReducedMotion
			}
			if ascii || os.Getenv("TERM") == "dumb" {
				next.ASCII = saved.ASCII
			}
			return deps.savePreferences(paths.ConfigFile, next)
		},
		LoadMCP: func(_ context.Context, account auth.Account) (config.MCPRegistry, error) {
			path, err := paths.AccountMCPFile(account.ID)
			if err != nil {
				return config.MCPRegistry{}, err
			}
			return deps.loadMCP(path)
		},
		SaveMCP: func(_ context.Context, account auth.Account, registry config.MCPRegistry) error {
			path, err := paths.AccountMCPFile(account.ID)
			if err != nil {
				return err
			}
			return deps.saveMCP(path, registry)
		},
		ManageSkills: skillManager.Apply,
		NewEngine: func(engineCtx context.Context, account auth.Account) (engine.Engine, error) {
			home, err := paths.AccountHome(account.ID)
			if err != nil {
				return nil, err
			}
			mcpPath, err := paths.AccountMCPFile(account.ID)
			if err != nil {
				return nil, err
			}
			registry, err := deps.loadMCP(mcpPath)
			if err != nil {
				return nil, err
			}
			servers, err := resolveMCPServers(registry)
			if err != nil {
				return nil, err
			}
			installed, active, err := skillManager.Resolve()
			if err != nil {
				return nil, err
			}
			engineSkills := make([]engine.Skill, 0, len(installed))
			for _, skill := range installed {
				engineSkills = append(engineSkills, engine.Skill{
					Name: skill.Name, Digest: skill.Digest, Directory: skill.Directory,
				})
			}
			backend, err := deps.newEngine(engine.Config{
				Project: project, Home: home, AccountID: account.ID,
				MCPServers: servers, Skills: engineSkills, ActiveSkillDigests: active,
				TokenSource: func(tokenCtx context.Context) (string, error) {
					return identity.TokenForAccount(tokenCtx, account.ID)
				},
			})
			if err != nil {
				return nil, err
			}
			if err := backend.Start(engineCtx); err != nil {
				return nil, errors.Join(err, backend.Close())
			}
			return backend, nil
		},
	})
	defer func() {
		err = errors.Join(err, model.Shutdown())
	}()
	err = deps.runProgram(ctx, model, input, output)
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, tea.ErrProgramKilled)) {
		return nil
	}
	return err
}

func resolveMCPServers(registry config.MCPRegistry) ([]engine.MCPServer, error) {
	servers := make([]engine.MCPServer, 0, len(registry.Servers))
	for _, configured := range registry.Servers {
		if !configured.Enabled {
			continue
		}
		env := make(map[string]string, len(configured.Env))
		for _, name := range configured.Env {
			value, present := os.LookupEnv(name)
			if !present {
				return nil, fmt.Errorf("MCP server %q requires environment variable %s", configured.Name, name)
			}
			env[name] = value
		}
		servers = append(servers, engine.MCPServer{
			Name: configured.Name, Command: configured.Command,
			Args: append([]string(nil), configured.Args...), Env: env,
			Tools:          append([]string(nil), configured.Tools...),
			TimeoutSeconds: configured.TimeoutSeconds,
		})
	}
	return servers, nil
}
