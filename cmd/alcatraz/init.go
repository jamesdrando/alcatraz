package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	assets "github.com/jamesdrando/alcatraz"
	"github.com/jamesdrando/alcatraz/internal/config"
	"github.com/jamesdrando/alcatraz/internal/gitops"
)

const (
	defaultInitConfigPath = ".alcatraz/config.json"
	defaultSkillDir       = ".codex/skills"
	initTemplatePrefix    = "templates/init/skills/"
)

type initOptions struct {
	RepoPath       string
	ConfigPath     string
	SkillDir       string
	NoSkills       bool
	NonInteractive bool
	Force          bool
}

type initPlan struct {
	RepoRoot   string
	ConfigPath string
	SkillDir   string
	Force      bool
}

type initResult struct {
	RepoRoot    string
	ConfigPath  string
	SkillDir    string
	Created     []string
	Overwritten []string
	Preserved   []string
}

func handleInit(args []string) error {
	return runInitCommand(args, os.Stdin, os.Stdout, os.Stderr, isInteractiveInput(os.Stdin))
}

func runInitCommand(args []string, stdin io.Reader, stdout, stderr io.Writer, interactiveInput bool) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)

	opts := initOptions{}
	fs.StringVar(&opts.RepoPath, "repo", "", "Path inside the target git repository; defaults to the current repository")
	fs.StringVar(&opts.ConfigPath, "config-path", defaultInitConfigPath, "Path to write the Alcatraz config, relative to the repo root unless absolute")
	fs.StringVar(&opts.SkillDir, "skill-dir", "", "Directory to write project-local Alcatraz skills, relative to the repo root unless absolute")
	fs.BoolVar(&opts.NoSkills, "no-skills", false, "Do not write any project-local skill files")
	fs.BoolVar(&opts.NonInteractive, "non-interactive", false, "Disable interactive prompts and use deterministic defaults")
	fs.BoolVar(&opts.Force, "force", false, "Overwrite generated files if they already exist")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("init does not accept positional arguments")
	}
	if opts.NoSkills && strings.TrimSpace(opts.SkillDir) != "" {
		return errors.New("--no-skills and --skill-dir cannot be used together")
	}

	repoRoot, err := discoverInitRepoRoot(opts.RepoPath)
	if err != nil {
		return err
	}

	interactive := interactiveInput && !opts.NonInteractive
	if interactive {
		printInitBanner(stdout)
	}

	plan, err := resolveInitPlan(opts, repoRoot, stdin, stdout, interactive)
	if err != nil {
		return err
	}

	result, err := executeInit(plan)
	if err != nil {
		return err
	}
	return printInitResult(result, stdout)
}

func discoverInitRepoRoot(repoPath string) (string, error) {
	start := repoPath
	if strings.TrimSpace(start) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		start = cwd
	}

	if !filepath.IsAbs(start) {
		abs, err := filepath.Abs(start)
		if err != nil {
			return "", err
		}
		start = abs
	}

	repoRoot, err := gitops.DiscoverRepoRoot(start)
	if err != nil {
		return "", fmt.Errorf("resolve repository for init: %w", err)
	}
	return repoRoot, nil
}

func resolveInitPlan(opts initOptions, repoRoot string, stdin io.Reader, stdout io.Writer, interactive bool) (initPlan, error) {
	plan := initPlan{
		RepoRoot:   repoRoot,
		ConfigPath: resolveInitPath(repoRoot, opts.ConfigPath, defaultInitConfigPath),
		Force:      opts.Force,
	}

	switch {
	case opts.NoSkills:
		return plan, nil
	case strings.TrimSpace(opts.SkillDir) != "":
		plan.SkillDir = resolveInitPath(repoRoot, opts.SkillDir, defaultSkillDir)
		return plan, nil
	case !interactive:
		plan.SkillDir = filepath.Join(repoRoot, filepath.FromSlash(defaultSkillDir))
		return plan, nil
	default:
		skillDir, err := promptSkillDir(stdin, stdout)
		if err != nil {
			return initPlan{}, err
		}
		if strings.TrimSpace(skillDir) == "" {
			return plan, nil
		}
		plan.SkillDir = resolveInitPath(repoRoot, skillDir, defaultSkillDir)
		return plan, nil
	}
}

func resolveInitPath(repoRoot, value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(repoRoot, filepath.FromSlash(value))
}

func promptSkillDir(stdin io.Reader, stdout io.Writer) (string, error) {
	reader := bufio.NewReader(stdin)
	for {
		fmt.Fprintf(stdout, "Welcome to Alcatraz.\n\n")
		fmt.Fprintf(stdout, "Would you like to add project-local Alcatraz skills for this repository?\n")
		fmt.Fprintf(stdout, "  1. Yes, create skills at %s (recommended)\n", defaultSkillDir)
		fmt.Fprintf(stdout, "  2. Yes, but I will choose the path\n")
		fmt.Fprintf(stdout, "  3. No\n")
		fmt.Fprintf(stdout, "> ")

		choice, err := readPromptLine(reader)
		if err != nil {
			return "", err
		}
		switch strings.ToLower(choice) {
		case "", "1", "y", "yes":
			return defaultSkillDir, nil
		case "2":
			fmt.Fprintf(stdout, "Skill directory path (relative to the repo root unless absolute): ")
			path, err := readPromptLine(reader)
			if err != nil {
				return "", err
			}
			path = strings.TrimSpace(path)
			if path == "" {
				fmt.Fprintln(stdout, "A path is required for option 2.")
				fmt.Fprintln(stdout)
				continue
			}
			return path, nil
		case "3", "n", "no":
			return "", nil
		default:
			fmt.Fprintln(stdout, "Choose 1, 2, or 3.")
			fmt.Fprintln(stdout)
		}
	}
}

func readPromptLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if errors.Is(err, io.EOF) && len(line) == 0 {
		return "", io.EOF
	}
	return strings.TrimSpace(line), nil
}

func executeInit(plan initPlan) (initResult, error) {
	result := initResult{
		RepoRoot:   plan.RepoRoot,
		ConfigPath: plan.ConfigPath,
		SkillDir:   plan.SkillDir,
	}

	cfg, err := buildInitConfig(plan.RepoRoot)
	if err != nil {
		return initResult{}, err
	}
	configData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return initResult{}, err
	}
	configData = append(configData, '\n')

	if err := writeGeneratedFile(plan.ConfigPath, configData, 0o644, plan.Force, &result); err != nil {
		return initResult{}, fmt.Errorf("write config: %w", err)
	}

	if strings.TrimSpace(plan.SkillDir) == "" {
		return result, nil
	}

	for _, template := range assets.InitTemplateFiles() {
		relPath := strings.TrimPrefix(filepath.ToSlash(template.Path), initTemplatePrefix)
		if relPath == template.Path {
			return initResult{}, fmt.Errorf("unsupported init template path: %s", template.Path)
		}
		data, err := assets.ReadInitTemplate(template.Path)
		if err != nil {
			return initResult{}, fmt.Errorf("read init template %s: %w", template.Path, err)
		}
		dest := filepath.Join(plan.SkillDir, filepath.FromSlash(relPath))
		if err := writeGeneratedFile(dest, data, template.Mode, plan.Force, &result); err != nil {
			return initResult{}, fmt.Errorf("write skill template %s: %w", relPath, err)
		}
	}

	return result, nil
}

func buildInitConfig(repoRoot string) (config.Config, error) {
	cfg := config.Default()
	cfg.DefaultBaseRef = "HEAD"

	currentBranch, err := gitops.New(repoRoot).CurrentBranch(repoRoot)
	if err == nil && strings.TrimSpace(currentBranch) != "" {
		cfg.DefaultBaseRef = strings.TrimSpace(currentBranch)
	}

	return cfg, nil
}

func writeGeneratedFile(path string, data []byte, mode fs.FileMode, force bool, result *initResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, data) {
			if info, statErr := os.Stat(path); statErr == nil && info.Mode().Perm() != mode {
				if err := os.Chmod(path, mode); err != nil {
					return err
				}
			}
			result.Preserved = append(result.Preserved, path)
			return nil
		}
		if !force {
			result.Preserved = append(result.Preserved, path)
			return nil
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			return err
		}
		result.Overwritten = append(result.Overwritten, path)
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.WriteFile(path, data, mode); err != nil {
		return err
	}
	result.Created = append(result.Created, path)
	return nil
}

func printInitBanner(out io.Writer) {
	fmt.Fprintln(out, "   _    _           _")
	fmt.Fprintln(out, "  /_\\  | | ___ __ _| |_ _ __ __ _ _____")
	fmt.Fprintln(out, " //_\\\\ | |/ __/ _` | __| '__/ _` |_  /")
	fmt.Fprintln(out, "/  _  \\| | (_| (_| | |_| | | (_| |/ /")
	fmt.Fprintln(out, "\\_/ \\_/_|\\___\\__,_|\\__|_|  \\__,_/___|")
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Alcatraz %s\n\n", version)
}

func printInitResult(result initResult, out io.Writer) error {
	if _, err := fmt.Fprintf(out, "Initialized Alcatraz in %s\n", result.RepoRoot); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Config path: %s\n", displayInitPath(result.RepoRoot, result.ConfigPath)); err != nil {
		return err
	}
	if strings.TrimSpace(result.SkillDir) == "" {
		if _, err := fmt.Fprintln(out, "Skills: not generated"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(out, "Skill root: %s\n", displayInitPath(result.RepoRoot, result.SkillDir)); err != nil {
			return err
		}
	}

	for _, path := range result.Created {
		if _, err := fmt.Fprintf(out, "Created %s\n", displayInitPath(result.RepoRoot, path)); err != nil {
			return err
		}
	}
	for _, path := range result.Overwritten {
		if _, err := fmt.Fprintf(out, "Overwrote %s\n", displayInitPath(result.RepoRoot, path)); err != nil {
			return err
		}
	}
	for _, path := range result.Preserved {
		if _, err := fmt.Fprintf(out, "Kept existing %s\n", displayInitPath(result.RepoRoot, path)); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintln(out, "Default project-local skill convention: .codex/skills"); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, "Next step: review the generated SKILL.md files, choose the harness command in .alcatraz/config.json if you are not using Codex, then create runs with explicit merge targets and path claims.")
	return err
}

func displayInitPath(repoRoot, path string) string {
	rel, err := filepath.Rel(repoRoot, path)
	if err == nil && rel != "" && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." {
		return filepath.ToSlash(rel)
	}
	return path
}

func isInteractiveInput(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
