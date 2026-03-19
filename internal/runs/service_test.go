package runs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesdrando/alcatraz/internal/dockerops"
	"github.com/jamesdrando/alcatraz/internal/gitops"
	rtpkg "github.com/jamesdrando/alcatraz/internal/runtime"
)

type fakeDocker struct {
	runningProjects      map[string]bool
	downCalls            int
	upEnv                []string
	runEnv               []string
	upServices           []string
	runCalls             int
	execCalls            int
	execInteractiveCalls int
}

func (f *fakeDocker) UpDetached(composeFiles, env []string, streams dockerops.Streams, services ...string) error {
	f.upEnv = append([]string{}, env...)
	f.upServices = append([]string{}, services...)
	return nil
}

func (f *fakeDocker) Down(composeFiles, env []string, streams dockerops.Streams) error {
	f.downCalls++
	return nil
}

func (f *fakeDocker) RunService(composeFiles, env []string, streams dockerops.Streams, service string, command []string) error {
	f.runCalls++
	f.runEnv = append([]string{}, env...)
	return nil
}

func (f *fakeDocker) ExecService(composeFiles, env []string, streams dockerops.Streams, service string, command []string) error {
	f.execCalls++
	f.runEnv = append([]string{}, env...)
	return nil
}

func (f *fakeDocker) ExecServiceInteractive(composeFiles, env []string, streams dockerops.Streams, service string, command []string) error {
	f.execInteractiveCalls++
	f.runEnv = append([]string{}, env...)
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
	if len(meta.ComposeFiles) != 2 {
		t.Fatalf("unexpected compose file count: %+v", meta.ComposeFiles)
	}
	for _, composeFile := range meta.ComposeFiles {
		if !filepath.IsAbs(composeFile) {
			t.Fatalf("compose file is not absolute: %s", composeFile)
		}
		if !strings.HasPrefix(composeFile, runtime.StateDir+string(os.PathSeparator)) {
			t.Fatalf("compose file should be staged under runtime state dir: %s", composeFile)
		}
		if composeFile == filepath.Join(repoRoot, filepath.Base(composeFile)) {
			t.Fatalf("compose file should not be resolved from target repo root: %s", composeFile)
		}
		if _, err := os.Stat(composeFile); err != nil {
			t.Fatalf("staged compose file missing: %v", err)
		}
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

func TestCreateBootstrapsEnvFileWhenMissing(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	service := NewForTesting(runtime, gitops.New(repoRoot), &fakeDocker{runningProjects: map[string]bool{}})
	service.newRunID = func() string { return "20260318-000001-feed" }

	if _, err := service.Create(CreateOptions{}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	envPath := filepath.Join(repoRoot, ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read generated env file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "OPENAI_API_KEY=test-key") {
		t.Fatalf("generated env file missing OPENAI_API_KEY: %s", content)
	}
	if !strings.Contains(content, "HOST_CODEX_BIN=/bin/sh") {
		t.Fatalf("generated env file missing HOST_CODEX_BIN: %s", content)
	}

	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("stat generated env file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected env file permissions 0600, got %o", info.Mode().Perm())
	}

	excludeData, err := os.ReadFile(filepath.Join(repoRoot, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("read git exclude: %v", err)
	}
	if !strings.Contains(string(excludeData), "/.env") {
		t.Fatalf("git exclude missing /.env entry: %s", string(excludeData))
	}
}

func TestCreatePreservesExistingEnvFile(t *testing.T) {
	repoRoot := initRepo(t)
	envPath := filepath.Join(repoRoot, ".env")
	original := "OPENAI_API_KEY=keep-me\n"
	if err := os.WriteFile(envPath, []byte(original), 0o600); err != nil {
		t.Fatalf("write existing env file: %v", err)
	}

	runtime := newTestRuntime(t, repoRoot)
	service := NewForTesting(runtime, gitops.New(repoRoot), &fakeDocker{runningProjects: map[string]bool{}})
	service.newRunID = func() string { return "20260318-000001-face" }

	if _, err := service.Create(CreateOptions{}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env file after create: %v", err)
	}
	if string(data) != original {
		t.Fatalf("existing env file was overwritten: %s", string(data))
	}
}

func TestRunInteractivePassesDependencySettingsToCompose(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	runtime.Config.DependencyProfiles = []string{"typescript", "python"}
	runtime.Config.NodePackages = []string{"hono", "decimal.js"}
	runtime.Config.PythonPackages = []string{"fastapi", "sqlmodel", "uv"}
	runtime.Config.GoModules = []string{"github.com/jackc/pgx/v5@v5.7.1"}

	docker := &fakeDocker{runningProjects: map[string]bool{}}
	service := NewForTesting(runtime, gitops.New(repoRoot), docker)
	service.newRunID = func() string { return "20260318-000004-dead" }

	meta, err := service.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := service.RunInteractive(meta, nil, dockerops.Streams{}); err != nil {
		t.Fatalf("RunInteractive() error = %v", err)
	}

	if !hasEnvValue(docker.upEnv, "ALCATRAZ_DEP_PROFILES", "typescript,python") {
		t.Fatalf("missing dependency profiles in compose env: %+v", docker.upEnv)
	}
	if !hasEnvValue(docker.upEnv, "ALCATRAZ_NODE_PACKAGES", "hono,decimal.js") {
		t.Fatalf("missing node packages in compose env: %+v", docker.upEnv)
	}
	if !hasEnvValue(docker.upEnv, "ALCATRAZ_PYTHON_PACKAGES", "fastapi,sqlmodel,uv") {
		t.Fatalf("missing python packages in compose env: %+v", docker.upEnv)
	}
	if !hasEnvValue(docker.upEnv, "ALCATRAZ_GO_MODULES", "github.com/jackc/pgx/v5@v5.7.1") {
		t.Fatalf("missing go modules in compose env: %+v", docker.upEnv)
	}
	if !hasEnvValue(docker.upEnv, "ALCATRAZ_CONTAINER_RUNTIME", "runc") {
		t.Fatalf("missing container runtime in compose env: %+v", docker.upEnv)
	}
	if len(docker.upServices) != 2 || docker.upServices[0] != "egress-proxy" || docker.upServices[1] != "agent" {
		t.Fatalf("expected interactive run to start egress-proxy and agent services, got %+v", docker.upServices)
	}
	if docker.runCalls != 0 {
		t.Fatalf("expected interactive run not to use compose run, got %d calls", docker.runCalls)
	}
	if docker.execInteractiveCalls != 1 {
		t.Fatalf("expected one interactive exec call, got %d", docker.execInteractiveCalls)
	}
}

func TestFinishCommitsMergesAndCleans(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	docker := &fakeDocker{runningProjects: map[string]bool{}}
	service := NewForTesting(runtime, gitops.New(repoRoot), docker)
	service.newRunID = func() string { return "20260318-000002-cafe" }

	meta, err := service.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	filePath := filepath.Join(meta.WorktreePath, "mcp.txt")
	if err := os.WriteFile(filePath, []byte("mcp\n"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}

	result, err := service.Finish(FinishOptions{
		RunID:        meta.ID,
		Merge:        true,
		Clean:        true,
		DeleteBranch: true,
	})
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}

	if !result.CommitCreated {
		t.Fatal("expected a commit to be created")
	}
	if !result.Merged {
		t.Fatal("expected branch to be merged")
	}
	if !result.WorktreeRemoved || !result.BranchDeleted || !result.MetadataRemoved {
		t.Fatalf("unexpected finish result: %+v", result)
	}
	if docker.downCalls != 1 {
		t.Fatalf("expected one down call, got %d", docker.downCalls)
	}

	mergedFile := filepath.Join(repoRoot, "mcp.txt")
	if _, err := os.Stat(mergedFile); err != nil {
		t.Fatalf("merged file missing from repo root: %v", err)
	}
}

func TestFinishMergesAndCleansWhenRunWorktreeIsAlreadyCommitted(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	docker := &fakeDocker{runningProjects: map[string]bool{}}
	service := NewForTesting(runtime, gitops.New(repoRoot), docker)
	service.newRunID = func() string { return "20260318-000003-babe" }

	meta, err := service.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	filePath := filepath.Join(meta.WorktreePath, "clean.txt")
	if err := os.WriteFile(filePath, []byte("already committed\n"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}
	runCmd(t, meta.WorktreePath, "git", "add", "clean.txt")
	runCmd(t, meta.WorktreePath, "git", "commit", "-m", "commit before finish")

	result, err := service.Finish(FinishOptions{
		RunID:        meta.ID,
		Merge:        true,
		Clean:        true,
		DeleteBranch: true,
	})
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}

	if result.CommitCreated {
		t.Fatal("expected no new commit to be created by finish")
	}
	if !result.Merged {
		t.Fatal("expected branch to be merged")
	}
	if !result.WorktreeRemoved || !result.BranchDeleted || !result.MetadataRemoved {
		t.Fatalf("unexpected finish result: %+v", result)
	}
	if docker.downCalls != 1 {
		t.Fatalf("expected one down call, got %d", docker.downCalls)
	}

	mergedFile := filepath.Join(repoRoot, "clean.txt")
	data, err := os.ReadFile(mergedFile)
	if err != nil {
		t.Fatalf("merged file missing from repo root: %v", err)
	}
	if string(data) != "already committed\n" {
		t.Fatalf("unexpected merged file content: %q", string(data))
	}
}

func TestCreateRejectsUnknownBundledComposeAsset(t *testing.T) {
	repoRoot := initRepo(t)
	configPath := filepath.Join(repoRoot, ".alcatraz.json")
	if err := os.WriteFile(configPath, []byte("{\"compose_files\":[\"custom.yaml\"]}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := rtpkg.Open(rtpkg.OpenOptions{
		RepoRoot: repoRoot,
		Environ: []string{
			"ALCATRAZ_CONTAINER_RUNTIME=runc",
			"OPENAI_API_KEY=test-key",
			"HOST_CODEX_BIN=/bin/sh",
			"HOME=" + repoRoot,
		},
	})
	if err == nil {
		t.Fatal("expected invalid compose asset error")
	}
	if !strings.Contains(err.Error(), "unsupported bundled compose asset") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newTestRuntime(t *testing.T, repoRoot string) *rtpkg.Runtime {
	t.Helper()

	runtime, err := rtpkg.Open(rtpkg.OpenOptions{
		RepoRoot: repoRoot,
		Environ: []string{
			"ALCATRAZ_CONTAINER_RUNTIME=runc",
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

func hasEnvValue(env []string, key, want string) bool {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) && strings.TrimPrefix(entry, prefix) == want {
			return true
		}
	}
	return false
}

func runCmd(t *testing.T, dir string, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, output)
	}
}
