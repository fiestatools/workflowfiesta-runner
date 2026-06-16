package localconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"workflowfiesta-runner/internal/auditlog"
	"workflowfiesta-runner/internal/platform"
)

// NamedAuditLogPath returns the audit log path for a specific runner instance.
// Format: ~/.workflowfiesta/audit-{name}-{id}.log
func NamedAuditLogPath(runnerName, runnerID string) string {
	home, _ := os.UserHomeDir()
	filename := fmt.Sprintf("audit-%s-%s.log", sanitizeForFilename(runnerName), sanitizeForFilename(runnerID))
	return filepath.Join(home, ".workflowfiesta", filename)
}

// sanitizeForFilename replaces characters that are invalid or problematic in
// filenames with dashes. Spaces are replaced to avoid breaking shell scripts
// that split on whitespace without quoting.
func sanitizeForFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', ' ':
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// UpdateAuditLogForRunner switches AuditLog to the runner-specific named path
// when the runner name and ID are known and the path is still the generic default.
// Existing content from the old log is appended to the new file so no audit
// history is orphaned. Returns true if changed — caller should save the config.
func (c *LocalConfig) UpdateAuditLogForRunner(runnerName, runnerID string) bool {
	if runnerName == "" || runnerID == "" {
		return false
	}
	named := NamedAuditLogPath(runnerName, runnerID)
	if c.AuditLog == named {
		return false
	}
	home, _ := os.UserHomeDir()
	defaultLog := filepath.Join(home, ".workflowfiesta", "audit.log")
	if c.AuditLog != "" && filepath.Clean(c.AuditLog) != defaultLog {
		return false // user has a custom path, don't override
	}
	migrateAuditLog(defaultLog, named)
	c.AuditLog = named
	return true
}

type migrationWarningEntry struct {
	Time  string `json:"time"`
	Event string `json:"event"`
	Src   string `json:"src"`
	Error string `json:"error"`
}

// migrateAuditLog appends the content of src to dst so existing audit entries
// are not orphaned when the log path migrates from the generic to the named file.
// If migration fails, a JSON warning entry is written to dst so the audit trail
// records that historical entries may be missing.
func migrateAuditLog(src, dst string) {
	data, err := os.ReadFile(src)
	if err != nil || len(data) == 0 {
		return
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		writeMigrationWarning(dst, src, err)
		return
	}
	f, err := os.OpenFile(dst, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		writeMigrationWarning(dst, src, err)
		return
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		writeMigrationWarning(dst, src, err)
	}
}

func writeMigrationWarning(dst, src string, migrationErr error) {
	_ = auditlog.AppendLine(dst, migrationWarningEntry{
		Time:  time.Now().UTC().Format(time.RFC3339),
		Event: "audit_migration_failed",
		Src:   src,
		Error: migrationErr.Error(),
	})
}

// LocalConfig holds the configuration for local executor mode, loaded from runner.yaml.
type LocalConfig struct {
	AllowedPaths          []string `yaml:"allowed_paths"`
	Confirm               string   `yaml:"confirm"`               // "always", "destructive", "never"
	Network               string   `yaml:"network"`               // "all", "localhost", "none"
	MaxTimeout            int      `yaml:"max_timeout"`           // seconds
	ConfirmTimeout        int      `yaml:"confirm_timeout"`       // seconds
	ConfirmNeverTimeout   bool     `yaml:"confirm_never_timeout"` // wait indefinitely; overrides confirm_timeout
	BlockedPatterns       []string `yaml:"blocked_patterns"`      // regex patterns
	Sandbox               string   `yaml:"sandbox"`               // "none", "kernel"
	AuditLog              string   `yaml:"audit_log"`             // path
	SoundOnApproval       bool     `yaml:"sound_on_approval"`
	AlwaysAllowedPatterns []string `yaml:"always_allowed_patterns"`

	// EnvironmentID is the UUID of an existing environment to associate this runner with.
	// If empty, the server auto-creates a new environment on registration.
	EnvironmentID string `yaml:"environment_id,omitempty"`

	// OrgID is populated from the first heartbeat response and persisted so it
	// survives restarts. Used to namespace the local script library.
	OrgID string `yaml:"org_id,omitempty"`

	// Runtime-only fields — not persisted to YAML.
	Headless   bool   `yaml:"-"`
	RunnerName string `yaml:"-"`
}

// DefaultPath returns ~/.workflowfiesta/runner.yaml.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "runner.yaml"
	}
	return filepath.Join(home, ".workflowfiesta", "runner.yaml")
}

// Default returns a LocalConfig with safe, out-of-the-box defaults.
func Default() *LocalConfig {
	home, _ := os.UserHomeDir()
	auditLog := filepath.Join(home, ".workflowfiesta", "audit.log")
	return &LocalConfig{
		AllowedPaths:    []string{"~/"},
		Confirm:         "destructive",
		Network:         "all",
		MaxTimeout:      180,
		ConfirmTimeout:  120,
		SoundOnApproval: false,
		BlockedPatterns: []string{
			`rm\s+-rf\s+/`,
			`rm\s+-rf\s+~`,
			`dd\s.*of=/dev/[a-z]`,
			`mkfs\.`,
			`:.*>.*\s*/dev/[a-z]`,
		},
		Sandbox:  "none",
		AuditLog: auditLog,
	}
}

// Load reads the YAML config from path, applies defaults for missing fields,
// and expands ~ in paths. Returns defaults if the file does not exist.
func Load(path string) (*LocalConfig, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	// Expand ~ in all path fields.
	for i, p := range cfg.AllowedPaths {
		cfg.AllowedPaths[i] = expandTilde(stripROSuffix(p))
	}
	cfg.AuditLog = expandTilde(cfg.AuditLog)

	return cfg, nil
}

// Save writes cfg to path (creating parent directories as needed).
func Save(cfg *LocalConfig, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	// Best-effort: hide the ~/.workflowfiesta directory on Windows.
	if home, err := os.UserHomeDir(); err == nil {
		_ = platform.SetHidden(filepath.Join(home, ".workflowfiesta"))
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}

// ExpandedAllowedPaths returns the allowed paths with ~ expanded and :ro stripped.
func (c *LocalConfig) ExpandedAllowedPaths() []string {
	paths := make([]string, len(c.AllowedPaths))
	for i, p := range c.AllowedPaths {
		paths[i] = expandTilde(stripROSuffix(p))
	}
	return paths
}

// IsPathReadOnly reports whether the given (already-expanded) path is marked :ro in config.
func (c *LocalConfig) IsPathReadOnly(path string) bool {
	for _, p := range c.AllowedPaths {
		if strings.HasSuffix(p, ":ro") {
			if expandTilde(stripROSuffix(p)) == path {
				return true
			}
		}
	}
	return false
}

// WorkingDir returns the first writable allowed path, falling back to the home dir.
func (c *LocalConfig) WorkingDir() string {
	for _, p := range c.AllowedPaths {
		if !strings.HasSuffix(p, ":ro") {
			expanded := filepath.Clean(expandTilde(p))
			if info, err := os.Stat(expanded); err == nil && info.IsDir() {
				return expanded
			}
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

func expandTilde(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return home + path[1:]
	}
	return path
}

func stripROSuffix(path string) string {
	return strings.TrimSuffix(path, ":ro")
}
