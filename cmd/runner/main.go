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
	"syscall"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"workflowfiesta-runner/internal/config"
	"workflowfiesta-runner/internal/runner"
)

var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "workflowfiesta-runner",
	Short: "WorkflowFiesta self-hosted runner",
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

		// Handle signals
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigs
			log.Info("Shutting down...")
			cancel()
		}()

		r := runner.New(cfg)
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

func init() {
	registerCmd.Flags().String("api-url", "", "WorkflowFiesta API URL")
	registerCmd.Flags().String("name", "", "Runner name")
	registerCmd.Flags().String("org-id", "", "Organization ID")

	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(registerCmd)
	rootCmd.AddCommand(versionCmd)
}

func main() {
	log.SetFormatter(&log.TextFormatter{FullTimestamp: true})
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
