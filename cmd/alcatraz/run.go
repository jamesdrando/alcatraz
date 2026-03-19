package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/jamesdrando/alcatraz/internal/dockerops"
	"github.com/jamesdrando/alcatraz/internal/runs"
	rtpkg "github.com/jamesdrando/alcatraz/internal/runtime"
)

type runOptions struct {
	ConfigPath         string
	BaseRef            string
	BranchName         string
	AllowDirty         bool
	DependencyProfiles string
	AptPackages        string
	NodePackages       string
	PythonPackages     string
	GoModules          string
	JSON               bool
}

func handleRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	opts := runOptions{}
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to a JSON config file")
	fs.StringVar(&opts.BaseRef, "base-ref", "", "Base ref to branch from")
	fs.StringVar(&opts.BranchName, "branch", "", "Explicit branch name")
	fs.BoolVar(&opts.AllowDirty, "allow-dirty", false, "Allow starting from a dirty checkout")
	fs.StringVar(&opts.DependencyProfiles, "deps", "", "Comma-separated dependency profiles to bake into the agent image")
	fs.StringVar(&opts.AptPackages, "apt-packages", "", "Comma-separated extra apt packages to bake into the agent image")
	fs.StringVar(&opts.NodePackages, "node-packages", "", "Comma-separated global Node.js packages to bake into the agent image")
	fs.StringVar(&opts.PythonPackages, "python-packages", "", "Comma-separated global Python packages to bake into the agent image")
	fs.StringVar(&opts.GoModules, "go-modules", "", "Comma-separated Go modules to prefetch into the image cache")
	fs.BoolVar(&opts.JSON, "json", false, "Print run metadata as JSON after exit")

	if err := fs.Parse(args); err != nil {
		return err
	}

	runtime, err := rtpkg.Open(rtpkg.OpenOptions{ConfigPath: opts.ConfigPath})
	if err != nil {
		return err
	}

	runtime.Config.DependencyProfiles = mergeStringLists(runtime.Config.DependencyProfiles, splitList(opts.DependencyProfiles))
	runtime.Config.AptPackages = mergeStringLists(runtime.Config.AptPackages, splitList(opts.AptPackages))
	runtime.Config.NodePackages = mergeStringLists(runtime.Config.NodePackages, splitList(opts.NodePackages))
	runtime.Config.PythonPackages = mergeStringLists(runtime.Config.PythonPackages, splitList(opts.PythonPackages))
	runtime.Config.GoModules = mergeStringLists(runtime.Config.GoModules, splitList(opts.GoModules))

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

func splitList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', ' ', '\t', '\n', '\r':
			return true
		default:
			return false
		}
	})
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}

func mergeStringLists(base []string, extra []string) []string {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	appendUnique := func(values []string) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}

	appendUnique(base)
	appendUnique(extra)
	return out
}
