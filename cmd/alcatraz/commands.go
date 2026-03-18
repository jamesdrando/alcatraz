package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func handleList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to a JSON config file")
	asJSON := fs.Bool("json", false, "Print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runtime, err := newRuntime(*configPath)
	if err != nil {
		return err
	}

	runs, err := runtime.loadRuns()
	if err != nil {
		return err
	}
	statuses := make([]RunStatus, 0, len(runs))
	for _, run := range runs {
		statuses = append(statuses, runtime.enrichStatus(run))
	}
	return printStatuses(statuses, *asJSON, os.Stdout)
}

func handleStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to a JSON config file")
	runID := fs.String("run", "", "Run ID")
	asJSON := fs.Bool("json", false, "Print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runID == "" && fs.NArg() > 0 {
		*runID = fs.Arg(0)
	}

	runtime, err := newRuntime(*configPath)
	if err != nil {
		return err
	}
	run, err := runtime.loadRun(*runID)
	if err != nil {
		return err
	}
	return printStatuses([]RunStatus{runtime.enrichStatus(run)}, *asJSON, os.Stdout)
}

func handleConfig(args []string) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to a JSON config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runtime, err := newRuntime(*configPath)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(runtime.Config, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func handleClean(args []string) error {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to a JSON config file")
	runID := fs.String("run", "", "Run ID")
	all := fs.Bool("all", false, "Clean all known runs")
	deleteBranch := fs.Bool("delete-branch", false, "Delete the run branch after removing the worktree")
	asJSON := fs.Bool("json", false, "Print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runID == "" && fs.NArg() > 0 {
		*runID = fs.Arg(0)
	}

	runtime, err := newRuntime(*configPath)
	if err != nil {
		return err
	}

	var runs []RunMetadata
	if *all {
		runs, err = runtime.loadRuns()
		if err != nil {
			return err
		}
	} else {
		run, err := runtime.loadRun(*runID)
		if err != nil {
			return err
		}
		runs = []RunMetadata{run}
	}

	cleaned := make([]RunStatus, 0, len(runs))
	for _, run := range runs {
		status := runtime.enrichStatus(run)
		if err := dockerComposeDown(runtime, run); err != nil {
			return err
		}

		if status.WorktreeExists {
			if _, err := execInDir(runtime.RepoRoot, "git", "worktree", "remove", "--force", run.WorktreePath); err != nil {
				return err
			}
		}

		if *deleteBranch && status.BranchExists {
			if _, err := execInDir(runtime.RepoRoot, "git", "branch", "-D", run.BranchName); err != nil {
				return err
			}
		}

		if err := os.Remove(runtime.metadataPath(run.ID)); err != nil && !os.IsNotExist(err) {
			return err
		}

		status.Running = false
		status.WorktreeExists = false
		if *deleteBranch {
			status.BranchExists = false
		}
		cleaned = append(cleaned, status)
	}

	if *asJSON {
		data, err := json.MarshalIndent(cleaned, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	for _, run := range cleaned {
		fmt.Printf("Removed worktree for %s\n", run.ID)
		if *deleteBranch {
			fmt.Printf("Deleted branch %s\n", run.BranchName)
		}
	}
	return nil
}

func dockerComposeDown(runtime *Runtime, run RunMetadata) error {
	env := runtime.commandEnv(map[string]string{
		"COMPOSE_PROJECT_NAME": run.ComposeProject,
	})
	if path, err := runtime.resolveCodexBin(); err == nil {
		env = append(env, "HOST_CODEX_BIN="+path)
	}

	files := make([]string, 0, len(run.ComposeFiles)*2)
	for _, file := range run.ComposeFiles {
		files = append(files, "-f", filepath.Join(runtime.RepoRoot, file))
	}

	args := append([]string{"compose"}, files...)
	args = append(args, "down", "--remove-orphans")
	cmd := exec.Command("docker", args...)
	cmd.Dir = runtime.RepoRoot
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
