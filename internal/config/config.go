package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	ConfigPath           string   `json:"config_path,omitempty"`
	BranchPrefix         string   `json:"branch_prefix"`
	ComposeProjectPrefix string   `json:"compose_project_prefix"`
	DefaultBaseRef       string   `json:"default_base_ref"`
	AllowDirty           bool     `json:"allow_dirty"`
	EnvFile              string   `json:"env_file"`
	ComposeFiles         []string `json:"compose_files"`
	ChatGPTComposeFile   string   `json:"chatgpt_compose_file"`
	AgentCommand         []string `json:"agent_command"`
}

func Default() Config {
	return Config{
		BranchPrefix:         "alcatraz",
		ComposeProjectPrefix: "alcatraz",
		DefaultBaseRef:       "HEAD",
		AllowDirty:           false,
		EnvFile:              ".env",
		ComposeFiles:         []string{"compose.yaml", "compose.codex.yaml"},
		ChatGPTComposeFile:   "compose.chatgpt.yaml",
		AgentCommand:         []string{"codex", "--dangerously-bypass-approvals-and-sandbox", "-C", "/workspace"},
	}
}

func Load(repoRoot, explicitPath string) (Config, error) {
	cfg := Default()

	path, err := ResolvePath(repoRoot, explicitPath)
	if err != nil {
		return Config{}, err
	}
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	cfg.ConfigPath = path
	applyDefaults(&cfg)
	return cfg, nil
}

func ResolvePath(repoRoot, explicitPath string) (string, error) {
	if explicitPath != "" {
		if filepath.IsAbs(explicitPath) {
			return explicitPath, nil
		}
		return filepath.Join(repoRoot, explicitPath), nil
	}

	candidates := []string{
		filepath.Join(repoRoot, ".alcatraz.json"),
		filepath.Join(repoRoot, ".alcatraz", "config.json"),
		filepath.Join(repoRoot, "alcatraz.json"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}

	return "", nil
}

func applyDefaults(cfg *Config) {
	defaults := Default()
	if len(cfg.ComposeFiles) == 0 {
		cfg.ComposeFiles = defaults.ComposeFiles
	}
	if len(cfg.AgentCommand) == 0 {
		cfg.AgentCommand = defaults.AgentCommand
	}
	if cfg.BranchPrefix == "" {
		cfg.BranchPrefix = defaults.BranchPrefix
	}
	if cfg.ComposeProjectPrefix == "" {
		cfg.ComposeProjectPrefix = defaults.ComposeProjectPrefix
	}
	if cfg.DefaultBaseRef == "" {
		cfg.DefaultBaseRef = defaults.DefaultBaseRef
	}
	if cfg.EnvFile == "" {
		cfg.EnvFile = defaults.EnvFile
	}
	if cfg.ChatGPTComposeFile == "" {
		cfg.ChatGPTComposeFile = defaults.ChatGPTComposeFile
	}
}
