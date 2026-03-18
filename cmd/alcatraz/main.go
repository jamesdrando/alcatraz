package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

func main() {
	if err := runCLI(os.Args[1:]); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "[alcatraz] %s\n", err)
		os.Exit(1)
	}
}

func runCLI(args []string) error {
	if len(args) == 0 {
		return handleRun(nil)
	}

	switch args[0] {
	case "run":
		return handleRun(args[1:])
	case "list":
		return handleList(args[1:])
	case "status":
		return handleStatus(args[1:])
	case "clean":
		return handleClean(args[1:])
	case "config":
		return handleConfig(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func printUsage() {
	fmt.Println(`Usage:
  alcatraz                  Run with defaults from the current repository
  alcatraz run [flags] [-- codex-args...]
  alcatraz list [--json]
  alcatraz status [run-id] [--json]
  alcatraz clean [run-id|--all] [--delete-branch]
  alcatraz config

Commands:
  run     Create a git worktree, start the isolated container, and launch the agent
  list    List known runs and their worktrees
  status  Show details for one run, or the most recent run by default
  clean   Remove one run or all runs; optionally delete branches too
  config  Print the effective config

Config discovery:
  .alcatraz.json
  .alcatraz/config.json
  alcatraz.json`)
}
