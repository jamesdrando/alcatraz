package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/jamesdrando/alcatraz/internal/runs"
	rtpkg "github.com/jamesdrando/alcatraz/internal/runtime"
)

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	*f = append(*f, value)
	return nil
}

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

func handleDiff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to a JSON config file")
	runID := fs.String("run", "", "Run ID")
	stat := fs.Bool("stat", false, "Show a diff summary instead of the full patch")
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

	out, err := service.Diff(*runID, *stat)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

func handleFinish(args []string) error {
	fs := flag.NewFlagSet("finish", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to a JSON config file")
	runID := fs.String("run", "", "Run ID")
	message := fs.String("message", "", "Commit message for changes in the run worktree")
	fs.StringVar(message, "m", "", "Commit message for changes in the run worktree")
	status := fs.String("status", "", "Structured completion status: ready, blocked, or ready_with_assumptions")
	summary := fs.String("summary", "", "Short completion summary")
	var assumptions stringListFlag
	var followups stringListFlag
	var needsChanges stringListFlag
	fs.Var(&assumptions, "assumption", "Record an assumption for this run; repeat to add more")
	fs.Var(&followups, "followup", "Record a suggested follow-up item; repeat to add more")
	fs.Var(&needsChanges, "needs-change", "Record a cross-scope change as path:description or description; repeat to add more")
	merge := fs.Bool("merge", false, "Merge the run branch into its recorded merge target")
	into := fs.String("into", "", "Branch to merge into; defaults to the run's recorded merge target")
	clean := fs.Bool("clean", false, "Remove the run worktree after finishing")
	deleteBranch := fs.Bool("delete-branch", false, "Delete the run branch after finishing")
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

	parsedNeedsChanges, err := parseNeedsChanges(needsChanges)
	if err != nil {
		return err
	}

	result, err := service.Finish(runs.FinishOptions{
		RunID:         *runID,
		CommitMessage: *message,
		Status:        runs.RunCompletionStatus(strings.TrimSpace(*status)),
		Summary:       *summary,
		NeedsChanges:  parsedNeedsChanges,
		Assumptions:   []string(assumptions),
		Followups:     []string(followups),
		Merge:         *merge,
		MergeInto:     *into,
		Clean:         *clean,
		DeleteBranch:  *deleteBranch,
	})
	if err != nil {
		return err
	}

	if *asJSON {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	return printFinishResult(result, os.Stdout)
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

func printFinishResult(result runs.FinishResult, out io.Writer) error {
	if result.CommitCreated {
		if _, err := fmt.Fprintf(out, "Committed changes on %s\n", result.BranchName); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(out, "No new worktree changes to commit on %s\n", result.BranchName); err != nil {
			return err
		}
	}
	if result.Merged {
		if _, err := fmt.Fprintf(out, "Merged %s into %s\n", result.BranchName, result.MergeTarget); err != nil {
			return err
		}
	}
	if result.CompletionSaved {
		if _, err := fmt.Fprintf(out, "Recorded structured completion state for %s\n", result.RunID); err != nil {
			return err
		}
	}
	if result.WorktreeRemoved {
		if _, err := fmt.Fprintf(out, "Removed worktree for %s\n", result.RunID); err != nil {
			return err
		}
	}
	if result.BranchDeleted {
		if _, err := fmt.Fprintf(out, "Deleted branch %s\n", result.BranchName); err != nil {
			return err
		}
	}
	return nil
}

func parseNeedsChanges(values []string) ([]runs.ChangeRequest, error) {
	items := make([]runs.ChangeRequest, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		item := runs.ChangeRequest{}
		if idx := strings.Index(value, ":"); idx >= 0 {
			item.Path = strings.TrimSpace(value[:idx])
			item.Description = strings.TrimSpace(value[idx+1:])
		} else {
			item.Description = value
		}
		if item.Description == "" {
			return nil, fmt.Errorf("invalid --needs-change value %q: description is required", value)
		}
		items = append(items, item)
	}
	return items, nil
}
