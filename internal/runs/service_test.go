package runs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jamesdrando/alcatraz/internal/dockerops"
	"github.com/jamesdrando/alcatraz/internal/gitops"
	rtpkg "github.com/jamesdrando/alcatraz/internal/runtime"
)

type fakeDocker struct {
	runningProjects map[string]bool
	downCalls       int
}

func (f *fakeDocker) UpDetached(composeFiles, env []string, streams dockerops.Streams, services ...string) error {
	return nil
}

func (f *fakeDocker) Down(composeFiles, env []string, streams dockerops.Streams) error {
	f.downCalls++
	return nil
}

func (f *fakeDocker) RunService(composeFiles, env []string, streams dockerops.Streams, service string, command []string) error {
	return nil
}

func (f *fakeDocker) ExecService(composeFiles, env []string, streams dockerops.Streams, service string, command []string) error {
	return nil
}

func (f *fakeDocker) ProjectRunning(project string) (bool, error) {
	return f.runningProjects[project], nil
}

func TestCreateListAndCleanRun(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	docker := &fakeDocker{runningProjects: map[string]bool{}}
	service := NewForTesting(runtime, gitops.New(repoRoot), docker)
	service.newRunID = func() string { return "20260318-000000-abcd" }
	service.now = func() time.Time { return time.Date(2026, 3, 18, 20, 0, 0, 0, time.UTC) }

	meta, err := service.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if meta.ID != "20260318-000000-abcd" {
		t.Fatalf("unexpected run id: %s", meta.ID)
	}
	if meta.BranchName != "alcatraz/20260318-000000-abcd" {
		t.Fatalf("unexpected branch name: %s", meta.BranchName)
	}
	if _, err := os.Stat(runtime.MetadataPath(meta.ID)); err != nil {
		t.Fatalf("metadata file missing: %v", err)
	}
	if _, err := os.Stat(meta.WorktreePath); err != nil {
		t.Fatalf("worktree missing: %v", err)
	}

	docker.runningProjects[meta.ComposeProject] = true
	status, err := service.GetStatus(meta.ID)
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if !status.Running || status.Status != "running" {
		t.Fatalf("unexpected status: %+v", status)
	}

	items, err := service.ListStatuses()
	if err != nil {
		t.Fatalf("ListStatuses() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != meta.ID {
		t.Fatalf("unexpected runs: %+v", items)
	}

	docker.runningProjects[meta.ComposeProject] = false
	summary, err := service.CleanRun(meta.ID, true)
	if err != nil {
		t.Fatalf("CleanRun() error = %v", err)
	}
	if len(summary.Runs) != 1 {
		t.Fatalf("unexpected cleanup summary: %+v", summary)
	}
	result := summary.Runs[0]
	if !result.WorktreeRemoved || !result.BranchDeleted || !result.MetadataRemoved {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}
	if docker.downCalls != 1 {
		t.Fatalf("expected one down call, got %d", docker.downCalls)
	}
	if _, err := os.Stat(meta.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after cleanup: %v", err)
	}
	if _, err := os.Stat(runtime.MetadataPath(meta.ID)); !os.IsNotExist(err) {
		t.Fatalf("metadata still exists after cleanup: %v", err)
	}
	if exists := gitBranchExists(t, repoRoot, meta.BranchName); exists {
		t.Fatalf("branch still exists after cleanup: %s", meta.BranchName)
	}
}

func TestCreateRejectsDirtyCheckoutUnlessAllowed(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	service := NewForTesting(runtime, gitops.New(repoRoot), &fakeDocker{runningProjects: map[string]bool{}})
	service.newRunID = func() string { return "20260318-000001-beef" }

	dirtyFile := filepath.Join(repoRoot, "dirty.txt")
	if err := os.WriteFile(dirtyFile, []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	if _, err := service.Create(CreateOptions{}); err == nil {
		t.Fatal("expected dirty checkout error")
	}

	meta, err := service.Create(CreateOptions{AllowDirty: true})
	if err != nil {
		t.Fatalf("Create() with AllowDirty error = %v", err)
	}
	if meta.ID != "20260318-000001-beef" {
		t.Fatalf("unexpected run id: %s", meta.ID)
	}
}

func newTestRuntime(t *testing.T, repoRoot string) *rtpkg.Runtime {
	t.Helper()

	runtime, err := rtpkg.Open(rtpkg.OpenOptions{
		RepoRoot: repoRoot,
		Environ: []string{
			"OPENAI_API_KEY=test-key",
			"HOST_CODEX_BIN=/bin/sh",
			"HOME=" + repoRoot,
		},
	})
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	return runtime
}

func initRepo(t *testing.T) string {
	t.Helper()

	repoRoot := t.TempDir()
	runCmd(t, repoRoot, "git", "init")
	runCmd(t, repoRoot, "git", "config", "user.name", "Alcatraz Test")
	runCmd(t, repoRoot, "git", "config", "user.email", "test@example.com")

	readmePath := filepath.Join(repoRoot, "README.md")
	if err := os.WriteFile(readmePath, []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	runCmd(t, repoRoot, "git", "add", "README.md")
	runCmd(t, repoRoot, "git", "commit", "-m", "initial commit")
	return repoRoot
}

func gitBranchExists(t *testing.T, repoRoot, branchName string) bool {
	t.Helper()

	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branchName)
	cmd.Dir = repoRoot
	err := cmd.Run()
	return err == nil
}

func runCmd(t *testing.T, dir string, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, output)
	}
}
