package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	wfapi "workflowfiesta-runner/internal/api"
	"workflowfiesta-runner/internal/config"
	"workflowfiesta-runner/internal/localconfig"
	"workflowfiesta-runner/internal/localui"
	"workflowfiesta-runner/internal/platform"
	"workflowfiesta-runner/internal/runner"
)

var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "workflowfiesta-runner",
	Short: "WorkflowFiesta self-hosted runner",
}

// ── ANSI helpers (no-bright variants so text is darker / more readable) ───────

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiGray   = "\033[90m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiCyan   = "\033[36m"
)

// darkFormatter formats logrus entries with dark, readable ANSI colors.
type darkFormatter struct{}

func (f *darkFormatter) Format(entry *log.Entry) ([]byte, error) {
	ts := entry.Time.Format("15:04:05")
	lvl := strings.ToUpper(entry.Level.String())
	if len(lvl) > 4 {
		lvl = lvl[:4]
	}

	var lvlColor, msgBold string
	switch entry.Level {
	case log.ErrorLevel, log.FatalLevel, log.PanicLevel:
		lvlColor = ansiRed
		msgBold = ansiBold
	case log.WarnLevel:
		lvlColor = ansiYellow
	default:
		lvlColor = ansiCyan
	}

	line := fmt.Sprintf("%s%s%s %s%s%s %s%s%s\n",
		ansiGray, ts, ansiReset,
		lvlColor, lvl, ansiReset,
		msgBold, entry.Message, ansiReset,
	)
	return []byte(line), nil
}

// ── cliSink ────────────────────────────────────────────────────────────────────

// cliSink is a StatusSink that prints ASCII banners for lifecycle events and
// streams raw job output to stdout.
type cliSink struct {
	apiURL string
	name   string
}

func (s *cliSink) SetConnecting() {
	fmt.Printf("\n%s●%s Connecting  %s%s → %s%s\n",
		ansiGray, ansiReset, ansiGray, s.name, s.apiURL, ansiReset)
}

func (s *cliSink) SetReconnecting() {
	fmt.Printf("\n%s●%s Reconnecting  %s%s → %s%s\n",
		ansiYellow, ansiReset, ansiGray, s.name, s.apiURL, ansiReset)
}

func (s *cliSink) SetConnected(connected bool) {
	if connected {
		fmt.Printf("\n%s●%s Connected  %s%s → %s%s\n\n",
			ansiGreen, ansiReset, ansiGray, s.name, s.apiURL, ansiReset)
	} else {
		fmt.Printf("\n%s●%s Disconnected\n\n", ansiYellow, ansiReset)
	}
}

func (s *cliSink) SetIdle() {
	fmt.Printf("%s%s%s\n\n", ansiGray, strings.Repeat("─", 66), ansiReset)
}

func (s *cliSink) SetJobRunning(jobID, image, scriptBlurb string) {
	w := 62 // inner content width
	rule := strings.Repeat("─", w+2)

	row := func(label, value string) string {
		// label is padded to 8 chars; value fills remaining space
		content := fmt.Sprintf("  %-8s %s", label+":", truncateCLI(value, w-11))
		pad := w + 2 - len([]rune(content))
		if pad < 0 {
			pad = 0
		}
		return fmt.Sprintf("│%s%s│", content, strings.Repeat(" ", pad))
	}

	fmt.Printf("\n%s┌%s┐%s\n", ansiBold, rule, ansiReset)
	fmt.Printf("%s│%s  ► JOB RECEIVED%-*s%s│%s\n",
		ansiBold, ansiReset, w-13, "", ansiBold, ansiReset)
	fmt.Printf("%s│%s  Source  : %-*s%s│%s\n",
		ansiBold, ansiReset, w-12, truncateCLI(s.name+" @ "+s.apiURL, w-12), ansiBold, ansiReset)
	fmt.Printf("%s├%s┤%s\n", ansiBold, rule, ansiReset)
	fmt.Println(row("Job", jobID))
	fmt.Println(row("Image", image))
	fmt.Println(row("Script", scriptBlurb))
	fmt.Printf("%s└%s┘%s\n\n", ansiBold, rule, ansiReset)
}

func (s *cliSink) SetJobComplete(_, _ string, exitCode int) {
	if exitCode == 0 {
		fmt.Printf("%s✓ passed%s\n", ansiGreen, ansiReset)
	} else {
		fmt.Printf("%s✗ failed (exit %d)%s\n", ansiRed, exitCode, ansiReset)
	}
}

func (s *cliSink) AppendLog(chunk string) {
	fmt.Fprint(os.Stdout, chunk)
}

func truncateCLI(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// applyNamedAuditLog updates localCfg.AuditLog to audit-{name}-{id}.log when
// the runner is registered and the path is still the generic default.
// Saves runner.yaml on change so the named path persists across restarts.
func applyNamedAuditLog(cfg *config.Config, localCfg *localconfig.LocalConfig, configPath string) {
	if cfg == nil || localCfg == nil {
		return
	}
	if localCfg.UpdateAuditLogForRunner(cfg.Name, cfg.RunnerID) {
		if err := localconfig.Save(localCfg, configPath); err != nil {
			log.Warnf("[runner] update audit log path in config: %v", err)
		}
	}
}

func auditLogPaths(cfg *config.Config) []string {
	if cfg == nil || cfg.LocalConfig == nil {
		return config.AuditLogPathsFromConfig("")
	}
	return config.AuditLogPathsFromConfig(cfg.LocalConfig.AuditLog)
}

func relaunchRunnerApp() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	cmd := exec.Command(exePath)
	cmd.Env = filteredEnvForRelaunch(os.Environ())
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("relaunch app: %w", err)
	}

	_ = cmd.Process.Release()
	return nil
}

// buildRegistrationRevokedHandler clears local credentials when the server
// rejects the runner token (e.g. runner deleted in the admin UI).
// This handler intentionally swallows errors (log.Warn only) rather than
// returning them. It fires during an automatic server-side logout where the app
// is already shutting down — there is no UI context to surface errors to, unlike
// buildClearConfigurationHandler which is user-initiated and returns errors for
// display.
func buildRegistrationRevokedHandler(cfg *config.Config, configPath string, onStop func()) func(reason string) {
	auditPaths := auditLogPaths(cfg)
	return func(reason string) {
		log.Warn("[runner] runner registration is no longer valid on the server — logging out locally")
		if cfg != nil && cfg.LocalConfig != nil {
			config.WriteRevocationEvent(cfg.LocalConfig.AuditLog, reason)
		}
		if err := config.ClearLocalState(configPath, false, false, auditPaths); err != nil {
			log.Warnf("[runner] clear local state: %v", err)
		}
		if onStop != nil {
			onStop()
		}
		if err := relaunchRunnerApp(); err != nil {
			log.Warnf("[runner] relaunch: %v", err)
		}
		localui.QuitApp()
	}
}

func newRunnerWithLogout(cfg *config.Config, configPath string, onStop func()) *runner.Runner {
	return runner.New(cfg).WithOnRegistrationRevoked(buildRegistrationRevokedHandler(cfg, configPath, onStop))
}

func runRunner(ctx context.Context, r *runner.Runner) error {
	err := r.Run(ctx)
	if errors.Is(err, runner.ErrRegistrationRevoked) {
		log.Info("[runner] logged out — register again to continue")
		return nil
	}
	return err
}

func buildClearConfigurationHandler(cfg *config.Config, configPath string, onStop func()) func(deleteAuditLogs, deleteScripts bool) error {
	auditPaths := auditLogPaths(cfg)
	return func(deleteAuditLogs, deleteScripts bool) error {
		// Best-effort: unregister the runner on the server first.
		if cfg != nil && cfg.APIURL != "" && cfg.Token != "" && cfg.RunnerID != "" {
			client := wfapi.New(cfg.APIURL, cfg.Token)
			if cfg.LocalConfig != nil && cfg.LocalConfig.OrgID != "" {
				client.SetOrgID(cfg.LocalConfig.OrgID)
			}
			if err := client.DeleteRunner(cfg.RunnerID); err != nil {
				// Best-effort only: local reset/relaunch must continue even if server reset fails.
				log.Warnf("reset runner: delete on server failed: %v", err)
			}
		}

		var clearErr, relaunchErr error
		if clearErr = config.ClearLocalState(configPath, deleteAuditLogs, deleteScripts, auditPaths); clearErr != nil {
			log.Warnf("reset runner: clear local state: %v", clearErr)
		}
		if relaunchErr = relaunchRunnerApp(); relaunchErr != nil {
			log.Warnf("reset runner: relaunch: %v", relaunchErr)
		}
		if err := errors.Join(clearErr, relaunchErr); err != nil {
			return err
		}
		if onStop != nil {
			onStop()
		}
		localui.QuitApp()
		return nil
	}
}

func filteredEnvForRelaunch(env []string) []string {
	drop := map[string]struct{}{
		"WORKFLOWFIESTA_TOKEN":       {},
		"WORKFLOWFIESTA_RUNNER_ID":   {},
		"WORKFLOWFIESTA_RUNNER_NAME": {},
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if _, blocked := drop[key]; blocked {
			continue
		}
		out = append(out, kv)
	}
	return out
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the runner and connect to WorkflowFiesta",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()

		if cfg.Token == "" {
			return fmt.Errorf("WORKFLOWFIESTA_TOKEN is required")
		}
		cfg.Version = version

		log.Infof("Starting WorkflowFiesta runner (version %s)", version)
		log.Infof("API URL: %s", cfg.APIURL)
		log.Infof("Runner name: %s", cfg.Name)
		log.Infof("Executor type: %s", cfg.ExecutorType)
		if cfg.ExecutorType == "docker" {
			log.Infof("Docker socket: %s", cfg.DockerSocket)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigs
			log.Info("Shutting down...")
			cancel()
		}()

		configPath := localconfig.DefaultPath()
		r := newRunnerWithLogout(cfg, configPath, cancel).WithSink(&cliSink{apiURL: cfg.APIURL, name: cfg.Name})
		return runRunner(ctx, r)
	},
}

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Register this runner using a one-time code from WorkflowFiesta",
	Long: `Register this machine as a self-hosted runner.

Get a code by visiting your WorkflowFiesta instance → Runners → "Set up a runner",
or by asking any platform agent in chat: "create a runner registration code". The
runner's name was already chosen at code-issuance time and shows up in the runners
list immediately.

The code embeds your organization, so you only need ONE thing: the code.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		code, _ := cmd.Flags().GetString("code")
		apiURL, _ := cmd.Flags().GetString("api-url")

		if code == "" {
			return fmt.Errorf("--code is required (get one from your WorkflowFiesta instance → Runners → Set up a runner)")
		}
		if apiURL == "" {
			apiURL = config.DefaultAPIURL
		}
		apiURL = strings.TrimRight(apiURL, "/")

		body, _ := json.Marshal(map[string]string{
			"code":          code,
			"documentsPath": platform.DocumentsPath(),
			"shell":         platform.Shell(),
		})

		resp, err := http.Post(apiURL+"/api/runner/register", "application/json", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("registration request failed: %w", err)
		}
		defer resp.Body.Close()

		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusCreated {
			// Try to extract a friendly error message
			var errBody struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(data, &errBody)
			if errBody.Error != "" {
				return fmt.Errorf("registration failed (%d): %s", resp.StatusCode, errBody.Error)
			}
			return fmt.Errorf("registration failed (%d): %s", resp.StatusCode, data)
		}

		var result struct {
			UID            string `json:"uid"`
			Token          string `json:"token"`
			OrgUID         string `json:"orgUid"`
			Name           string `json:"name"`
			EnvironmentUID string `json:"environmentUid"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("could not parse server response: %w", err)
		}

		fmt.Printf("✓ Runner registered: %s\n", result.Name)
		fmt.Printf("  UID: %s\n\n", result.UID)
		fmt.Println("Set these environment variables (or save them to ~/.workflowfiesta/credentials.env):")
		fmt.Printf("  export WORKFLOWFIESTA_API_URL=%s\n", apiURL)
		fmt.Printf("  export WORKFLOWFIESTA_TOKEN=%s\n", result.Token)
		fmt.Printf("  export WORKFLOWFIESTA_RUNNER_ID=%s\n", result.UID)
		fmt.Printf("  export WORKFLOWFIESTA_RUNNER_NAME=%s\n\n", result.Name)
		fmt.Println("Then run: workflowfiesta-runner run")

		return nil
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("workflowfiesta-runner %s\n", version)
	},
}

var runLocalCmd = &cobra.Command{
	Use:   "run-local",
	Short: "Run the runner in local executor mode (scripts run directly on this machine)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()

		if cfg.Token == "" {
			return fmt.Errorf("WORKFLOWFIESTA_TOKEN is required")
		}
		cfg.Version = version

		configPath, _ := cmd.Flags().GetString("config")
		headless, _ := cmd.Flags().GetBool("headless")

		if configPath == "" {
			configPath = localconfig.DefaultPath()
		}

		localCfg, err := localconfig.Load(configPath)
		if err != nil {
			return fmt.Errorf("load local config: %w", err)
		}
		localCfg.Headless = headless
		localui.Headless = headless

		cfg.ExecutorType = "local"
		cfg.LocalConfig = localCfg
		applyNamedAuditLog(cfg, localCfg, configPath)

		log.Infof("Starting WorkflowFiesta runner in local mode (version %s)", version)
		log.Infof("API URL: %s", cfg.APIURL)
		log.Infof("Runner name: %s", cfg.Name)
		log.Infof("Allowed paths: %v", localCfg.AllowedPaths)
		log.Infof("Working dir: %s", localCfg.WorkingDir())
		log.Infof("Confirm: %s  Network: %s  Sandbox: %s", localCfg.Confirm, localCfg.Network, localCfg.Sandbox)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigs
			log.Info("Shutting down...")
			cancel()
			localui.QuitApp()
		}()

		if headless {
			r := newRunnerWithLogout(cfg, configPath, cancel).WithSink(&cliSink{apiURL: cfg.APIURL, name: cfg.Name})
			return runRunner(ctx, r)
		}

		// GUI mode: open status window + system tray.
		// The runner runs in a goroutine; Fyne event loop on main thread.
		// macOS requires the GUI event loop on the main OS thread.
		r := newRunnerWithLogout(cfg, configPath, cancel)
		sw := localui.NewStatusWindow(cfg.Name, cfg.APIURL)
		sw.SetStopAgentHandler(r.StopAgentJob)
		// Wire up the settings gear button on the status window.
		sw.SetOnOpenSettings(func() {
			localui.OpenSettingsWindow(localCfg, localconfig.DefaultPath(), func(updated *localconfig.LocalConfig) {
				cfg.LocalConfig = updated
				localCfg = updated
			}, buildClearConfigurationHandler(cfg, configPath, cancel))
		})
		// Show before a.Run() — must be a direct call, not via fyne.Do,
		// because the Fyne event loop hasn't started yet.
		sw.Show()

		go func() {
			if err := runRunner(ctx, r.WithSink(sw)); err != nil {
				log.Errorf("Runner stopped: %v", err)
			}
			localui.QuitApp()
		}()

		localui.StartTray(
			cfg.Name,
			cancel,
			buildClearConfigurationHandler(cfg, configPath, cancel),
			sw,
			localCfg,
			func(updated *localconfig.LocalConfig) {
				cfg.LocalConfig = updated
			},
		) // Blocks until app.Quit().
		return nil
	},
}

var initLocalCmd = &cobra.Command{
	Use:   "init-local",
	Short: "Interactive setup wizard for local executor mode",
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, _ := cmd.Flags().GetString("config")
		if configPath == "" {
			configPath = localconfig.DefaultPath()
		}
		return localui.RunWizard(configPath)
	},
}

var registerLocalCmd = &cobra.Command{
	Use:   "register-local",
	Short: "Register a new runner and configure local executor mode (GUI wizard)",
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, _ := cmd.Flags().GetString("config")
		if configPath == "" {
			configPath = localconfig.DefaultPath()
		}

		result, err := localui.RunRegisterWizard(configPath)
		if err != nil {
			return err
		}
		if result == nil {
			return nil // wizard cancelled
		}

		fmt.Printf("\nRunner registered!\n")
		fmt.Printf("Runner UID: %s\n", result.RunnerUID)
		fmt.Printf("Runner    : %s\n", result.RunnerName)
		fmt.Printf("\nCredentials saved to ~/.workflowfiesta/credentials.env\n")
		fmt.Printf("Start the runner with:\n\n  source ~/.workflowfiesta/credentials.env\n  workflowfiesta-runner run-local\n\n")
		return nil
	},
}

func init() {
	registerCmd.Flags().String("code", "", "One-time registration code from WorkflowFiesta (required)")
	registerCmd.Flags().String("api-url", "", "WorkflowFiesta API URL (defaults to "+config.DefaultAPIURL+", or $WORKFLOWFIESTA_API_URL)")

	runLocalCmd.Flags().Bool("headless", false, "Skip GUI; use terminal y/n prompts (for SSH/CI use)")
	runLocalCmd.Flags().String("config", "", "Path to runner.yaml (default: ~/.workflowfiesta/runner.yaml)")

	initLocalCmd.Flags().String("config", "", "Path to write runner.yaml (default: ~/.workflowfiesta/runner.yaml)")
	registerLocalCmd.Flags().String("config", "", "Path to write runner.yaml (default: ~/.workflowfiesta/runner.yaml)")

	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(runLocalCmd)
	rootCmd.AddCommand(initLocalCmd)
	rootCmd.AddCommand(registerLocalCmd)
	rootCmd.AddCommand(registerCmd)
	rootCmd.AddCommand(versionCmd)
}

func main() {
	log.SetFormatter(&darkFormatter{})

	// No arguments: user double-clicked the binary.
	if len(os.Args) == 1 {
		if !localui.HasGUI {
			// Headless build has no wizard. Print a helpful message and pause
			// so the console window doesn't disappear before the user can read it.
			fmt.Println("WorkflowFiesta Runner — headless build")
			fmt.Println()
			fmt.Println("This binary is for servers and CI pipelines. It has no setup wizard.")
			fmt.Println("For the desktop installer with a GUI wizard, download:")
			fmt.Println("  workflowfiesta-runner-windows-amd64-gui.exe")
			fmt.Println()
			fmt.Println("To run headless, set environment variables and use a subcommand:")
			fmt.Println("  WORKFLOWFIESTA_TOKEN=<token> workflowfiesta-runner run")
			fmt.Println()
			fmt.Println("Press Enter to exit...")
			bufio.NewReader(os.Stdin).ReadString('\n') //nolint:errcheck
			return
		}

		// GUI build: open the first-run wizard or status window.
		localui.RunAutoLaunch(localconfig.DefaultPath(), func(cfg *config.Config) *localui.StatusWindow {
			ctx, cancel := context.WithCancel(context.Background())
			configPath := localconfig.DefaultPath()
			applyNamedAuditLog(cfg, cfg.LocalConfig, configPath)

			r := newRunnerWithLogout(cfg, configPath, cancel)
			sw := localui.NewStatusWindow(cfg.Name, cfg.APIURL)
			sw.SetStopAgentHandler(r.StopAgentJob)
			localCfg2 := cfg.LocalConfig
			sw.SetOnOpenSettings(func() {
				localui.OpenSettingsWindow(localCfg2, configPath, func(updated *localconfig.LocalConfig) {
					cfg.LocalConfig = updated
					localCfg2 = updated
				}, buildClearConfigurationHandler(cfg, configPath, cancel))
			})
			sw.Show()

			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigs
				cancel()
				localui.QuitApp()
			}()

			go func() {
				if err := runRunner(ctx, r.WithSink(sw)); err != nil {
					log.Errorf("Runner stopped: %v", err)
				}
				localui.QuitApp()
			}()

			localui.SetupTray(
				cfg.Name,
				cancel,
				buildClearConfigurationHandler(cfg, configPath, cancel),
				sw,
				localCfg2,
				func(updated *localconfig.LocalConfig) {
					cfg.LocalConfig = updated
				},
			)
			return sw
		})
		return
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
