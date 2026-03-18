package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	rtpkg "github.com/jamesdrando/alcatraz/internal/runtime"
	"github.com/jamesdrando/alcatraz/internal/runs"
)

func handleList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to a JSON config file")
	asJSON := fs.Bool("json", false, "Print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runtime, err := rtpkg.Open(rtpkg.OpenOptions{ConfigPath: *configPath})
	if err != nil {
		return err
	}
	service := runs.New(runtime)

	statuses, err := service.ListStatuses()
	if err != nil {
		return err
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

	runtime, err := rtpkg.Open(rtpkg.OpenOptions{ConfigPath: *configPath})
	if err != nil {
		return err
	}
	service := runs.New(runtime)

	status, err := service.GetStatus(*runID)
	if err != nil {
		return err
	}
	return printStatuses([]runs.RunStatus{status}, *asJSON, os.Stdout)
}

func handleConfig(args []string) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to a JSON config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runtime, err := rtpkg.Open(rtpkg.OpenOptions{ConfigPath: *configPath})
	if err != nil {
		return err
	}
	service := runs.New(runtime)

	data, err := json.MarshalIndent(service.EffectiveConfig(), "", "  ")
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

	runtime, err := rtpkg.Open(rtpkg.OpenOptions{ConfigPath: *configPath})
	if err != nil {
		return err
	}
	service := runs.New(runtime)

	var summary runs.CleanupSummary
	if *all {
		summary, err = service.CleanAll(*deleteBranch)
	} else {
		summary, err = service.CleanRun(*runID, *deleteBranch)
	}
	if err != nil {
		return err
	}

	if *asJSON {
		data, err := json.MarshalIndent(summary, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	for _, item := range summary.Runs {
		fmt.Printf("Removed worktree for %s\n", item.RunID)
		if *deleteBranch {
			fmt.Printf("Deleted branch %s\n", item.BranchName)
		}
	}
	return nil
}

func printStatuses(statuses []runs.RunStatus, asJSON bool, out *os.File) error {
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
		dirty := "clean"
		if status.Dirty {
			dirty = "dirty"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", status.ID, status.BranchName, status.Status, dirty, status.WorktreePath)
	}
	return tw.Flush()
}
