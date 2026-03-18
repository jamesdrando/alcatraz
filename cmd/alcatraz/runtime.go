package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

type AuthMode string

const (
	AuthModeAPIKey  AuthMode = "api-key"
	AuthModeChatGPT AuthMode = "chatgpt"
)

type Runtime struct {
	RepoRoot string
	GitDir   string
	StateDir string
	Config   Config
	Env      map[string]string
}

type RunMetadata struct {
	ID             string    `json:"id"`
	BranchName     string    `json:"branch_name"`
	BaseRef        string    `json:"base_ref"`
	WorktreePath   string    `json:"worktree_path"`
	ComposeProject string    `json:"compose_project"`
	AuthMode       AuthMode  `json:"auth_mode"`
	ComposeFiles   []string  `json:"compose_files"`
	ConfigPath     string    `json:"config_path,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type RunStatus struct {
	RunMetadata
	Running        bool `json:"running"`
	WorktreeExists bool `json:"worktree_exists"`
	BranchExists   bool `json:"branch_exists"`
	Dirty          bool `json:"dirty"`
}

func newRuntime(configPath string) (*Runtime, error) {
	repoRoot, err := repoRoot()
	if err != nil {
		return nil, err
	}

	gitDir, err := gitDir(repoRoot)
	if err != nil {
		return nil, err
	}

	cfg, err := loadConfig(repoRoot, configPath)
	if err != nil {
		return nil, err
	}

	env := environmentMap(os.Environ())
	if cfg.EnvFile != "" {
		path := cfg.EnvFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(repoRoot, path)
		}
		if _, err := os.Stat(path); err == nil {
			fileEnv, err := parseDotEnv(path)
			if err != nil {
				return nil, err
			}
			for k, v := range fileEnv {
				env[k] = v
			}
		}
	}

	currentUser, err := user.Current()
	if err == nil {
		if env["AGENT_UID"] == "" {
			env["AGENT_UID"] = currentUser.Uid
		}
		if env["AGENT_GID"] == "" {
			env["AGENT_GID"] = currentUser.Gid
		}
		if env["HOST_CODEX_HOME"] == "" {
			env["HOST_CODEX_HOME"] = filepath.Join(currentUser.HomeDir, ".codex")
		}
	}

	stateDir := filepath.Join(gitDir, "alcatraz")
	if err := os.MkdirAll(filepath.Join(stateDir, "runs"), 0o755); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "worktrees"), 0o755); err != nil {
		return nil, fmt.Errorf("create worktree dir: %w", err)
	}

	return &Runtime{
		RepoRoot: repoRoot,
		GitDir:   gitDir,
		StateDir: stateDir,
		Config:   cfg,
		Env:      env,
	}, nil
}

func repoRoot() (string, error) {
	out, err := execInDir("", "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("must be run inside a git repository")
	}
	return strings.TrimSpace(out), nil
}

func gitDir(repoRoot string) (string, error) {
	out, err := execInDir(repoRoot, "git", "rev-parse", "--git-dir")
	if err != nil {
		return "", fmt.Errorf("resolve git dir: %w", err)
	}
	gitDir := strings.TrimSpace(out)
	if filepath.IsAbs(gitDir) {
		return gitDir, nil
	}
	return filepath.Join(repoRoot, gitDir), nil
}

func environmentMap(entries []string) map[string]string {
	env := make(map[string]string, len(entries))
	for _, entry := range entries {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		env[parts[0]] = parts[1]
	}
	return env
}

func parseDotEnv(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open env file: %w", err)
	}
	defer file.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		value := parts[1]
		if key == "" {
			continue
		}
		if len(value) >= 2 {
			if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
				value = strings.TrimSuffix(strings.TrimPrefix(value, "\""), "\"")
			} else if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
				value = strings.TrimSuffix(strings.TrimPrefix(value, "'"), "'")
			}
		}
		values[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan env file: %w", err)
	}
	return values, nil
}

func (r *Runtime) metadataDir() string {
	return filepath.Join(r.StateDir, "runs")
}

func (r *Runtime) worktreeDir() string {
	return filepath.Join(r.StateDir, "worktrees")
}

func (r *Runtime) metadataPath(runID string) string {
	return filepath.Join(r.metadataDir(), runID+".json")
}

func (r *Runtime) composeFiles(authMode AuthMode) []string {
	files := append([]string{}, r.Config.ComposeFiles...)
	if authMode == AuthModeChatGPT {
		files = append(files, r.Config.ChatGPTComposeFile)
	}
	return files
}

func (r *Runtime) commandEnv(extra map[string]string) []string {
	env := make(map[string]string, len(r.Env)+len(extra))
	for k, v := range r.Env {
		env[k] = v
	}
	for k, v := range extra {
		env[k] = v
	}

	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+env[key])
	}
	return result
}

func (r *Runtime) ensureCleanCheckout() error {
	out, err := execInDir(r.RepoRoot, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return errors.New("working tree is not clean; commit or stash changes first, or rerun with --allow-dirty")
	}
	return nil
}

func (r *Runtime) resolveCodexBin() (string, error) {
	if path := r.Env["HOST_CODEX_BIN"]; path != "" {
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("HOST_CODEX_BIN is not usable: %w", err)
		}
		if info.Mode()&0o111 == 0 {
			return "", fmt.Errorf("HOST_CODEX_BIN is not executable: %s", path)
		}
		return path, nil
	}

	if path, err := exec.LookPath("codex"); err == nil {
		return path, nil
	}

	patterns := []string{
		filepath.Join(os.Getenv("HOME"), ".vscode-server", "extensions", "openai.chatgpt-*", "bin", "linux-x86_64", "codex"),
		filepath.Join(os.Getenv("HOME"), ".vscode", "extensions", "openai.chatgpt-*", "bin", "linux-x86_64", "codex"),
	}
	candidates := make([]string, 0)
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			info, err := os.Stat(match)
			if err == nil && info.Mode()&0o111 != 0 {
				candidates = append(candidates, match)
			}
		}
	}
	sort.Strings(candidates)
	if len(candidates) > 0 {
		return candidates[len(candidates)-1], nil
	}

	return "", errors.New("could not find a local codex binary; set HOST_CODEX_BIN explicitly")
}

func (r *Runtime) resolveAuthMode() (AuthMode, error) {
	if strings.TrimSpace(r.Env["OPENAI_API_KEY"]) != "" {
		return AuthModeAPIKey, nil
	}

	hostCodexHome := strings.TrimSpace(r.Env["HOST_CODEX_HOME"])
	if hostCodexHome == "" {
		return "", errors.New("no auth configured; set OPENAI_API_KEY or HOST_CODEX_HOME")
	}
	if _, err := os.Stat(filepath.Join(hostCodexHome, "auth.json")); err == nil {
		return AuthModeChatGPT, nil
	}

	return "", errors.New("no auth configured; set OPENAI_API_KEY or point HOST_CODEX_HOME at a logged-in .codex directory")
}

func (r *Runtime) createWorktree(runID, branchName, baseRef string) (string, error) {
	worktreePath := filepath.Join(r.worktreeDir(), runID)
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(worktreePath); err == nil {
		return "", fmt.Errorf("worktree path already exists: %s", worktreePath)
	}
	if _, err := execInDir(r.RepoRoot, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+branchName); err == nil {
		return "", fmt.Errorf("branch already exists: %s", branchName)
	}

	if _, err := execInDir(r.RepoRoot, "git", "worktree", "add", "-b", branchName, worktreePath, baseRef); err != nil {
		return "", fmt.Errorf("create worktree: %w", err)
	}
	return worktreePath, nil
}

func (r *Runtime) writeRunMetadata(meta RunMetadata) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run metadata: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(r.metadataPath(meta.ID), data, 0o644)
}

func (r *Runtime) readRunMetadata(path string) (RunMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RunMetadata{}, fmt.Errorf("read run metadata: %w", err)
	}

	var meta RunMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return RunMetadata{}, fmt.Errorf("parse run metadata %s: %w", path, err)
	}
	return meta, nil
}

func (r *Runtime) loadRuns() ([]RunMetadata, error) {
	entries, err := os.ReadDir(r.metadataDir())
	if err != nil {
		return nil, fmt.Errorf("read metadata dir: %w", err)
	}

	runs := make([]RunMetadata, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		meta, err := r.readRunMetadata(filepath.Join(r.metadataDir(), entry.Name()))
		if err != nil {
			return nil, err
		}
		runs = append(runs, meta)
	}

	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	return runs, nil
}

func (r *Runtime) loadRun(runID string) (RunMetadata, error) {
	if runID == "" {
		runs, err := r.loadRuns()
		if err != nil {
			return RunMetadata{}, err
		}
		if len(runs) == 0 {
			return RunMetadata{}, errors.New("no runs found")
		}
		return runs[0], nil
	}

	path := r.metadataPath(runID)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return RunMetadata{}, fmt.Errorf("run not found: %s", runID)
		}
		return RunMetadata{}, err
	}
	return r.readRunMetadata(path)
}

func (r *Runtime) enrichStatus(meta RunMetadata) RunStatus {
	status := RunStatus{RunMetadata: meta}

	if _, err := os.Stat(meta.WorktreePath); err == nil {
		status.WorktreeExists = true
	}

	if _, err := execInDir(r.RepoRoot, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+meta.BranchName); err == nil {
		status.BranchExists = true
	}

	if status.WorktreeExists {
		if out, err := execInDir(meta.WorktreePath, "git", "status", "--porcelain"); err == nil && strings.TrimSpace(out) != "" {
			status.Dirty = true
		}
	}

	if out, err := exec.Command("docker", "ps", "--filter", "label=com.docker.compose.project="+meta.ComposeProject, "--format", "{{.ID}}").Output(); err == nil {
		status.Running = strings.TrimSpace(string(out)) != ""
	}

	return status
}

func printStatuses(statuses []RunStatus, asJSON bool, out io.Writer) error {
	if asJSON {
		data, err := json.MarshalIndent(statuses, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(data))
		return err
	}

	if len(statuses) == 0 {
		_, err := fmt.Fprintln(out, "No runs found.")
		return err
	}

	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN ID\tBRANCH\tSTATE\tDIRTY\tWORKTREE")
	for _, status := range statuses {
		state := "stopped"
		if status.Running {
			state = "running"
		}
		dirty := "clean"
		if status.Dirty {
			dirty = "dirty"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", status.ID, status.BranchName, state, dirty, status.WorktreePath)
	}
	return tw.Flush()
}

func composeProjectName(prefix, runID string) string {
	name := sanitizeComposePart(prefix) + "-" + sanitizeComposePart(runID)
	return strings.Trim(name, "-")
}

func sanitizeComposePart(value string) string {
	value = strings.ToLower(value)
	replacer := strings.NewReplacer("/", "-", "_", "-", " ", "-")
	value = replacer.Replace(value)

	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-':
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}

func newRunID() string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	return time.Now().Format("20060102-150405") + fmt.Sprintf("-%04x", rng.Intn(1<<16))
}

func execInDir(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return "", err
	}
	return stdout.String(), nil
}
