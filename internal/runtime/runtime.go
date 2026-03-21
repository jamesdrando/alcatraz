package runtime

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	assets "github.com/jamesdrando/alcatraz"
	"github.com/jamesdrando/alcatraz/internal/config"
	"github.com/jamesdrando/alcatraz/internal/dockerops"
	"github.com/jamesdrando/alcatraz/internal/gitops"
)

type AuthMode string

const (
	AuthModeAPIKey  AuthMode = "api-key"
	AuthModeChatGPT AuthMode = "chatgpt"
)

type OpenOptions struct {
	RepoRoot   string
	ConfigPath string
	Environ    []string
}

type Runtime struct {
	RepoRoot             string
	GitDir               string
	StateDir             string
	AssetsRoot           string
	Config               config.Config
	Env                  map[string]string
	Git                  *gitops.Client
	Docker               *dockerops.Client
	composeFiles         []string
	chatGPTComposeFile   string
	containerRuntime     string
	containerRuntimeErr  error
	containerRuntimeOnce sync.Once
}

func Open(opts OpenOptions) (*Runtime, error) {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		repoRoot, err = gitops.DiscoverRepoRoot(cwd)
		if err != nil {
			return nil, err
		}
	}

	gitDir, err := gitops.DiscoverGitDir(repoRoot)
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(repoRoot, opts.ConfigPath)
	if err != nil {
		return nil, err
	}

	envEntries := opts.Environ
	if envEntries == nil {
		envEntries = os.Environ()
	}
	env := environmentMap(envEntries)
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
			for key, value := range fileEnv {
				env[key] = value
			}
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
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
	if err := os.MkdirAll(filepath.Join(repoRoot, ".alcatraz", "worktrees"), 0o755); err != nil {
		return nil, fmt.Errorf("create worktree dir: %w", err)
	}
	if err := ensureGitExclude(gitDir, "/.alcatraz/worktrees/"); err != nil {
		return nil, fmt.Errorf("update git exclude: %w", err)
	}

	assetsRoot, err := assets.Materialize(stateDir)
	if err != nil {
		return nil, fmt.Errorf("stage bundled assets: %w", err)
	}
	composeFiles, err := assets.ResolveComposeFiles(assetsRoot, cfg.ComposeFiles)
	if err != nil {
		return nil, err
	}
	chatGPTComposeFile, err := assets.ResolveComposeFile(assetsRoot, cfg.ChatGPTComposeFile)
	if err != nil {
		return nil, err
	}

	return &Runtime{
		RepoRoot:           repoRoot,
		GitDir:             gitDir,
		StateDir:           stateDir,
		AssetsRoot:         assetsRoot,
		Config:             cfg,
		Env:                env,
		Git:                gitops.New(repoRoot),
		Docker:             dockerops.New(repoRoot),
		composeFiles:       composeFiles,
		chatGPTComposeFile: chatGPTComposeFile,
	}, nil
}

func (r *Runtime) MetadataDir() string {
	return filepath.Join(r.StateDir, "runs")
}

func (r *Runtime) WorktreeDir() string {
	return filepath.Join(r.RepoRoot, ".alcatraz", "worktrees")
}

func (r *Runtime) MetadataPath(runID string) string {
	return filepath.Join(r.MetadataDir(), runID+".json")
}

func (r *Runtime) ComposeFiles(authMode AuthMode) []string {
	files := append([]string{}, r.composeFiles...)
	if authMode == AuthModeChatGPT {
		files = append(files, r.chatGPTComposeFile)
	}
	return files
}

func (r *Runtime) EnsureEnvFile() error {
	if strings.TrimSpace(r.Config.EnvFile) == "" {
		return nil
	}

	path := r.Config.EnvFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.RepoRoot, path)
	}
	if err := r.ensureEnvFileIgnored(path); err != nil {
		return err
	}

	if _, err := os.Stat(path); err == nil {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat env file: %w", err)
	}

	content, ok := r.bootstrapEnvFileContents()
	if !ok {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create env dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write env file: %w", err)
	}
	return nil
}

func (r *Runtime) EnsureEnvFileIgnored() error {
	if strings.TrimSpace(r.Config.EnvFile) == "" {
		return nil
	}

	path := r.Config.EnvFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.RepoRoot, path)
	}
	return r.ensureEnvFileIgnored(path)
}

func (r *Runtime) CommandEnv(extra map[string]string) []string {
	env := make(map[string]string, len(r.Env)+len(extra))
	for key, value := range r.Env {
		env[key] = value
	}
	for key, value := range extra {
		env[key] = value
	}

	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func (r *Runtime) ResolveContainerRuntime() (string, error) {
	if value := strings.TrimSpace(r.Env["ALCATRAZ_CONTAINER_RUNTIME"]); value != "" {
		return value, nil
	}

	r.containerRuntimeOnce.Do(func() {
		r.containerRuntime, r.containerRuntimeErr = detectContainerRuntime()
	})
	if r.containerRuntimeErr != nil {
		return "", r.containerRuntimeErr
	}
	if r.containerRuntime == "" {
		return "", errors.New("could not resolve a usable Docker runtime; set ALCATRAZ_CONTAINER_RUNTIME explicitly")
	}
	return r.containerRuntime, nil
}

func (r *Runtime) ResolveEgressProxyRuntime() (string, error) {
	if value := strings.TrimSpace(r.Env["ALCATRAZ_EGRESS_PROXY_RUNTIME"]); value != "" {
		return value, nil
	}

	defaultRuntime, err := detectDefaultContainerRuntime()
	if err != nil {
		return "", err
	}
	if defaultRuntime != "" {
		return defaultRuntime, nil
	}
	return "runc", nil
}

func (r *Runtime) ResolveCodexBin() (string, error) {
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

	home := os.Getenv("HOME")
	patterns := []string{
		filepath.Join(home, ".vscode-server", "extensions", "openai.chatgpt-*", "bin", "linux-x86_64", "codex"),
		filepath.Join(home, ".vscode", "extensions", "openai.chatgpt-*", "bin", "linux-x86_64", "codex"),
	}

	var candidates []string
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

func (r *Runtime) ResolveAuthMode() (AuthMode, error) {
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

func (r *Runtime) bootstrapEnvFileContents() (string, bool) {
	lines := []string{
		"# Generated by alcatraz on first run.",
		"# Safe to edit locally. Existing .env files are left untouched.",
	}

	hasValues := false
	if value := strings.TrimSpace(r.Env["OPENAI_API_KEY"]); value != "" {
		lines = append(lines, "OPENAI_API_KEY="+dotenvValue(value))
		hasValues = true
	}
	if value := strings.TrimSpace(r.Env["OPENAI_BASE_URL"]); value != "" {
		lines = append(lines, "OPENAI_BASE_URL="+dotenvValue(value))
		hasValues = true
	}

	if value := strings.TrimSpace(r.Env["HOST_CODEX_HOME"]); value != "" {
		lines = append(lines, "HOST_CODEX_HOME="+dotenvValue(value))
		hasValues = true
	}

	if value, err := r.ResolveCodexBin(); err == nil && strings.TrimSpace(value) != "" {
		lines = append(lines, "HOST_CODEX_BIN="+dotenvValue(value))
		hasValues = true
	}

	for _, key := range []string{
		"ALCATRAZ_DEP_PROFILES",
		"ALCATRAZ_APT_PACKAGES",
		"ALCATRAZ_NODE_PACKAGES",
		"ALCATRAZ_PYTHON_PACKAGES",
		"ALCATRAZ_GO_MODULES",
	} {
		if value := strings.TrimSpace(r.Env[key]); value != "" {
			lines = append(lines, key+"="+dotenvValue(value))
			hasValues = true
		}
	}

	if !hasValues {
		return "", false
	}

	lines = append(lines, "")
	return strings.Join(lines, "\n"), true
}

func (r *Runtime) ensureEnvFileIgnored(path string) error {
	rel, err := filepath.Rel(r.RepoRoot, path)
	if err != nil {
		return nil
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return nil
	}
	return ensureGitExclude(r.GitDir, "/"+filepath.ToSlash(rel))
}

func dotenvValue(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\r\n#") {
		return strconv.Quote(value)
	}
	return value
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

func detectContainerRuntime() (string, error) {
	runtimes, defaultRuntime, err := inspectDockerRuntimes()
	if err != nil {
		return "", err
	}

	if _, ok := runtimes["runsc"]; ok {
		return "runsc", nil
	}
	if defaultRuntime != "" {
		return defaultRuntime, nil
	}
	if _, ok := runtimes["runc"]; ok {
		return "runc", nil
	}

	if len(runtimes) == 0 {
		return "", errors.New("docker did not report any registered container runtimes")
	}

	names := make([]string, 0, len(runtimes))
	for name := range runtimes {
		names = append(names, name)
	}
	sort.Strings(names)
	return "", fmt.Errorf("docker did not report a default runtime; available runtimes: %s", strings.Join(names, ", "))
}

func detectDefaultContainerRuntime() (string, error) {
	runtimes, defaultRuntime, err := inspectDockerRuntimes()
	if err != nil {
		return "", err
	}
	if defaultRuntime != "" {
		return defaultRuntime, nil
	}
	if _, ok := runtimes["runc"]; ok {
		return "runc", nil
	}
	if len(runtimes) == 0 {
		return "", errors.New("docker did not report any registered container runtimes")
	}
	names := make([]string, 0, len(runtimes))
	for name := range runtimes {
		names = append(names, name)
	}
	sort.Strings(names)
	return "", fmt.Errorf("docker did not report a default runtime; available runtimes: %s", strings.Join(names, ", "))
}

func inspectDockerRuntimes() (map[string]struct{}, string, error) {
	const dockerInfoFormat = "{{range $name, $_ := .Runtimes}}{{$name}}{{println}}{{end}}DEFAULT={{.DefaultRuntime}}"

	output, err := exec.Command("docker", "info", "--format", dockerInfoFormat).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return nil, "", fmt.Errorf("inspect docker runtimes: %w: %s", err, message)
		}
		return nil, "", fmt.Errorf("inspect docker runtimes: %w", err)
	}

	runtimes := map[string]struct{}{}
	defaultRuntime := ""
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "DEFAULT=") {
			defaultRuntime = strings.TrimSpace(strings.TrimPrefix(line, "DEFAULT="))
			continue
		}
		runtimes[line] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, "", fmt.Errorf("read docker runtime probe: %w", err)
	}
	return runtimes, defaultRuntime, nil
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
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
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

func ensureGitExclude(gitDir, pattern string) error {
	infoDir := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return err
	}

	excludePath := filepath.Join(infoDir, "exclude")
	current := ""
	if data, err := os.ReadFile(excludePath); err == nil {
		current = string(data)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	lines := strings.Split(current, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}

	file, err := os.OpenFile(excludePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	if current != "" && !strings.HasSuffix(current, "\n") {
		if _, err := file.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = file.WriteString(pattern + "\n")
	return err
}
