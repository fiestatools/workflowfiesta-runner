package executor

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"workflowfiesta-runner/internal/localconfig"
)

// ScriptSyncer pushes a saved script to the server-side script library.
// Implemented by the API client; nil disables server sync.
type ScriptSyncer interface {
	PushScript(name, content, description string, tags []string) error
}

// ToolHandler executes named platform tools natively on the local filesystem.
// It is used when runner_jobs.tool_name is set (structured tool dispatch).
type ToolHandler struct {
	localCfg     *localconfig.LocalConfig
	orgID        string       // populated from heartbeat; used for per-org script namespacing
	syncer       ScriptSyncer // nil = no server sync
	worktreeRoot string       // when non-empty, relative paths resolve here and it is allowed
}

// NewToolHandler creates a ToolHandler bound to the given local config.
func NewToolHandler(localCfg *localconfig.LocalConfig) *ToolHandler {
	if localCfg == nil {
		localCfg = localconfig.Default()
	}
	orgID := ""
	if localCfg != nil {
		orgID = localCfg.OrgID
	}
	return &ToolHandler{localCfg: localCfg, orgID: orgID}
}

// SetOrgID updates the org namespace used for the script library directory.
func (h *ToolHandler) SetOrgID(orgID string) {
	h.orgID = orgID
}

// SetSyncer wires in a ScriptSyncer for fire-and-forget server sync on save.
func (h *ToolHandler) SetSyncer(s ScriptSyncer) {
	h.syncer = s
}

// SetWorktreeRoot sets (or clears) the worktree root for this handler.
// When non-empty, relative paths are resolved against the worktree root and
// the worktree directory is implicitly allowed regardless of localCfg.AllowedPaths.
// Pass an empty string to clear after the job completes.
func (h *ToolHandler) SetWorktreeRoot(root string) {
	h.worktreeRoot = root
}

// Execute dispatches to the appropriate native tool implementation.
// Returns a string result (or error message embedded in the result) and a Go error only for
// unexpected failures (not for expected tool errors like "file not found").
func (h *ToolHandler) Execute(toolName string, toolArgsRaw json.RawMessage) (string, error) {
	var args map[string]interface{}
	if len(toolArgsRaw) > 0 {
		if err := json.Unmarshal(toolArgsRaw, &args); err != nil {
			return "", fmt.Errorf("parse tool_args: %w", err)
		}
	}

	log.Infof("[tools] executing native tool %q", toolName)

	switch toolName {
	case "read_file":
		return h.readFile(args)
	case "write_file":
		return h.writeFile(args)
	case "edit_file":
		return h.editFile(args)
	case "multi_edit":
		return h.multiEdit(args)
	case "glob_files":
		return h.globFiles(args)
	case "grep_files":
		return h.grepFiles(args)
	case "list_dir":
		return h.listDir(args)
	case "save_local_script":
		return h.saveLocalScript(args)
	case "list_local_scripts":
		return h.listLocalScripts(args)
	default:
		return "", fmt.Errorf("unsupported tool: %s", toolName)
	}
}

// ── Path helpers ──────────────────────────────────────────────────────────────

// resolvePath resolves p relative to the working dir and validates it stays within allowed_paths.
// When a worktree root is active, relative paths resolve against it and the worktree directory
// is implicitly allowed so the agent can freely read/write the checked-out repo.
func (h *ToolHandler) resolvePath(p string) (string, error) {
	var resolved string
	if filepath.IsAbs(p) {
		resolved = filepath.Clean(p)
	} else if h.worktreeRoot != "" {
		resolved = filepath.Clean(filepath.Join(h.worktreeRoot, p))
	} else {
		resolved = filepath.Clean(filepath.Join(h.localCfg.WorkingDir(), p))
	}

	// If a worktree root is set, allow any path inside it without checking localCfg.
	if h.worktreeRoot != "" {
		wt := filepath.Clean(h.worktreeRoot)
		if resolved == wt || strings.HasPrefix(resolved, wt+string(filepath.Separator)) {
			return resolved, nil
		}
	}

	for _, a := range h.localCfg.ExpandedAllowedPaths() {
		clean := filepath.Clean(a)
		if resolved == clean || strings.HasPrefix(resolved, clean+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("path %q is outside allowed paths", p)
}

// scriptLibDir returns the path to the local script library directory.
// Namespaced by org_id when available so runners shared across orgs stay isolated.
func (h *ToolHandler) scriptLibDir() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".workflowfiesta", "scripts")
	if h.orgID != "" {
		dir = filepath.Join(dir, h.orgID)
	}
	return dir
}

// ── Argument helpers ─────────────────────────────────────────────────────────

func strArg(args map[string]interface{}, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func intArg(args map[string]interface{}, key string, defaultVal int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return defaultVal
}

func boolArg(args map[string]interface{}, key string) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// ── read_file ─────────────────────────────────────────────────────────────────

func (h *ToolHandler) readFile(args map[string]interface{}) (string, error) {
	path := strArg(args, "path")
	if path == "" {
		return "Error: path is required", nil
	}
	resolved, err := h.resolvePath(path)
	if err != nil {
		return "Error: " + err.Error(), nil
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Sprintf("Error reading %s: %v", path, err), nil
	}
	lines := strings.Split(string(data), "\n")
	offset := intArg(args, "offset", 0)
	limit := intArg(args, "limit", 0)
	start := 0
	if offset > 0 {
		start = offset - 1 // 1-based
	}
	if start >= len(lines) {
		return "(empty — offset beyond file end)", nil
	}
	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	slice := lines[start:end]
	out := make([]string, len(slice))
	for i, l := range slice {
		out[i] = fmt.Sprintf("%d\t%s", start+i+1, l)
	}
	return strings.Join(out, "\n"), nil
}

// ── write_file ────────────────────────────────────────────────────────────────

func (h *ToolHandler) writeFile(args map[string]interface{}) (string, error) {
	path := strArg(args, "path")
	content := strArg(args, "content")
	if path == "" {
		return "Error: path is required", nil
	}
	resolved, err := h.resolvePath(path)
	if err != nil {
		return "Error: " + err.Error(), nil
	}
	if h.localCfg.IsPathReadOnly(filepath.Dir(resolved)) {
		return fmt.Sprintf("Error: %q is in a read-only directory", path), nil
	}
	if mkErr := os.MkdirAll(filepath.Dir(resolved), 0o755); mkErr != nil {
		return fmt.Sprintf("Error creating directories: %v", mkErr), nil
	}
	if writeErr := os.WriteFile(resolved, []byte(content), 0o644); writeErr != nil {
		return fmt.Sprintf("Error writing %s: %v", path, writeErr), nil
	}

	return fmt.Sprintf("Written: %s (%s)", resolved, fmtSize(len(content))), nil
}

// autoIndexScript extracts a description from the first docstring/comment of a script
// and saves it to the script library index — if it looks like a reusable script.
func (h *ToolHandler) autoIndexScript(path, content string) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".sh" && ext != ".py" && ext != ".ps1" && ext != ".js" {
		return
	}
	name := filepath.Base(path)
	// Extract description from first comment block / docstring.
	desc := extractScriptDescription(content, ext)
	if desc == "" {
		return
	}
	libDir := h.scriptLibDir()
	if mkErr := os.MkdirAll(libDir, 0o755); mkErr != nil {
		return
	}
	meta := map[string]interface{}{
		"name":        name,
		"path":        path,
		"description": desc,
		"auto_indexed": true,
		"created":     time.Now().UTC().Format(time.RFC3339),
	}
	metaPath := filepath.Join(libDir, name+".meta.json")
	data, _ := json.MarshalIndent(meta, "", "  ")
	_ = os.WriteFile(metaPath, data, 0o644)
}

// extractScriptDescription returns the first docstring or comment block from a script.
func extractScriptDescription(content, ext string) string {
	lines := strings.Split(content, "\n")
	switch ext {
	case ".py":
		// Python: find triple-quoted docstring after optional shebang/encoding
		inDoc := false
		var docLines []string
		for _, l := range lines {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "#") || t == "" {
				continue
			}
			if strings.HasPrefix(t, `"""`) || strings.HasPrefix(t, `'''`) {
				q := t[:3]
				rest := t[3:]
				if idx := strings.Index(rest, q); idx >= 0 {
					return strings.TrimSpace(rest[:idx])
				}
				inDoc = true
				if rest != "" {
					docLines = append(docLines, rest)
				}
				continue
			}
			break
		}
		if inDoc {
			for _, l := range lines[len(docLines)+1:] {
				t := strings.TrimSpace(l)
				if strings.Contains(t, `"""`) || strings.Contains(t, `'''`) {
					return strings.TrimSpace(strings.Join(docLines, " "))
				}
				docLines = append(docLines, t)
			}
		}
	case ".sh", ".ps1", ".js":
		// Shell/JS: first contiguous comment block
		var commentLines []string
		for _, l := range lines {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "#!") {
				continue // shebang
			}
			if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") {
				text := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "//"), "#"))
				if text != "" {
					commentLines = append(commentLines, text)
				}
			} else if t != "" {
				break
			}
		}
		if len(commentLines) > 0 {
			return strings.Join(commentLines[:min(3, len(commentLines))], " ")
		}
	}
	return ""
}

// ── edit_file ─────────────────────────────────────────────────────────────────

func (h *ToolHandler) editFile(args map[string]interface{}) (string, error) {
	path := strArg(args, "path")
	oldString := strArg(args, "old_string")
	newString := strArg(args, "new_string")
	replaceAll := boolArg(args, "replace_all")
	if path == "" || oldString == "" {
		return "Error: path and old_string are required", nil
	}
	resolved, err := h.resolvePath(path)
	if err != nil {
		return "Error: " + err.Error(), nil
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Sprintf("Error reading %s: %v", path, err), nil
	}
	original := string(data)
	count := strings.Count(original, oldString)
	if count == 0 {
		return fmt.Sprintf("Error: old_string not found in %s", resolved), nil
	}
	if count > 1 && !replaceAll {
		return fmt.Sprintf("Error: old_string appears %d times in %s. Use replace_all: true to replace all, or provide more surrounding context.", count, resolved), nil
	}
	var updated string
	if replaceAll {
		updated = strings.ReplaceAll(original, oldString, newString)
	} else {
		updated = strings.Replace(original, oldString, newString, 1)
	}
	if writeErr := os.WriteFile(resolved, []byte(updated), 0o644); writeErr != nil {
		return fmt.Sprintf("Error writing %s: %v", path, writeErr), nil
	}

	// Return a compact diff so the agent gets confirmation without re-reading the file.
	diff := compactDiff(oldString, newString)
	return fmt.Sprintf("Edited: %s\n%d replacement(s) made\n%s", resolved, count, diff), nil
}

// compactDiff returns a compact unified-style diff of the change.
func compactDiff(oldStr, newStr string) string {
	oldLines := strings.Split(oldStr, "\n")
	newLines := strings.Split(newStr, "\n")
	var sb strings.Builder
	for _, l := range oldLines {
		sb.WriteString("- ")
		sb.WriteString(l)
		sb.WriteString("\n")
	}
	for _, l := range newLines {
		sb.WriteString("+ ")
		sb.WriteString(l)
		sb.WriteString("\n")
	}
	return sb.String()
}

// ── multi_edit ────────────────────────────────────────────────────────────────

func (h *ToolHandler) multiEdit(args map[string]interface{}) (string, error) {
	path := strArg(args, "path")
	if path == "" {
		return "Error: path is required", nil
	}
	resolved, err := h.resolvePath(path)
	if err != nil {
		return "Error: " + err.Error(), nil
	}
	editsRaw, _ := args["edits"].([]interface{})
	if len(editsRaw) == 0 {
		return "Error: edits array is required", nil
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Sprintf("Error reading %s: %v", path, err), nil
	}
	current := string(data)
	var results []string
	for i, editRaw := range editsRaw {
		editMap, ok := editRaw.(map[string]interface{})
		if !ok {
			results = append(results, fmt.Sprintf("Edit %d: invalid format — skipped", i+1))
			continue
		}
		oldS := strArg(editMap, "old_string")
		newS := strArg(editMap, "new_string")
		replAll := boolArg(editMap, "replace_all")
		count := strings.Count(current, oldS)
		if count == 0 {
			results = append(results, fmt.Sprintf("Edit %d: old_string not found — skipped", i+1))
			continue
		}
		if count > 1 && !replAll {
			results = append(results, fmt.Sprintf("Edit %d: old_string appears %d times — skipped (use replace_all: true)", i+1, count))
			continue
		}
		if replAll {
			current = strings.ReplaceAll(current, oldS, newS)
		} else {
			current = strings.Replace(current, oldS, newS, 1)
		}
		results = append(results, fmt.Sprintf("Edit %d: %d replacement(s) applied", i+1, count))
	}
	if writeErr := os.WriteFile(resolved, []byte(current), 0o644); writeErr != nil {
		return fmt.Sprintf("Error writing %s: %v", path, writeErr), nil
	}
	return strings.Join(results, "\n"), nil
}

// ── list_dir ──────────────────────────────────────────────────────────────────

func (h *ToolHandler) listDir(args map[string]interface{}) (string, error) {
	path := strArg(args, "path")
	if path == "" {
		path = "."
	}
	resolved, err := h.resolvePath(path)
	if err != nil {
		return "Error: " + err.Error(), nil
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return fmt.Sprintf("Error reading directory %s: %v", path, err), nil
	}
	if len(entries) == 0 {
		return "(empty directory)", nil
	}
	lines := make([]string, len(entries))
	for i, e := range entries {
		prefix := "f"
		if e.IsDir() {
			prefix = "d"
		}
		lines[i] = prefix + " " + e.Name()
	}
	return strings.Join(lines, "\n"), nil
}

// ── glob_files ────────────────────────────────────────────────────────────────

type globMatch struct {
	rel   string
	mtime time.Time
}

func (h *ToolHandler) globFiles(args map[string]interface{}) (string, error) {
	pattern := strArg(args, "pattern")
	// Accept "path" (platform-tools schema) or "cwd" (internal alias).
	cwd := strArg(args, "path")
	if cwd == "" {
		cwd = strArg(args, "cwd")
	}
	headLimit := intArg(args, "head_limit", 500)
	if headLimit <= 0 || headLimit > 500 {
		headLimit = 500
	}
	if pattern == "" {
		return "Error: pattern is required", nil
	}

	// Default base: worktree root if active, else configured working dir.
	base := h.localCfg.WorkingDir()
	if h.worktreeRoot != "" {
		base = h.worktreeRoot
	}
	if cwd != "" {
		resolved, err := h.resolvePath(cwd)
		if err != nil {
			return "Error: " + err.Error(), nil
		}
		base = resolved
	}

	// Build ignore patterns list.
	var ignorePatterns []string
	if ignoreRaw, ok := args["ignore"].([]interface{}); ok {
		for _, v := range ignoreRaw {
			if s, ok := v.(string); ok {
				ignorePatterns = append(ignorePatterns, s)
			}
		}
	}

	var matches []globMatch
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(base, p)
		if relErr != nil {
			return nil
		}
		// Check ignore patterns before including.
		for _, ig := range ignorePatterns {
			if matchGlob(ig, rel) {
				return nil
			}
		}
		if matchGlob(pattern, rel) {
			info, infoErr := d.Info()
			var mtime time.Time
			if infoErr == nil {
				mtime = info.ModTime()
			}
			matches = append(matches, globMatch{rel: rel, mtime: mtime})
		}
		return nil
	})

	if len(matches) == 0 {
		return "No files found", nil
	}

	// Sort by modification time descending (most recently modified first), matching Claude Code behaviour.
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].mtime.After(matches[j].mtime)
	})

	if len(matches) > headLimit {
		matches = matches[:headLimit]
	}

	out := make([]string, len(matches))
	for i := range matches {
		out[i] = matches[i].rel
	}
	return strings.Join(out, "\n"), nil
}

// matchGlob matches a path against a glob pattern supporting ** for recursive matching.
func matchGlob(pattern, path string) bool {
	// Normalize separators
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)

	if !strings.Contains(pattern, "**") {
		// No **, use standard match against basename for simple patterns like "*.ts"
		if !strings.Contains(pattern, "/") {
			matched, _ := filepath.Match(pattern, filepath.Base(path))
			return matched
		}
		matched, _ := filepath.Match(pattern, path)
		return matched
	}

	// Split on **
	parts := strings.SplitN(pattern, "**", 2)
	prefix := strings.TrimSuffix(parts[0], "/")
	suffix := strings.TrimPrefix(parts[1], "/")

	// Validate prefix
	if prefix != "" && !strings.HasPrefix(path, prefix+"/") && path != prefix {
		return false
	}

	rest := path
	if prefix != "" {
		rest = strings.TrimPrefix(path, prefix+"/")
	}

	if suffix == "" {
		return true
	}

	// Match suffix against any tail of the remaining path
	pathParts := strings.Split(rest, "/")
	for i := range pathParts {
		sub := strings.Join(pathParts[i:], "/")
		matched, _ := filepath.Match(suffix, sub)
		if matched {
			return true
		}
	}
	return false
}

// ── grep_files ────────────────────────────────────────────────────────────────

// grepOpts collects parsed grep parameters to avoid long arg lists.
type grepOpts struct {
	pattern         string
	globFilter      string
	fileType        string // rg --type, e.g. "ts", "py"
	base            string
	outputMode      string // "content" | "files_with_matches" | "count"
	context         int
	caseInsensitive bool
	multiline       bool
	headLimit       int
	offset          int
}

func (h *ToolHandler) grepFiles(args map[string]interface{}) (string, error) {
	pattern := strArg(args, "pattern")
	if pattern == "" {
		return "Error: pattern is required", nil
	}

	globFilter := strArg(args, "glob")
	fileType := strArg(args, "type")
	// Accept "path" (platform-tools schema) or "cwd" (internal alias).
	cwd := strArg(args, "path")
	if cwd == "" {
		cwd = strArg(args, "cwd")
	}
	outputMode := strArg(args, "output_mode")
	if outputMode == "" {
		outputMode = "files_with_matches"
	}
	ctx := intArg(args, "context", 0)
	if c2 := intArg(args, "-C", 0); c2 > ctx {
		ctx = c2
	}
	caseInsensitive := boolArg(args, "-i") || boolArg(args, "case_insensitive")
	multiline := boolArg(args, "multiline")
	headLimit := intArg(args, "head_limit", 250)
	if headLimit <= 0 {
		headLimit = 250
	}
	offset := intArg(args, "offset", 0)

	// Default base: worktree root if active, else configured working dir.
	base := h.localCfg.WorkingDir()
	if h.worktreeRoot != "" {
		base = h.worktreeRoot
	}
	if cwd != "" {
		resolved, err := h.resolvePath(cwd)
		if err != nil {
			return "Error: " + err.Error(), nil
		}
		base = resolved
	}

	opts := grepOpts{
		pattern:         pattern,
		globFilter:      globFilter,
		fileType:        fileType,
		base:            base,
		outputMode:      outputMode,
		context:         min(ctx, 10),
		caseInsensitive: caseInsensitive,
		multiline:       multiline,
		headLimit:       headLimit,
		offset:          offset,
	}

	// Prefer ripgrep for speed and full feature support.
	if rgBin, lookErr := exec.LookPath("rg"); lookErr == nil {
		return h.ripgrepSearch(rgBin, opts)
	}
	// Fallback: pure Go regexp walk (supports all output_mode values).
	return h.goGrep(opts)
}

func (h *ToolHandler) ripgrepSearch(rgBin string, opts grepOpts) (string, error) {
	var cmdArgs []string
	switch opts.outputMode {
	case "files_with_matches":
		cmdArgs = append(cmdArgs, "-l")
	case "count":
		cmdArgs = append(cmdArgs, "-c")
	default: // "content"
		cmdArgs = append(cmdArgs, "-n", "--no-heading")
		if opts.context > 0 {
			cmdArgs = append(cmdArgs, fmt.Sprintf("-C%d", opts.context))
		}
	}
	if opts.caseInsensitive {
		cmdArgs = append(cmdArgs, "-i")
	}
	if opts.multiline {
		cmdArgs = append(cmdArgs, "-U", "--multiline-dotall")
	}
	if opts.globFilter != "" {
		cmdArgs = append(cmdArgs, "--glob="+opts.globFilter)
	}
	if opts.fileType != "" {
		cmdArgs = append(cmdArgs, "--type="+opts.fileType)
	}
	cmdArgs = append(cmdArgs, "--", opts.pattern, opts.base)
	cmd := exec.Command(rgBin, cmdArgs...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "No matches found", nil
		}
		return fmt.Sprintf("rg error: %v", err), nil
	}
	return applyPagination(string(out), opts.offset, opts.headLimit), nil
}

func (h *ToolHandler) goGrep(opts grepOpts) (string, error) {
	pat := opts.pattern
	if opts.caseInsensitive {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return fmt.Sprintf("Invalid pattern: %v", err), nil
	}

	type fileCount struct {
		rel   string
		count int
	}
	var contentLines []string
	var matchedFiles []string
	var fileCounts []fileCount

	_ = filepath.WalkDir(opts.base, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if opts.globFilter != "" {
			matched, _ := filepath.Match(opts.globFilter, d.Name())
			if !matched {
				return nil
			}
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(opts.base, p)
		lines := strings.Split(string(data), "\n")
		count := 0
		for i, line := range lines {
			if re.MatchString(line) {
				count++
				if opts.outputMode == "content" {
					contentLines = append(contentLines, fmt.Sprintf("%s:%d:%s", rel, i+1, line))
				}
			}
		}
		if count > 0 {
			matchedFiles = append(matchedFiles, rel)
			fileCounts = append(fileCounts, fileCount{rel: rel, count: count})
		}
		return nil
	})

	switch opts.outputMode {
	case "count":
		var lines []string
		for _, fc := range fileCounts {
			lines = append(lines, fmt.Sprintf("%s:%d", fc.rel, fc.count))
		}
		if len(lines) == 0 {
			return "No matches found", nil
		}
		return applyPagination(strings.Join(lines, "\n"), opts.offset, opts.headLimit), nil
	case "files_with_matches":
		if len(matchedFiles) == 0 {
			return "No matches found", nil
		}
		return applyPagination(strings.Join(matchedFiles, "\n"), opts.offset, opts.headLimit), nil
	default: // "content"
		if len(contentLines) == 0 {
			return "No matches found", nil
		}
		return applyPagination(strings.Join(contentLines, "\n"), opts.offset, opts.headLimit), nil
	}
}

// applyPagination skips `offset` lines and returns up to `limit` lines.
func applyPagination(output string, offset, limit int) string {
	if output == "" {
		return output
	}
	lines := strings.Split(output, "\n")
	// Trim trailing empty line from trailing newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if offset > 0 && offset < len(lines) {
		lines = lines[offset:]
	} else if offset >= len(lines) {
		return "No matches found"
	}
	if limit > 0 && len(lines) > limit {
		lines = lines[:limit]
	}
	result := strings.Join(lines, "\n")
	if len(result) > 40_000 {
		result = result[:40_000] + "\n[... truncated]"
	}
	return result
}


// ── Script Library ────────────────────────────────────────────────────────────

type scriptMeta struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	AutoIndexed bool     `json:"auto_indexed,omitempty"`
	Created     string   `json:"created"`
	UsageCount  int      `json:"usage_count,omitempty"`
}

func (h *ToolHandler) saveLocalScript(args map[string]interface{}) (string, error) {
	name := strArg(args, "name")
	content := strArg(args, "content")
	description := strArg(args, "description")
	if name == "" || content == "" {
		return "Error: name and content are required", nil
	}

	// Sanitize name (no path separators)
	name = filepath.Base(name)
	if name == "." || name == ".." {
		return "Error: invalid script name", nil
	}

	libDir := h.scriptLibDir()
	if mkErr := os.MkdirAll(libDir, 0o755); mkErr != nil {
		return fmt.Sprintf("Error creating script library: %v", mkErr), nil
	}

	scriptPath := filepath.Join(libDir, name)
	if writeErr := os.WriteFile(scriptPath, []byte(content), 0o755); writeErr != nil {
		return fmt.Sprintf("Error saving script: %v", writeErr), nil
	}

	// Parse tags
	var tags []string
	if tagsRaw, ok := args["tags"].([]interface{}); ok {
		for _, t := range tagsRaw {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}

	if description == "" {
		ext := strings.ToLower(filepath.Ext(name))
		description = extractScriptDescription(content, ext)
	}

	meta := scriptMeta{
		Name:        name,
		Path:        scriptPath,
		Description: description,
		Tags:        tags,
		Created:     time.Now().UTC().Format(time.RFC3339),
	}
	metaData, _ := json.MarshalIndent(meta, "", "  ")
	metaPath := scriptPath + ".meta.json"
	if writeErr := os.WriteFile(metaPath, metaData, 0o644); writeErr != nil {
		return fmt.Sprintf("Error saving script metadata: %v", writeErr), nil
	}

	// Fire-and-forget sync to server so other runners can pull this script.
	if h.syncer != nil {
		go func() {
			if err := h.syncer.PushScript(name, content, description, tags); err != nil {
				log.Warnf("[tools] server sync for script %q failed: %v", name, err)
			}
		}()
	}

	return fmt.Sprintf("Saved: %s (%s)\nLibrary: %s", name, fmtSize(len(content)), libDir), nil
}

func (h *ToolHandler) listLocalScripts(args map[string]interface{}) (string, error) {
	tagFilter := strArg(args, "tag")
	libDir := h.scriptLibDir()

	entries, err := os.ReadDir(libDir)
	if os.IsNotExist(err) {
		return "Script library is empty. Use save_local_script to add scripts.", nil
	}
	if err != nil {
		return fmt.Sprintf("Error reading script library: %v", err), nil
	}

	var lines []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".meta.json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(libDir, e.Name()))
		if readErr != nil {
			continue
		}
		var meta scriptMeta
		if jsonErr := json.Unmarshal(data, &meta); jsonErr != nil {
			continue
		}
		// Apply tag filter
		if tagFilter != "" {
			found := false
			for _, t := range meta.Tags {
				if strings.EqualFold(t, tagFilter) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		desc := meta.Description
		if desc == "" {
			desc = "(no description)"
		}
		tagStr := ""
		if len(meta.Tags) > 0 {
			tagStr = " [" + strings.Join(meta.Tags, ", ") + "]"
		}
		lines = append(lines, fmt.Sprintf("- %s%s: %s", meta.Name, tagStr, desc))
	}

	if len(lines) == 0 {
		if tagFilter != "" {
			return fmt.Sprintf("No scripts found with tag %q.", tagFilter), nil
		}
		return "Script library is empty. Use save_local_script to add scripts.", nil
	}
	return fmt.Sprintf("Local script library (%s):\n%s", libDir, strings.Join(lines, "\n")), nil
}

// LoadLocalScript reads a script from the library by name and returns its content.
// Used by runner.go to expand run_local_script into a bash execution.
func (h *ToolHandler) LoadLocalScript(name string) (string, error) {
	name = filepath.Base(name)
	libDir := h.scriptLibDir()
	scriptPath := filepath.Join(libDir, name)
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", fmt.Errorf("script %q not found in library (%s): %w", name, libDir, err)
	}
	// Increment usage count in meta (best-effort)
	metaPath := scriptPath + ".meta.json"
	metaData, _ := os.ReadFile(metaPath)
	if len(metaData) > 0 {
		var meta scriptMeta
		if json.Unmarshal(metaData, &meta) == nil {
			meta.UsageCount++
			if updated, _ := json.MarshalIndent(meta, "", "  "); len(updated) > 0 {
				_ = os.WriteFile(metaPath, updated, 0o644)
			}
		}
	}
	return string(data), nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func fmtSize(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
