package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamesdrando/alcatraz/internal/config"
)

func TestRunInitCreatesConfigAndDefaultSkills(t *testing.T) {
	repoRoot := initCLIRepo(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runInitCommand([]string{"--repo", repoRoot, "--non-interactive"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v\nstderr:\n%s", err, stderr.String())
	}

	configPath := filepath.Join(repoRoot, ".alcatraz", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if cfg.DefaultBaseRef != "main" {
		t.Fatalf("unexpected default base ref: %s", cfg.DefaultBaseRef)
	}
	if cfg.EnvFile != ".env" {
		t.Fatalf("unexpected env file: %s", cfg.EnvFile)
	}
	if len(cfg.HarnessCommand) == 0 || cfg.HarnessCommand[0] != "codex" {
		t.Fatalf("unexpected harness command: %+v", cfg.HarnessCommand)
	}
	if strings.Contains(string(data), "\"agent_command\"") {
		t.Fatalf("did not expect compatibility alias in generated config: %s", string(data))
	}

	for _, skillPath := range []string{
		filepath.Join(repoRoot, ".codex", "skills", "alcatraz-orchestrator", "SKILL.md"),
		filepath.Join(repoRoot, ".codex", "skills", "alcatraz-worker", "SKILL.md"),
	} {
		if _, err := os.Stat(skillPath); err != nil {
			t.Fatalf("expected generated skill at %s: %v", skillPath, err)
		}
	}

	if !strings.Contains(stdout.String(), "Default project-local skill convention: .codex/skills") {
		t.Fatalf("expected init summary to mention default skill convention, got:\n%s", stdout.String())
	}
}

func TestRunInitHonorsNoSkills(t *testing.T) {
	repoRoot := initCLIRepo(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runInitCommand([]string{"--repo", repoRoot, "--non-interactive", "--no-skills"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v\nstderr:\n%s", err, stderr.String())
	}

	if _, err := os.Stat(filepath.Join(repoRoot, ".alcatraz", "config.json")); err != nil {
		t.Fatalf("expected config to be created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("did not expect default skill dir to be created: %v", err)
	}
}

func TestRunInitInteractiveSupportsCustomSkillDir(t *testing.T) {
	repoRoot := initCLIRepo(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	input := strings.NewReader("2\nproject-skills\n")
	err := runInitCommand([]string{"--repo", repoRoot}, input, &stdout, &stderr, true)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v\nstderr:\n%s", err, stderr.String())
	}

	customRoot := filepath.Join(repoRoot, "project-skills")
	for _, skillPath := range []string{
		filepath.Join(customRoot, "alcatraz-orchestrator", "SKILL.md"),
		filepath.Join(customRoot, "alcatraz-worker", "SKILL.md"),
	} {
		if _, err := os.Stat(skillPath); err != nil {
			t.Fatalf("expected generated skill at %s: %v", skillPath, err)
		}
	}

	if !strings.Contains(stdout.String(), "Welcome to Alcatraz.") {
		t.Fatalf("expected interactive prompt in stdout, got:\n%s", stdout.String())
	}
}

func initCLIRepo(t *testing.T) string {
	t.Helper()

	repoRoot := t.TempDir()
	runCLICmd(t, repoRoot, "git", "init")
	runCLICmd(t, repoRoot, "git", "checkout", "-b", "main")
	return repoRoot
}

func runCLICmd(t *testing.T, dir string, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, output)
	}
}
