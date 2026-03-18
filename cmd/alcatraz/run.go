package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/jamesdrando/alcatraz/internal/dockerops"
	"github.com/jamesdrando/alcatraz/internal/runs"
	rtpkg "github.com/jamesdrando/alcatraz/internal/runtime"
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

	runtime, err := rtpkg.Open(rtpkg.OpenOptions{ConfigPath: opts.ConfigPath})
	if err != nil {
		return err
	}
	service := runs.New(runtime)

	meta, err := service.Create(runs.CreateOptions{
		BaseRef:    opts.BaseRef,
		BranchName: opts.BranchName,
		AllowDirty: opts.AllowDirty,
	})
	if err != nil {
		return err
	}

	err = service.RunInteractive(meta, fs.Args(), dockerops.Streams{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
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
