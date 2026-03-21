package runs

import (
	"fmt"
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
	upCalls              [][]string
	runCalls             int
	execCalls            int
	execInteractiveCalls int
	execOutputCalls      int
	serviceNetworkIP     string
	execInteractiveErr   error
	execOutputs          map[string]string
	execOutputErrors     map[string]error
	serviceLogs          map[string]string
}

func (f *fakeDocker) UpDetached(composeFiles, env []string, streams dockerops.Streams, services ...string) error {
	f.upEnv = append([]string{}, env...)
	f.upServices = append([]string{}, services...)
	f.upCalls = append(f.upCalls, append([]string{}, services...))
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
	return f.execInteractiveErr
}

func (f *fakeDocker) ExecServiceOutput(composeFiles, env []string, service string, command []string) (string, error) {
	f.execOutputCalls++
	key := dockerCommandKey(service, command)
	if output, ok := f.execOutputs[key]; ok {
		return output, f.execOutputErrors[key]
	}
	return "", f.execOutputErrors[key]
}

func (f *fakeDocker) ServiceLogs(composeFiles, env []string, service string, tailLines int) (string, error) {
	if f.serviceLogs == nil {
		return "", nil
	}
	return f.serviceLogs[service], nil
}

func (f *fakeDocker) ServiceNetworkIP(composeFiles, env []string, service, network string) (string, error) {
	if f.serviceNetworkIP == "" {
		return "192.168.80.2", nil
	}
	return f.serviceNetworkIP, nil
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
	if meta.MergeTarget != currentBranch(t, repoRoot) {
		t.Fatalf("unexpected merge target: %s", meta.MergeTarget)
	}
	if meta.BaseCommit == "" {
		t.Fatal("expected base commit to be recorded")
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
	if !hasEnvValue(docker.upEnv, "ALCATRAZ_EGRESS_PROXY_RUNTIME", "runc") {
		t.Fatalf("missing egress proxy runtime in compose env: %+v", docker.upEnv)
	}
	if !hasEnvValue(docker.upEnv, "ALCATRAZ_EGRESS_PROXY", "http://192.168.80.2:3128") {
		t.Fatalf("missing resolved egress proxy URL in compose env: %+v", docker.upEnv)
	}
	if !hasEnvKey(docker.upEnv, "ALCATRAZ_EGRESS_DNS_1") || !hasEnvKey(docker.upEnv, "ALCATRAZ_EGRESS_DNS_2") {
		t.Fatalf("missing explicit egress DNS configuration in compose env: %+v", docker.upEnv)
	}
	if len(docker.upCalls) != 2 {
		t.Fatalf("expected two compose up calls, got %+v", docker.upCalls)
	}
	if len(docker.upCalls[0]) != 1 || docker.upCalls[0][0] != "egress-proxy" {
		t.Fatalf("expected first compose up to start only egress-proxy, got %+v", docker.upCalls[0])
	}
	if len(docker.upCalls[1]) != 1 || docker.upCalls[1][0] != "agent" {
		t.Fatalf("expected second compose up to start only agent, got %+v", docker.upCalls[1])
	}
	if docker.runCalls != 0 {
		t.Fatalf("expected interactive run not to use compose run, got %d calls", docker.runCalls)
	}
	if docker.execInteractiveCalls != 1 {
		t.Fatalf("expected one interactive exec call, got %d", docker.execInteractiveCalls)
	}
	if docker.execOutputCalls == 0 {
		t.Fatal("expected network preflight to run before interactive exec")
	}
}

func TestRunInteractiveReportsNetworkPreflightFailure(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	runtime.Env["OPENAI_API_KEY"] = ""
	hostCodexHome := t.TempDir()
	if err := os.MkdirAll(hostCodexHome, 0o755); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostCodexHome, "auth.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
	runtime.Env["HOST_CODEX_HOME"] = hostCodexHome

	docker := &fakeDocker{
		runningProjects: map[string]bool{},
		execOutputs: map[string]string{
			dockerCommandKey("agent", preflightCurlCommand("chatgpt.com")):      "curl: (56) CONNECT tunnel failed, response 500",
			dockerCommandKey("agent", agentProxyEnvCommand()):                   "HTTPS_PROXY=http://192.168.80.2:3128\nHTTP_PROXY=http://192.168.80.2:3128",
			dockerCommandKey("egress-proxy", proxyResolvConfCommand()):          "nameserver 127.0.0.11",
			dockerCommandKey("egress-proxy", proxyLookupCommand("chatgpt.com")): "",
		},
		execOutputErrors: map[string]error{
			dockerCommandKey("agent", preflightCurlCommand("chatgpt.com")): fmt.Errorf("exit status 56"),
		},
		serviceLogs: map[string]string{
			"egress-proxy": "NONE_NONE/500 0 CONNECT chatgpt.com:443 - HIER_NONE/- -",
		},
	}

	service := NewForTesting(runtime, gitops.New(repoRoot), docker)
	service.newRunID = func() string { return "20260319-000004-dead" }

	meta, err := service.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	err = service.RunInteractive(meta, nil, dockerops.Streams{})
	if err == nil {
		t.Fatal("expected network preflight failure")
	}
	message := err.Error()
	if !strings.Contains(message, "network preflight failed for chatgpt.com") {
		t.Fatalf("missing preflight header: %s", message)
	}
	if !strings.Contains(message, "curl: (56) CONNECT tunnel failed, response 500") {
		t.Fatalf("missing curl diagnostics: %s", message)
	}
	if !strings.Contains(message, "egress-proxy logs (tail 30):") {
		t.Fatalf("missing proxy logs: %s", message)
	}
	if !strings.Contains(message, "compose project preserved for inspection: "+meta.ComposeProject) {
		t.Fatalf("missing preserved project hint: %s", message)
	}
	if docker.execInteractiveCalls != 0 {
		t.Fatalf("expected interactive exec not to run after preflight failure, got %d", docker.execInteractiveCalls)
	}
	if docker.downCalls != 0 {
		t.Fatalf("expected failed preflight to preserve containers, got %d down calls", docker.downCalls)
	}
}

func TestRunInteractivePreservesContainersWhenAgentCommandFails(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	docker := &fakeDocker{
		runningProjects:    map[string]bool{},
		execInteractiveErr: fmt.Errorf("exit status 1"),
	}
	service := NewForTesting(runtime, gitops.New(repoRoot), docker)
	service.newRunID = func() string { return "20260319-000005-beef" }

	meta, err := service.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	err = service.RunInteractive(meta, nil, dockerops.Streams{})
	if err == nil {
		t.Fatal("expected interactive exec failure")
	}
	if docker.downCalls != 0 {
		t.Fatalf("expected failed interactive exec to preserve containers, got %d down calls", docker.downCalls)
	}
}

func TestParseNameserversSkipsEmbeddedDockerDNS(t *testing.T) {
	resolvConf := strings.Join([]string{
		"nameserver 127.0.0.11",
		"nameserver 172.30.32.1",
		"nameserver 1.1.1.1",
		"nameserver 172.30.32.1",
		"",
	}, "\n")

	got := parseNameservers(resolvConf)
	want := []string{"172.30.32.1", "1.1.1.1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("parseNameservers() = %v, want %v", got, want)
	}
}

func TestDefaultEgressDNSServersFallsBackAndDuplicatesSingleResolver(t *testing.T) {
	if got := defaultEgressDNSServers(nil); strings.Join(got, ",") != "1.1.1.1,8.8.8.8" {
		t.Fatalf("defaultEgressDNSServers(nil) = %v", got)
	}
	if got := defaultEgressDNSServers([]string{"172.30.32.1"}); strings.Join(got, ",") != "172.30.32.1,172.30.32.1" {
		t.Fatalf("defaultEgressDNSServers(single) = %v", got)
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

func TestFinishUsesRecordedMergeTargetByDefault(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	docker := &fakeDocker{runningProjects: map[string]bool{}}
	service := NewForTesting(runtime, gitops.New(repoRoot), docker)
	service.newRunID = func() string { return "20260318-000006-feed" }

	expectedTarget := currentBranch(t, repoRoot)

	meta, err := service.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	filePath := filepath.Join(meta.WorktreePath, "targeted.txt")
	if err := os.WriteFile(filePath, []byte("targeted\n"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}

	runCmd(t, repoRoot, "git", "switch", "-c", "scratch/integration-check")

	result, err := service.Finish(FinishOptions{
		RunID: meta.ID,
		Merge: true,
	})
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}

	if result.MergeTarget != expectedTarget {
		t.Fatalf("Finish() merge target = %q, want %q", result.MergeTarget, expectedTarget)
	}
	if current := currentBranch(t, repoRoot); current != expectedTarget {
		t.Fatalf("current branch after finish = %q, want %q", current, expectedTarget)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "targeted.txt")); err != nil {
		t.Fatalf("merged file missing from merge target: %v", err)
	}
}

func TestFinishRejectsChangesOutsideOwnedPaths(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	docker := &fakeDocker{runningProjects: map[string]bool{}}
	service := NewForTesting(runtime, gitops.New(repoRoot), docker)
	service.newRunID = func() string { return "20260318-000007-c0de" }

	meta, err := service.Create(CreateOptions{OwnedPaths: []string{"internal/mcp"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	filePath := filepath.Join(meta.WorktreePath, "README.md")
	if err := os.WriteFile(filePath, []byte("out of scope\n"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}

	_, err = service.Finish(FinishOptions{
		RunID:   meta.ID,
		Status:  RunCompletionStatusBlocked,
		Summary: "needs wider change",
	})
	if err == nil {
		t.Fatal("expected ownership error")
	}
	if !strings.Contains(err.Error(), "outside its claimed scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFinishRecordsStructuredCompletionState(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	docker := &fakeDocker{runningProjects: map[string]bool{}}
	service := NewForTesting(runtime, gitops.New(repoRoot), docker)
	service.newRunID = func() string { return "20260318-000008-face" }

	meta, err := service.Create(CreateOptions{OwnedPaths: []string{"pkg"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := os.MkdirAll(filepath.Join(meta.WorktreePath, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir owned path: %v", err)
	}
	filePath := filepath.Join(meta.WorktreePath, "pkg", "worker.txt")
	if err := os.WriteFile(filePath, []byte("worker\n"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}

	result, err := service.Finish(FinishOptions{
		RunID:   meta.ID,
		Status:  RunCompletionStatusReadyWithAssumptions,
		Summary: "implemented owned slice",
		NeedsChanges: []ChangeRequest{
			{Path: "internal/orchestrator", Description: "wire the new completion status into scheduling", Blocking: true},
		},
		Assumptions: []string{"the orchestrator will honor owned_paths"},
		Followups:   []string{"add integration test coverage"},
	})
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}

	if !result.CompletionSaved {
		t.Fatal("expected structured completion state to be saved")
	}
	if result.CommitSHA == "" {
		t.Fatal("expected commit SHA to be recorded")
	}
	if len(result.TouchedPaths) != 1 || result.TouchedPaths[0] != "pkg/worker.txt" {
		t.Fatalf("unexpected touched paths: %+v", result.TouchedPaths)
	}

	status, err := service.GetStatus(meta.ID)
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if status.Completion == nil {
		t.Fatal("expected completion metadata")
	}
	if status.Completion.Status != RunCompletionStatusReadyWithAssumptions {
		t.Fatalf("unexpected completion status: %+v", status.Completion)
	}
	if status.Completion.CommitSHA != result.CommitSHA {
		t.Fatalf("completion commit SHA = %q, want %q", status.Completion.CommitSHA, result.CommitSHA)
	}
	if len(status.Completion.NeedsChanges) != 1 || status.Completion.NeedsChanges[0].Path != "internal/orchestrator" {
		t.Fatalf("unexpected needs changes: %+v", status.Completion.NeedsChanges)
	}
}

func TestCreateRejectsOverlappingOwnedPathsForActiveRuns(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	docker := &fakeDocker{runningProjects: map[string]bool{}}
	service := NewForTesting(runtime, gitops.New(repoRoot), docker)

	service.newRunID = func() string { return "20260318-000009-aaaa" }
	first, err := service.Create(CreateOptions{OwnedPaths: []string{"internal/mcp"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if first.ID == "" {
		t.Fatal("expected first run to have an ID")
	}

	service.newRunID = func() string { return "20260318-000010-bbbb" }
	if _, err := service.Create(CreateOptions{OwnedPaths: []string{"internal/mcp/server.go"}}); err == nil {
		t.Fatal("expected overlapping scope error")
	} else if !strings.Contains(err.Error(), "overlaps with active run") {
		t.Fatalf("unexpected overlap error: %v", err)
	}

	if _, err := service.Create(CreateOptions{OwnedPaths: []string{"internal/runs"}}); err != nil {
		t.Fatalf("expected disjoint scope to succeed, got %v", err)
	}
}

func TestCreateAllowsOverlapAfterRunIsMarkedReady(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	docker := &fakeDocker{runningProjects: map[string]bool{}}
	service := NewForTesting(runtime, gitops.New(repoRoot), docker)

	service.newRunID = func() string { return "20260318-000011-cccc" }
	meta, err := service.Create(CreateOptions{OwnedPaths: []string{"pkg"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := os.MkdirAll(filepath.Join(meta.WorktreePath, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir owned path: %v", err)
	}
	filePath := filepath.Join(meta.WorktreePath, "pkg", "done.txt")
	if err := os.WriteFile(filePath, []byte("done\n"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}

	if _, err := service.Finish(FinishOptions{
		RunID:  meta.ID,
		Status: RunCompletionStatusReady,
	}); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}

	service.newRunID = func() string { return "20260318-000012-dddd" }
	if _, err := service.Create(CreateOptions{OwnedPaths: []string{"pkg"}}); err != nil {
		t.Fatalf("expected ready run not to block new claim, got %v", err)
	}
}

func TestCreateAllowsSharedScopeOverlapButRejectsExclusiveOverlap(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	docker := &fakeDocker{runningProjects: map[string]bool{}}
	service := NewForTesting(runtime, gitops.New(repoRoot), docker)

	service.newRunID = func() string { return "20260318-000013-eeee" }
	if _, err := service.Create(CreateOptions{
		ClaimMode:  RunClaimModeShared,
		OwnedPaths: []string{"internal/mcp"},
	}); err != nil {
		t.Fatalf("Create(shared) error = %v", err)
	}

	service.newRunID = func() string { return "20260318-000014-ffff" }
	if _, err := service.Create(CreateOptions{
		ClaimMode:  RunClaimModeShared,
		OwnedPaths: []string{"internal/mcp/server.go"},
	}); err != nil {
		t.Fatalf("expected shared overlap to succeed, got %v", err)
	}

	service.newRunID = func() string { return "20260318-000015-1111" }
	if _, err := service.Create(CreateOptions{
		ClaimMode:  RunClaimModeExclusive,
		OwnedPaths: []string{"internal/mcp"},
	}); err == nil {
		t.Fatal("expected exclusive overlap error")
	} else if !strings.Contains(err.Error(), "overlaps with active run") {
		t.Fatalf("unexpected overlap error: %v", err)
	}
}

func TestCreateRejectsSharedWholeRepoClaim(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	service := NewForTesting(runtime, gitops.New(repoRoot), &fakeDocker{runningProjects: map[string]bool{}})

	if _, err := service.Create(CreateOptions{ClaimMode: RunClaimModeShared}); err == nil {
		t.Fatal("expected shared whole-repo claim to be rejected")
	} else if !strings.Contains(err.Error(), "shared claim mode requires owned_paths") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateRejectsCoordinationPathOverlap(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	docker := &fakeDocker{runningProjects: map[string]bool{}}
	service := NewForTesting(runtime, gitops.New(repoRoot), docker)

	service.newRunID = func() string { return "20260318-000016-2222" }
	if _, err := service.Create(CreateOptions{
		OwnedPaths:        []string{"pkg/a"},
		CoordinationPaths: []string{"go.mod"},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	service.newRunID = func() string { return "20260318-000017-3333" }
	if _, err := service.Create(CreateOptions{
		OwnedPaths:        []string{"pkg/b"},
		CoordinationPaths: []string{"go.mod"},
	}); err == nil {
		t.Fatal("expected coordination path overlap error")
	} else if !strings.Contains(err.Error(), "coordination scope") {
		t.Fatalf("unexpected overlap error: %v", err)
	}
}

func TestFinishAllowsClaimedCoordinationPaths(t *testing.T) {
	repoRoot := initRepo(t)
	runtime := newTestRuntime(t, repoRoot)
	docker := &fakeDocker{runningProjects: map[string]bool{}}
	service := NewForTesting(runtime, gitops.New(repoRoot), docker)
	service.newRunID = func() string { return "20260318-000018-4444" }

	meta, err := service.Create(CreateOptions{
		OwnedPaths:        []string{"pkg"},
		CoordinationPaths: []string{"go.mod"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := os.MkdirAll(filepath.Join(meta.WorktreePath, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir owned path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(meta.WorktreePath, "pkg", "worker.txt"), []byte("worker\n"), 0o644); err != nil {
		t.Fatalf("write owned file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(meta.WorktreePath, "go.mod"), []byte("module example.com/test\n"), 0o644); err != nil {
		t.Fatalf("write coordination file: %v", err)
	}

	result, err := service.Finish(FinishOptions{RunID: meta.ID, Status: RunCompletionStatusReady})
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if len(result.TouchedPaths) != 2 {
		t.Fatalf("unexpected touched paths: %+v", result.TouchedPaths)
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
			"ALCATRAZ_EGRESS_PROXY_RUNTIME=runc",
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
			"ALCATRAZ_EGRESS_PROXY_RUNTIME=runc",
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

func dockerCommandKey(service string, command []string) string {
	return service + "\x00" + strings.Join(command, "\x00")
}

func hasEnvKey(env []string, key string) bool {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
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

func currentBranch(t *testing.T, dir string) string {
	t.Helper()

	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --show-current failed: %v\n%s", err, output)
	}
	return strings.TrimSpace(string(output))
}
