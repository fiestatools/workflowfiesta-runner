package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"workflowfiesta-runner/internal/config"
	"workflowfiesta-runner/internal/localconfig"
	"workflowfiesta-runner/internal/localui"
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

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the runner and connect to WorkflowFiesta",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()

		if cfg.Token == "" {
			return fmt.Errorf("WORKFLOWFIESTA_TOKEN is required")
		}

		log.Infof("Starting WorkflowFiesta runner (version %s)", version)
		log.Infof("API URL: %s", cfg.APIURL)
		log.Infof("Runner name: %s", cfg.Name)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigs
			log.Info("Shutting down...")
			cancel()
		}()

		r := runner.New(cfg).WithSink(&cliSink{apiURL: cfg.APIURL, name: cfg.Name})
		return r.Run(ctx)
	},
}

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a new runner with WorkflowFiesta",
	RunE: func(cmd *cobra.Command, args []string) error {
		apiURL, _ := cmd.Flags().GetString("api-url")
		name, _ := cmd.Flags().GetString("name")
		orgID, _ := cmd.Flags().GetString("org-id")

		if apiURL == "" || name == "" || orgID == "" {
			return fmt.Errorf("--api-url, --name, and --org-id are required")
		}

		body, _ := json.Marshal(map[string]string{
			"name":   name,
			"org_id": orgID,
		})

		resp, err := http.Post(apiURL+"/api/runners/register", "application/json", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("registration request failed: %w", err)
		}
		defer resp.Body.Close()

		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusCreated {
			return fmt.Errorf("registration failed (%d): %s", resp.StatusCode, data)
		}

		var result map[string]interface{}
		json.Unmarshal(data, &result)

		fmt.Printf("Runner registered successfully!\n")
		fmt.Printf("Runner ID: %s\n", result["id"])
		fmt.Printf("Token: %s\n\n", result["token"])
		fmt.Printf("Set environment variables:\n")
		fmt.Printf("  export WORKFLOWFIESTA_API_URL=%s\n", apiURL)
		fmt.Printf("  export WORKFLOWFIESTA_TOKEN=%s\n", result["token"])
		fmt.Printf("  export WORKFLOWFIESTA_RUNNER_ID=%s\n", result["id"])
		fmt.Printf("  export WORKFLOWFIESTA_RUNNER_NAME=%s\n\n", name)
		fmt.Printf("Then run: workflowfiesta-runner run\n")

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

		log.Infof("Starting WorkflowFiesta runner in local mode (version %s)", version)
		log.Infof("API URL: %s", cfg.APIURL)
		log.Infof("Runner name: %s", cfg.Name)
		log.Infof("Allowed paths: %v", localCfg.AllowedPaths)
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
			r := runner.New(cfg).WithSink(&cliSink{apiURL: cfg.APIURL, name: cfg.Name})
			return r.Run(ctx)
		}

		// GUI mode: open status window + system tray.
		// The runner runs in a goroutine; Fyne event loop on main thread.
		// macOS requires the GUI event loop on the main OS thread.
		sw := localui.NewStatusWindow(cfg.Name, cfg.APIURL)
		// Show before a.Run() — must be a direct call, not via fyne.Do,
		// because the Fyne event loop hasn't started yet.
		sw.Show()

		r := runner.New(cfg)
		go func() {
			if err := r.WithSink(sw).Run(ctx); err != nil {
				log.Errorf("Runner stopped: %v", err)
			}
			localui.QuitApp()
		}()

		localui.StartTray(cfg.Name, cancel, sw, localCfg, func(updated *localconfig.LocalConfig) {
			cfg.LocalConfig = updated
		}) // Blocks until app.Quit().
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
		fmt.Printf("Runner ID : %s\n", result.RunnerID)
		fmt.Printf("Runner    : %s\n", result.RunnerName)
		fmt.Printf("\nCredentials saved to ~/.workflowfiesta/credentials.env\n")
		fmt.Printf("Start the runner with:\n\n  source ~/.workflowfiesta/credentials.env\n  workflowfiesta-runner run-local\n\n")
		return nil
	},
}

func init() {
	registerCmd.Flags().String("api-url", "", "WorkflowFiesta API URL")
	registerCmd.Flags().String("name", "", "Runner name")
	registerCmd.Flags().String("org-id", "", "Organization ID")

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

	// No arguments: user double-clicked the app. Skip the CLI entirely and
	// go straight to the GUI launcher (first-run wizard or status window).
	if len(os.Args) == 1 {
		localui.RunAutoLaunch(localconfig.DefaultPath(), func(cfg *config.Config) {
			ctx, cancel := context.WithCancel(context.Background())

			sw := localui.NewStatusWindow(cfg.Name, cfg.APIURL)
			sw.Show()

			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigs
				cancel()
				localui.QuitApp()
			}()

			go func() {
				if err := runner.New(cfg).WithSink(sw).Run(ctx); err != nil {
					log.Errorf("Runner stopped: %v", err)
				}
				localui.QuitApp()
			}()

			localCfg2 := cfg.LocalConfig
			localui.SetupTray(cfg.Name, cancel, sw, localCfg2, func(updated *localconfig.LocalConfig) {
				cfg.LocalConfig = updated
			})
		})
		return
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
