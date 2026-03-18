package main

import (
	"bytes"
	"testing"

	"github.com/jamesdrando/alcatraz/internal/runs"
)

func TestPrintFinishResultIncludesFollowOnActionsAfterNoOpCommit(t *testing.T) {
	var out bytes.Buffer

	err := printFinishResult(runs.FinishResult{
		RunID:           "20260318-160551-3196",
		BranchName:      "alcatraz/20260318-160551-3196",
		CommitCreated:   false,
		Merged:          true,
		MergeTarget:     "main",
		WorktreeRemoved: true,
		BranchDeleted:   true,
	}, &out)
	if err != nil {
		t.Fatalf("printFinishResult() error = %v", err)
	}

	want := "" +
		"No new worktree changes to commit on alcatraz/20260318-160551-3196\n" +
		"Merged alcatraz/20260318-160551-3196 into main\n" +
		"Removed worktree for 20260318-160551-3196\n" +
		"Deleted branch alcatraz/20260318-160551-3196\n"
	if out.String() != want {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}
