package runs

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jamesdrando/alcatraz/internal/config"
	"github.com/jamesdrando/alcatraz/internal/dockerops"
	"github.com/jamesdrando/alcatraz/internal/gitops"
	rtpkg "github.com/jamesdrando/alcatraz/internal/runtime"
)

type RunMetadata struct {
	ID             string         `json:"id"`
	BranchName     string         `json:"branch_name"`
	BaseRef        string         `json:"base_ref"`
	WorktreePath   string         `json:"worktree_path"`
	ComposeProject string         `json:"compose_project"`
	AuthMode       rtpkg.AuthMode `json:"auth_mode"`
	ComposeFiles   []string       `json:"compose_files"`
	ConfigPath     string         `json:"config_path,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type RunStatus struct {
	RunMetadata
	Status         string `json:"status"`
	Running        bool   `json:"running"`
	WorktreeExists bool   `json:"worktree_exists"`
	BranchExists   bool   `json:"branch_exists"`
	Dirty          bool   `json:"dirty"`
}

type CreateOptions struct {
	BaseRef    string
	BranchName string
	AllowDirty bool
}

type CleanupResult struct {
	RunID           string `json:"run_id"`
	BranchName      string `json:"branch_name"`
	ComposeProject  string `json:"compose_project"`
	WorktreePath    string `json:"worktree_path"`
	WorktreeRemoved bool   `json:"worktree_removed"`
	BranchDeleted   bool   `json:"branch_deleted"`
	MetadataRemoved bool   `json:"metadata_removed"`
}

type CleanupSummary struct {
	Runs []CleanupResult `json:"runs"`
}

type FinishOptions struct {
	RunID         string
	CommitMessage string
	Merge         bool
	MergeInto     string
	Clean         bool
	DeleteBranch  bool
}

type FinishResult struct {
	RunID           string `json:"run_id"`
	BranchName      string `json:"branch_name"`
	CommitCreated   bool   `json:"commit_created"`
	Merged          bool   `json:"merged"`
	MergeTarget     string `json:"merge_target,omitempty"`
	WorktreeRemoved bool   `json:"worktree_removed"`
	BranchDeleted   bool   `json:"branch_deleted"`
	MetadataRemoved bool   `json:"metadata_removed"`
}

type gitClient interface {
	EnsureCleanCheckout() error
	BranchExists(branchName string) (bool, error)
	CreateWorktree(worktreePath, branchName, baseRef string) error
	RemoveWorktree(worktreePath string) error
	DeleteBranch(branchName string) error
	WorktreeDirty(worktreePath string) (bool, error)
	CurrentBranch(dir string) (string, error)
	SwitchBranch(dir, branchName string) error
	StageAll(dir string) error
	Commit(dir, message string) (bool, error)
	MergeIntoCurrent(dir, branchName string) error
	Diff(worktreePath, baseRef, branchName string, stat bool) (string, error)
	ListWorktrees() ([]gitops.WorktreeEntry, error)
}

type dockerClient interface {
	UpDetached(composeFiles, env []string, streams dockerops.Streams, services ...string) error
	Down(composeFiles, env []string, streams dockerops.Streams) error
	RunService(composeFiles, env []string, streams dockerops.Streams, service string, command []string) error
	ExecService(composeFiles, env []string, streams dockerops.Streams, service string, command []string) error
	ExecServiceInteractive(composeFiles, env []string, streams dockerops.Streams, service string, command []string) error
	ServiceNetworkIP(composeFiles, env []string, service, network string) (string, error)
	ProjectRunning(project string) (bool, error)
}

type Service struct {
	runtime  *rtpkg.Runtime
	git      gitClient
	docker   dockerClient
	now      func() time.Time
	newRunID func() string
}

func New(runtime *rtpkg.Runtime) *Service {
	return &Service{
		runtime:  runtime,
		git:      runtime.Git,
		docker:   runtime.Docker,
		now:      func() time.Time { return time.Now().UTC() },
		newRunID: defaultRunID,
	}
}

func NewForTesting(runtime *rtpkg.Runtime, git gitClient, docker dockerClient) *Service {
	svc := New(runtime)
	svc.git = git
	svc.docker = docker
	return svc
}

func (s *Service) EffectiveConfig() config.Config {
	return s.runtime.Config
}

func (s *Service) Create(opts CreateOptions) (RunMetadata, error) {
	if err := s.runtime.EnsureEnvFileIgnored(); err != nil {
		return RunMetadata{}, err
	}

	if !opts.AllowDirty && !s.runtime.Config.AllowDirty {
		if err := s.git.EnsureCleanCheckout(); err != nil {
			return RunMetadata{}, err
		}
	}

	if err := s.runtime.EnsureEnvFile(); err != nil {
		return RunMetadata{}, err
	}

	authMode, err := s.runtime.ResolveAuthMode()
	if err != nil {
		return RunMetadata{}, err
	}

	baseRef := opts.BaseRef
	if baseRef == "" {
		baseRef = s.runtime.Config.DefaultBaseRef
	}

	runID := s.newRunID()
	branchName := opts.BranchName
	if branchName == "" {
		branchName = s.runtime.Config.BranchPrefix + "/" + runID
	}

	worktreePath := filepath.Join(s.runtime.WorktreeDir(), runID)
	if err := s.git.CreateWorktree(worktreePath, branchName, baseRef); err != nil {
		return RunMetadata{}, err
	}

	meta := RunMetadata{
		ID:             runID,
		BranchName:     branchName,
		BaseRef:        baseRef,
		WorktreePath:   worktreePath,
		ComposeProject: composeProjectName(s.runtime.Config.ComposeProjectPrefix, runID),
		AuthMode:       authMode,
		ComposeFiles:   s.runtime.ComposeFiles(authMode),
		ConfigPath:     s.runtime.Config.ConfigPath,
		CreatedAt:      s.now(),
	}
	if err := s.writeRunMetadata(meta); err != nil {
		_ = s.git.RemoveWorktree(worktreePath)
		_ = s.git.DeleteBranch(branchName)
		return RunMetadata{}, err
	}
	return meta, nil
}

func (s *Service) StartPersistent(meta RunMetadata, extraAgentArgs []string) error {
	bootstrapEnv, err := s.runEnv(meta, "")
	if err != nil {
		return err
	}

	if err := s.docker.UpDetached(meta.ComposeFiles, bootstrapEnv, dockerops.Streams{}, "egress-proxy"); err != nil {
		return err
	}

	env, err := s.runEnvWithResolvedProxy(meta, bootstrapEnv)
	if err != nil {
		return err
	}

	if err := s.docker.UpDetached(meta.ComposeFiles, env, dockerops.Streams{}, "agent"); err != nil {
		return err
	}

	if len(extraAgentArgs) == 0 {
		return nil
	}
	command := append(append([]string{}, s.runtime.Config.AgentCommand...), extraAgentArgs...)
	return s.docker.ExecService(meta.ComposeFiles, env, dockerops.Streams{}, "agent", command)
}

func (s *Service) RunInteractive(meta RunMetadata, extraAgentArgs []string, streams dockerops.Streams) error {
	bootstrapEnv, err := s.runEnv(meta, "")
	if err != nil {
		return err
	}

	if err := s.docker.UpDetached(meta.ComposeFiles, bootstrapEnv, streams, "egress-proxy"); err != nil {
		return err
	}
	defer func() {
		_ = s.docker.Down(meta.ComposeFiles, bootstrapEnv, streams)
	}()

	env, err := s.runEnvWithResolvedProxy(meta, bootstrapEnv)
	if err != nil {
		return err
	}
	if err := s.docker.UpDetached(meta.ComposeFiles, env, streams, "agent"); err != nil {
		return err
	}

	command := append(append([]string{}, s.runtime.Config.AgentCommand...), extraAgentArgs...)
	return s.docker.ExecServiceInteractive(meta.ComposeFiles, env, streams, "agent", command)
}

func (s *Service) ListStatuses() ([]RunStatus, error) {
	items, err := s.loadRuns()
	if err != nil {
		return nil, err
	}

	statuses := make([]RunStatus, 0, len(items))
	for _, item := range items {
		status, err := s.EnrichStatus(item)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (s *Service) GetStatus(runID string) (RunStatus, error) {
	meta, err := s.loadRun(runID)
	if err != nil {
		return RunStatus{}, err
	}
	return s.EnrichStatus(meta)
}

func (s *Service) CleanRun(runID string, deleteBranch bool) (CleanupSummary, error) {
	meta, err := s.loadRun(runID)
	if err != nil {
		return CleanupSummary{}, err
	}
	return s.cleanRuns([]RunMetadata{meta}, deleteBranch, false)
}

func (s *Service) CleanAll(deleteBranch bool) (CleanupSummary, error) {
	items, err := s.loadRuns()
	if err != nil {
		return CleanupSummary{}, err
	}
	summary, err := s.cleanRuns(items, deleteBranch, false)
	if err != nil {
		return CleanupSummary{}, err
	}

	legacy, err := s.cleanLegacyWorktrees(deleteBranch)
	if err != nil {
		return CleanupSummary{}, err
	}
	summary.Runs = append(summary.Runs, legacy.Runs...)
	return summary, nil
}

func (s *Service) Finish(opts FinishOptions) (FinishResult, error) {
	meta, err := s.loadRun(opts.RunID)
	if err != nil {
		return FinishResult{}, err
	}

	status, err := s.EnrichStatus(meta)
	if err != nil {
		return FinishResult{}, err
	}

	env, err := s.cleanupEnv(meta)
	if err != nil {
		return FinishResult{}, err
	}
	if err := s.docker.Down(meta.ComposeFiles, env, dockerops.Streams{}); err != nil {
		return FinishResult{}, err
	}

	result := FinishResult{
		RunID:      meta.ID,
		BranchName: meta.BranchName,
	}

	message := strings.TrimSpace(opts.CommitMessage)
	if message == "" {
		message = fmt.Sprintf("alcatraz: finish %s", meta.ID)
	}

	if status.WorktreeExists {
		committed, err := s.git.Commit(meta.WorktreePath, message)
		if err != nil {
			return FinishResult{}, err
		}
		result.CommitCreated = committed
	}

	if opts.Merge {
		if err := s.git.EnsureCleanCheckout(); err != nil {
			return FinishResult{}, err
		}

		targetBranch := strings.TrimSpace(opts.MergeInto)
		if targetBranch == "" {
			targetBranch, err = s.git.CurrentBranch(s.runtime.RepoRoot)
			if err != nil {
				return FinishResult{}, err
			}
		} else {
			currentBranch, err := s.git.CurrentBranch(s.runtime.RepoRoot)
			if err != nil {
				return FinishResult{}, err
			}
			if currentBranch != targetBranch {
				if err := s.git.SwitchBranch(s.runtime.RepoRoot, targetBranch); err != nil {
					return FinishResult{}, err
				}
			}
		}

		if targetBranch == meta.BranchName {
			return FinishResult{}, fmt.Errorf("merge target matches run branch: %s", targetBranch)
		}
		if err := s.git.MergeIntoCurrent(s.runtime.RepoRoot, meta.BranchName); err != nil {
			return FinishResult{}, err
		}
		result.Merged = true
		result.MergeTarget = targetBranch
	}

	if opts.Clean || opts.DeleteBranch {
		summary, err := s.cleanRuns([]RunMetadata{meta}, opts.DeleteBranch, true)
		if err != nil {
			return FinishResult{}, err
		}
		if len(summary.Runs) == 1 {
			result.WorktreeRemoved = summary.Runs[0].WorktreeRemoved
			result.BranchDeleted = summary.Runs[0].BranchDeleted
			result.MetadataRemoved = summary.Runs[0].MetadataRemoved
		}
	}

	return result, nil
}

func (s *Service) Diff(runID string, stat bool) (string, error) {
	meta, err := s.loadRun(runID)
	if err != nil {
		return "", err
	}
	return s.git.Diff(meta.WorktreePath, meta.BaseRef, meta.BranchName, stat)
}

func (s *Service) EnrichStatus(meta RunMetadata) (RunStatus, error) {
	status := RunStatus{RunMetadata: meta}

	if _, err := os.Stat(meta.WorktreePath); err == nil {
		status.WorktreeExists = true
	} else if err != nil && !os.IsNotExist(err) {
		return RunStatus{}, err
	}

	branchExists, err := s.git.BranchExists(meta.BranchName)
	if err != nil {
		return RunStatus{}, err
	}
	status.BranchExists = branchExists

	if status.WorktreeExists {
		dirty, err := s.git.WorktreeDirty(meta.WorktreePath)
		if err != nil {
			return RunStatus{}, err
		}
		status.Dirty = dirty
	}

	running, err := s.docker.ProjectRunning(meta.ComposeProject)
	if err != nil {
		return RunStatus{}, err
	}
	status.Running = running
	status.Status = summarizeStatus(status)
	return status, nil
}

func (s *Service) loadRuns() ([]RunMetadata, error) {
	entries, err := os.ReadDir(s.runtime.MetadataDir())
	if err != nil {
		return nil, fmt.Errorf("read metadata dir: %w", err)
	}

	runs := make([]RunMetadata, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		meta, err := s.readRunMetadata(filepath.Join(s.runtime.MetadataDir(), entry.Name()))
		if err != nil {
			return nil, err
		}
		runs = append(runs, meta)
	}

	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	return runs, nil
}

func (s *Service) loadRun(runID string) (RunMetadata, error) {
	if runID == "" {
		items, err := s.loadRuns()
		if err != nil {
			return RunMetadata{}, err
		}
		if len(items) == 0 {
			return RunMetadata{}, errors.New("no runs found")
		}
		return items[0], nil
	}

	path := s.runtime.MetadataPath(runID)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return RunMetadata{}, fmt.Errorf("run not found: %s", runID)
		}
		return RunMetadata{}, err
	}
	return s.readRunMetadata(path)
}

func (s *Service) cleanRuns(items []RunMetadata, deleteBranch bool, skipDown bool) (CleanupSummary, error) {
	results := make([]CleanupResult, 0, len(items))
	for _, item := range items {
		if !skipDown {
			env, err := s.cleanupEnv(item)
			if err != nil {
				return CleanupSummary{}, err
			}
			if err := s.docker.Down(item.ComposeFiles, env, dockerops.Streams{}); err != nil {
				return CleanupSummary{}, err
			}
		}

		result := CleanupResult{
			RunID:          item.ID,
			BranchName:     item.BranchName,
			ComposeProject: item.ComposeProject,
			WorktreePath:   item.WorktreePath,
		}

		if _, err := os.Stat(item.WorktreePath); err == nil {
			if err := s.git.RemoveWorktree(item.WorktreePath); err != nil {
				return CleanupSummary{}, err
			}
			result.WorktreeRemoved = true
		} else if err != nil && !os.IsNotExist(err) {
			return CleanupSummary{}, err
		}

		if deleteBranch {
			branchExists, err := s.git.BranchExists(item.BranchName)
			if err != nil {
				return CleanupSummary{}, err
			}
			if branchExists {
				if err := s.git.DeleteBranch(item.BranchName); err != nil {
					return CleanupSummary{}, err
				}
				result.BranchDeleted = true
			}
		}

		if err := os.Remove(s.runtime.MetadataPath(item.ID)); err != nil && !os.IsNotExist(err) {
			return CleanupSummary{}, err
		} else if err == nil {
			result.MetadataRemoved = true
		}

		results = append(results, result)
	}

	return CleanupSummary{Runs: results}, nil
}

func (s *Service) cleanLegacyWorktrees(deleteBranch bool) (CleanupSummary, error) {
	entries, err := s.git.ListWorktrees()
	if err != nil {
		return CleanupSummary{}, err
	}

	managedPrefix := filepath.Clean(s.runtime.WorktreeDir()) + string(os.PathSeparator)
	results := make([]CleanupResult, 0)
	for _, entry := range entries {
		path := filepath.Clean(entry.Path)
		if path == filepath.Clean(s.runtime.RepoRoot) {
			continue
		}
		if !strings.HasPrefix(path, managedPrefix) {
			continue
		}

		runID := filepath.Base(path)
		if _, err := os.Stat(s.runtime.MetadataPath(runID)); err == nil {
			continue
		} else if err != nil && !os.IsNotExist(err) {
			return CleanupSummary{}, err
		}

		result := CleanupResult{
			RunID:        runID,
			BranchName:   entry.Branch,
			WorktreePath: path,
		}

		if _, err := os.Stat(path); err == nil {
			if err := s.git.RemoveWorktree(path); err != nil {
				return CleanupSummary{}, err
			}
			result.WorktreeRemoved = true
		} else if err != nil && !os.IsNotExist(err) {
			return CleanupSummary{}, err
		}

		if deleteBranch && strings.TrimSpace(entry.Branch) != "" {
			branchExists, err := s.git.BranchExists(entry.Branch)
			if err != nil {
				return CleanupSummary{}, err
			}
			if branchExists {
				if err := s.git.DeleteBranch(entry.Branch); err != nil {
					return CleanupSummary{}, err
				}
				result.BranchDeleted = true
			}
		}

		results = append(results, result)
	}

	return CleanupSummary{Runs: results}, nil
}

func (s *Service) runEnv(meta RunMetadata, proxyURL string) ([]string, error) {
	codexBin, err := s.runtime.ResolveCodexBin()
	if err != nil {
		return nil, err
	}

	return s.composeEnv(meta, codexBin, proxyURL)
}

func (s *Service) runEnvWithResolvedProxy(meta RunMetadata, env []string) ([]string, error) {
	ip, err := s.docker.ServiceNetworkIP(meta.ComposeFiles, env, "egress-proxy", agentNetworkName(meta.ComposeProject))
	if err != nil {
		return nil, err
	}
	return s.runEnv(meta, "http://"+ip+":3128")
}

func (s *Service) cleanupEnv(meta RunMetadata) ([]string, error) {
	if codexBin := strings.TrimSpace(s.runtime.Env["HOST_CODEX_BIN"]); codexBin != "" {
		return s.composeEnv(meta, codexBin, "")
	}
	if _, err := os.Stat("/bin/sh"); err == nil {
		return s.composeEnv(meta, "/bin/sh", "")
	}
	if executable, err := os.Executable(); err == nil {
		return s.composeEnv(meta, executable, "")
	}
	return nil, errors.New("could not resolve HOST_CODEX_BIN for cleanup")
}

func (s *Service) composeEnv(meta RunMetadata, codexBin string, proxyURL string) ([]string, error) {
	containerRuntime, err := s.runtime.ResolveContainerRuntime()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(proxyURL) == "" {
		proxyURL = "http://egress-proxy:3128"
	}

	extra := map[string]string{
		"ALCATRAZ_CONTAINER_RUNTIME": containerRuntime,
		"ALCATRAZ_EGRESS_PROXY":      proxyURL,
		"ALCATRAZ_WORKSPACE":         meta.WorktreePath,
		"COMPOSE_PROJECT_NAME":       meta.ComposeProject,
		"HOST_CODEX_BIN":             codexBin,
	}

	if value := joinCSV(s.runtime.Config.DependencyProfiles); value != "" {
		extra["ALCATRAZ_DEP_PROFILES"] = value
	}
	if value := joinCSV(s.runtime.Config.AptPackages); value != "" {
		extra["ALCATRAZ_APT_PACKAGES"] = value
	}
	if value := joinCSV(s.runtime.Config.NodePackages); value != "" {
		extra["ALCATRAZ_NODE_PACKAGES"] = value
	}
	if value := joinCSV(s.runtime.Config.PythonPackages); value != "" {
		extra["ALCATRAZ_PYTHON_PACKAGES"] = value
	}
	if value := joinCSV(s.runtime.Config.GoModules); value != "" {
		extra["ALCATRAZ_GO_MODULES"] = value
	}

	return s.runtime.CommandEnv(extra), nil
}

func joinCSV(values []string) string {
	seen := make(map[string]struct{}, len(values))
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		parts = append(parts, value)
	}
	return strings.Join(parts, ",")
}

func (s *Service) writeRunMetadata(meta RunMetadata) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run metadata: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(s.runtime.MetadataPath(meta.ID), data, 0o644)
}

func (s *Service) readRunMetadata(path string) (RunMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RunMetadata{}, fmt.Errorf("read run metadata: %w", err)
	}

	var meta RunMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return RunMetadata{}, fmt.Errorf("parse run metadata %s: %w", path, err)
	}
	return meta, nil
}

func summarizeStatus(status RunStatus) string {
	switch {
	case status.Running:
		return "running"
	case status.WorktreeExists && status.BranchExists:
		return "stopped"
	case status.WorktreeExists && !status.BranchExists:
		return "missing-branch"
	case !status.WorktreeExists && status.BranchExists:
		return "missing-worktree"
	default:
		return "removed"
	}
}

func composeProjectName(prefix, runID string) string {
	name := sanitizeComposePart(prefix) + "-" + sanitizeComposePart(runID)
	return strings.Trim(name, "-")
}

func agentNetworkName(composeProject string) string {
	return composeProject + "_agent-net"
}

func sanitizeComposePart(value string) string {
	value = strings.ToLower(value)
	replacer := strings.NewReplacer("/", "-", "_", "-", " ", "-")
	value = replacer.Replace(value)

	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-':
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}

func defaultRunID() string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	return time.Now().Format("20060102-150405") + fmt.Sprintf("-%04x", rng.Intn(1<<16))
}
