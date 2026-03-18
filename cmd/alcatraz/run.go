package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type runOptions struct {
	ConfigPath string
	BaseRef    string
	BranchName string
	AllowDirty bool
	JSON       bool
}

func handleRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	opts := runOptions{}
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to a JSON config file")
	fs.StringVar(&opts.BaseRef, "base-ref", "", "Base ref to branch from")
	fs.StringVar(&opts.BranchName, "branch", "", "Explicit branch name")
	fs.BoolVar(&opts.AllowDirty, "allow-dirty", false, "Allow starting from a dirty checkout")
	fs.BoolVar(&opts.JSON, "json", false, "Print run metadata as JSON after exit")

	if err := fs.Parse(args); err != nil {
		return err
	}

	runtime, err := newRuntime(opts.ConfigPath)
	if err != nil {
		return err
	}

	if !opts.AllowDirty && !runtime.Config.AllowDirty {
		if err := runtime.ensureCleanCheckout(); err != nil {
			return err
		}
	}

	codexBin, err := runtime.resolveCodexBin()
	if err != nil {
		return err
	}

	authMode, err := runtime.resolveAuthMode()
	if err != nil {
		return err
	}

	baseRef := opts.BaseRef
	if baseRef == "" {
		baseRef = runtime.Config.DefaultBaseRef
	}

	runID := newRunID()
	branchName := opts.BranchName
	if branchName == "" {
		branchName = runtime.Config.BranchPrefix + "/" + runID
	}

	worktreePath, err := runtime.createWorktree(runID, branchName, baseRef)
	if err != nil {
		return err
	}

	meta := RunMetadata{
		ID:             runID,
		BranchName:     branchName,
		BaseRef:        baseRef,
		WorktreePath:   worktreePath,
		ComposeProject: composeProjectName(runtime.Config.ComposeProjectPrefix, runID),
		AuthMode:       authMode,
		ComposeFiles:   runtime.composeFiles(authMode),
		ConfigPath:     runtime.Config.ConfigPath,
		CreatedAt:      timeNowUTC(),
	}
	if err := runtime.writeRunMetadata(meta); err != nil {
		return err
	}

	cmdEnv := runtime.commandEnv(map[string]string{
		"ALCATRAZ_WORKSPACE":   worktreePath,
		"COMPOSE_PROJECT_NAME": meta.ComposeProject,
		"HOST_CODEX_BIN":       codexBin,
	})

	composeFiles := make([]string, 0, len(meta.ComposeFiles)*2)
	for _, file := range meta.ComposeFiles {
		composeFiles = append(composeFiles, "-f", filepath.Join(runtime.RepoRoot, file))
	}

	cleanup := func() {
		downArgs := append([]string{"compose"}, composeFiles...)
		downArgs = append(downArgs, "down", "--remove-orphans")
		cmd := exec.Command("docker", downArgs...)
		cmd.Dir = runtime.RepoRoot
		cmd.Env = cmdEnv
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}
	defer cleanup()

	upArgs := append([]string{"compose"}, composeFiles...)
	upArgs = append(upArgs, "up", "-d", "--build", "egress-proxy")
	upCmd := exec.Command("docker", upArgs...)
	upCmd.Dir = runtime.RepoRoot
	upCmd.Env = cmdEnv
	upCmd.Stdout = os.Stdout
	upCmd.Stderr = os.Stderr
	if err := upCmd.Run(); err != nil {
		return err
	}

	runArgs := append([]string{"compose"}, composeFiles...)
	runArgs = append(runArgs, "run", "--rm", "--no-deps", "--build", "agent")
	runArgs = append(runArgs, runtime.Config.AgentCommand...)
	runArgs = append(runArgs, fs.Args()...)

	agentCmd := exec.Command("docker", runArgs...)
	agentCmd.Dir = runtime.RepoRoot
	agentCmd.Env = cmdEnv
	agentCmd.Stdin = os.Stdin
	agentCmd.Stdout = os.Stdout
	agentCmd.Stderr = os.Stderr

	err = agentCmd.Run()
	if opts.JSON {
		data, marshalErr := json.MarshalIndent(meta, "", "  ")
		if marshalErr == nil {
			fmt.Println(string(data))
		}
	}

	if err == nil {
		fmt.Fprintf(os.Stderr, "[alcatraz] branch ready on host: %s\n", meta.BranchName)
		fmt.Fprintf(os.Stderr, "[alcatraz] worktree preserved at: %s\n", meta.WorktreePath)
		return nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr
	}
	return err
}

func timeNowUTC() time.Time {
	return time.Now().UTC()
}
