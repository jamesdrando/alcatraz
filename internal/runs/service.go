package runs

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jamesdrando/alcatraz/internal/config"
	"github.com/jamesdrando/alcatraz/internal/dockerops"
	"github.com/jamesdrando/alcatraz/internal/gitops"
	rtpkg "github.com/jamesdrando/alcatraz/internal/runtime"
)

type RunMetadata struct {
	ID                string         `json:"id"`
	BranchName        string         `json:"branch_name"`
	BaseRef           string         `json:"base_ref"`
	BaseCommit        string         `json:"base_commit"`
	MergeTarget       string         `json:"merge_target"`
	ClaimMode         RunClaimMode   `json:"claim_mode"`
	OwnedPaths        []string       `json:"owned_paths,omitempty"`
	CoordinationPaths []string       `json:"coordination_paths,omitempty"`
	WorktreePath      string         `json:"worktree_path"`
	ComposeProject    string         `json:"compose_project"`
	AuthMode          rtpkg.AuthMode `json:"auth_mode"`
	ComposeFiles      []string       `json:"compose_files"`
	ConfigPath        string         `json:"config_path,omitempty"`
	Completion        *RunCompletion `json:"completion,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
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
	BaseRef           string
	BranchName        string
	MergeTarget       string
	ClaimMode         RunClaimMode
	OwnedPaths        []string
	CoordinationPaths []string
	AllowDirty        bool
}

type RunClaimMode string

const (
	RunClaimModeExclusive RunClaimMode = "exclusive"
	RunClaimModeShared    RunClaimMode = "shared"
)

type RunCompletionStatus string

const (
	RunCompletionStatusReady                RunCompletionStatus = "ready"
	RunCompletionStatusBlocked              RunCompletionStatus = "blocked"
	RunCompletionStatusReadyWithAssumptions RunCompletionStatus = "ready_with_assumptions"
)

type ChangeRequest struct {
	Path        string `json:"path,omitempty"`
	Description string `json:"description"`
	Blocking    bool   `json:"blocking,omitempty"`
}

type RunCompletion struct {
	Status             RunCompletionStatus `json:"status"`
	Summary            string              `json:"summary,omitempty"`
	NeedsChanges       []ChangeRequest     `json:"needs_changes,omitempty"`
	Assumptions        []string            `json:"assumptions,omitempty"`
	SuggestedFollowups []string            `json:"suggested_followups,omitempty"`
	TouchedPaths       []string            `json:"touched_paths,omitempty"`
	CommitSHA          string              `json:"commit_sha,omitempty"`
	SubmittedAt        time.Time           `json:"submitted_at,omitempty"`
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
	Status        RunCompletionStatus
	Summary       string
	NeedsChanges  []ChangeRequest
	Assumptions   []string
	Followups     []string
	Merge         bool
	MergeInto     string
	Clean         bool
	DeleteBranch  bool
}

type FinishResult struct {
	RunID           string   `json:"run_id"`
	BranchName      string   `json:"branch_name"`
	CommitCreated   bool     `json:"commit_created"`
	CommitSHA       string   `json:"commit_sha,omitempty"`
	TouchedPaths    []string `json:"touched_paths,omitempty"`
	CompletionSaved bool     `json:"completion_saved"`
	Merged          bool     `json:"merged"`
	MergeTarget     string   `json:"merge_target,omitempty"`
	WorktreeRemoved bool     `json:"worktree_removed"`
	BranchDeleted   bool     `json:"branch_deleted"`
	MetadataRemoved bool     `json:"metadata_removed"`
}

type gitClient interface {
	EnsureCleanCheckout() error
	BranchExists(branchName string) (bool, error)
	ResolveCommit(dir, ref string) (string, error)
	MergeBase(left, right string) (string, error)
	CreateWorktree(worktreePath, branchName, baseRef string) error
	RemoveWorktree(worktreePath string) error
	DeleteBranch(branchName string) error
	WorktreeDirty(worktreePath string) (bool, error)
	CurrentBranch(dir string) (string, error)
	SwitchBranch(dir, branchName string) error
	StageAll(dir string) error
	Commit(dir, message string) (bool, error)
	MergeIntoCurrent(dir, branchName string) error
	ChangedPaths(baseCommit, ref string) ([]string, error)
	Diff(worktreePath, baseCommit, branchName string, stat bool) (string, error)
	ListWorktrees() ([]gitops.WorktreeEntry, error)
}

type dockerClient interface {
	UpDetached(composeFiles, env []string, streams dockerops.Streams, services ...string) error
	Down(composeFiles, env []string, streams dockerops.Streams) error
	RunService(composeFiles, env []string, streams dockerops.Streams, service string, command []string) error
	ExecService(composeFiles, env []string, streams dockerops.Streams, service string, command []string) error
	ExecServiceInteractive(composeFiles, env []string, streams dockerops.Streams, service string, command []string) error
	ExecServiceOutput(composeFiles, env []string, service string, command []string) (string, error)
	ServiceLogs(composeFiles, env []string, service string, tailLines int) (string, error)
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
	baseCommit, err := s.git.ResolveCommit(s.runtime.RepoRoot, baseRef)
	if err != nil {
		return RunMetadata{}, err
	}

	runID := s.newRunID()
	branchName := opts.BranchName
	if branchName == "" {
		branchName = s.runtime.Config.BranchPrefix + "/" + runID
	}
	mergeTarget, err := s.resolveMergeTarget(baseRef, opts.MergeTarget)
	if err != nil {
		return RunMetadata{}, err
	}
	claimMode, err := normalizeClaimMode(opts.ClaimMode)
	if err != nil {
		return RunMetadata{}, err
	}
	ownedPaths := normalizeOwnedPaths(opts.OwnedPaths)
	coordinationPaths := normalizeOwnedPaths(opts.CoordinationPaths)
	if err := validateClaimMode(claimMode, ownedPaths); err != nil {
		return RunMetadata{}, err
	}

	return withRunsLock(filepath.Join(s.runtime.StateDir, "runs.lock"), func() (RunMetadata, error) {
		if err := s.ensureClaimsAvailable(ownedPaths, claimMode, coordinationPaths); err != nil {
			return RunMetadata{}, err
		}

		worktreePath := filepath.Join(s.runtime.WorktreeDir(), runID)
		if err := s.git.CreateWorktree(worktreePath, branchName, baseRef); err != nil {
			return RunMetadata{}, err
		}

		meta := RunMetadata{
			ID:                runID,
			BranchName:        branchName,
			BaseRef:           baseRef,
			BaseCommit:        baseCommit,
			MergeTarget:       mergeTarget,
			ClaimMode:         claimMode,
			OwnedPaths:        ownedPaths,
			CoordinationPaths: coordinationPaths,
			WorktreePath:      worktreePath,
			ComposeProject:    composeProjectName(s.runtime.Config.ComposeProjectPrefix, runID),
			AuthMode:          authMode,
			ComposeFiles:      s.runtime.ComposeFiles(authMode),
			ConfigPath:        s.runtime.Config.ConfigPath,
			CreatedAt:         s.now(),
		}
		if err := s.writeRunMetadata(meta); err != nil {
			_ = s.git.RemoveWorktree(worktreePath)
			_ = s.git.DeleteBranch(branchName)
			return RunMetadata{}, err
		}
		return meta, nil
	})
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
	if err := s.runNetworkPreflight(meta, env); err != nil {
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
	cleanup := true

	if err := s.docker.UpDetached(meta.ComposeFiles, bootstrapEnv, streams, "egress-proxy"); err != nil {
		return err
	}
	defer func() {
		if cleanup {
			_ = s.docker.Down(meta.ComposeFiles, bootstrapEnv, streams)
		}
	}()

	env, err := s.runEnvWithResolvedProxy(meta, bootstrapEnv)
	if err != nil {
		cleanup = false
		return err
	}
	if err := s.docker.UpDetached(meta.ComposeFiles, env, streams, "agent"); err != nil {
		cleanup = false
		return err
	}
	if err := s.runNetworkPreflight(meta, env); err != nil {
		cleanup = false
		return fmt.Errorf("%w\n\ncompose project preserved for inspection: %s", err, meta.ComposeProject)
	}

	command := append(append([]string{}, s.runtime.Config.AgentCommand...), extraAgentArgs...)
	if err := s.docker.ExecServiceInteractive(meta.ComposeFiles, env, streams, "agent", command); err != nil {
		cleanup = false
		return err
	}
	return nil
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
	return withRunsLock(filepath.Join(s.runtime.StateDir, "runs.lock"), func() (CleanupSummary, error) {
		meta, err := s.loadRun(runID)
		if err != nil {
			return CleanupSummary{}, err
		}
		return s.cleanRuns([]RunMetadata{meta}, deleteBranch, false)
	})
}

func (s *Service) CleanAll(deleteBranch bool) (CleanupSummary, error) {
	return withRunsLock(filepath.Join(s.runtime.StateDir, "runs.lock"), func() (CleanupSummary, error) {
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
	})
}

func (s *Service) Finish(opts FinishOptions) (FinishResult, error) {
	return withRunsLock(filepath.Join(s.runtime.StateDir, "runs.lock"), func() (FinishResult, error) {
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

		commitSHA, err := s.git.ResolveCommit(s.runtime.RepoRoot, meta.BranchName)
		if err != nil {
			return FinishResult{}, err
		}
		result.CommitSHA = commitSHA

		touchedPaths, err := s.git.ChangedPaths(meta.BaseCommit, meta.BranchName)
		if err != nil {
			return FinishResult{}, err
		}
		result.TouchedPaths = touchedPaths
		if err := ensureClaimedPaths(meta.OwnedPaths, meta.CoordinationPaths, touchedPaths); err != nil {
			return FinishResult{}, err
		}

		if completion, ok, err := buildCompletion(opts, touchedPaths, commitSHA, s.now()); err != nil {
			return FinishResult{}, err
		} else if ok {
			meta.Completion = completion
			if err := s.writeRunMetadata(meta); err != nil {
				return FinishResult{}, err
			}
			result.CompletionSaved = true
		}

		if opts.Merge {
			if err := s.git.EnsureCleanCheckout(); err != nil {
				return FinishResult{}, err
			}

			targetBranch := strings.TrimSpace(opts.MergeInto)
			if targetBranch == "" {
				targetBranch = strings.TrimSpace(meta.MergeTarget)
			}
			if targetBranch == "" {
				return FinishResult{}, errors.New("merge target is empty; set it when creating the run or pass --into explicitly")
			}

			currentBranch, err := s.git.CurrentBranch(s.runtime.RepoRoot)
			if err != nil {
				return FinishResult{}, err
			}
			if currentBranch != targetBranch {
				if err := s.git.SwitchBranch(s.runtime.RepoRoot, targetBranch); err != nil {
					return FinishResult{}, err
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
	})
}

func (s *Service) Diff(runID string, stat bool) (string, error) {
	meta, err := s.loadRun(runID)
	if err != nil {
		return "", err
	}
	return s.git.Diff(meta.WorktreePath, meta.BaseCommit, meta.BranchName, stat)
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
	egressProxyRuntime, err := s.runtime.ResolveEgressProxyRuntime()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(proxyURL) == "" {
		proxyURL = "http://egress-proxy:3128"
	}

	extra := map[string]string{
		"ALCATRAZ_CONTAINER_RUNTIME":    containerRuntime,
		"ALCATRAZ_EGRESS_DNS_1":         egressDNSServers()[0],
		"ALCATRAZ_EGRESS_DNS_2":         egressDNSServers()[1],
		"ALCATRAZ_EGRESS_PROXY":         proxyURL,
		"ALCATRAZ_EGRESS_PROXY_RUNTIME": egressProxyRuntime,
		"ALCATRAZ_WORKSPACE":            meta.WorktreePath,
		"COMPOSE_PROJECT_NAME":          meta.ComposeProject,
		"HOST_CODEX_BIN":                codexBin,
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

func (s *Service) runNetworkPreflight(meta RunMetadata, env []string) error {
	host := preflightHost(meta.AuthMode)
	if host == "" {
		return nil
	}

	var lastOutput string
	var lastErr error
	command := preflightCurlCommand(host)
	for attempt := 1; attempt <= 3; attempt++ {
		output, err := s.docker.ExecServiceOutput(meta.ComposeFiles, env, "agent", command)
		if err == nil {
			return nil
		}
		lastOutput = strings.TrimSpace(output)
		lastErr = err
		if attempt < 3 {
			time.Sleep(1 * time.Second)
		}
	}

	return s.preflightError(meta, env, host, lastOutput, lastErr)
}

func (s *Service) preflightError(meta RunMetadata, env []string, host, curlOutput string, cause error) error {
	sections := []string{
		fmt.Sprintf("network preflight failed for %s", host),
	}

	if proxyEnv, err := s.docker.ExecServiceOutput(meta.ComposeFiles, env, "agent", agentProxyEnvCommand()); err == nil {
		proxyEnv = strings.TrimSpace(proxyEnv)
		if proxyEnv != "" {
			sections = append(sections, "agent proxy env:\n"+proxyEnv)
		}
	}

	if curlOutput != "" {
		sections = append(sections, "agent curl output:\n"+curlOutput)
	}

	if resolver, err := s.docker.ExecServiceOutput(meta.ComposeFiles, env, "egress-proxy", proxyResolvConfCommand()); err == nil {
		resolver = strings.TrimSpace(resolver)
		if resolver != "" {
			sections = append(sections, "egress-proxy resolv.conf:\n"+resolver)
		}
	}

	if lookup, err := s.docker.ExecServiceOutput(meta.ComposeFiles, env, "egress-proxy", proxyLookupCommand(host)); err == nil {
		lookup = strings.TrimSpace(lookup)
		if lookup == "" {
			lookup = "<no records>"
		}
		sections = append(sections, fmt.Sprintf("egress-proxy DNS lookup for %s:\n%s", host, lookup))
	}

	if logs, err := s.docker.ServiceLogs(meta.ComposeFiles, env, "egress-proxy", 30); err == nil {
		logs = strings.TrimSpace(logs)
		if logs != "" {
			sections = append(sections, "egress-proxy logs (tail 30):\n"+logs)
		}
	}

	message := strings.Join(sections, "\n\n")
	if cause != nil {
		return fmt.Errorf("%s\n\nroot cause: %w", message, cause)
	}
	return errors.New(message)
}

func preflightHost(mode rtpkg.AuthMode) string {
	switch mode {
	case rtpkg.AuthModeChatGPT:
		return "chatgpt.com"
	case rtpkg.AuthModeAPIKey:
		return "api.openai.com"
	default:
		return ""
	}
}

func preflightCurlCommand(host string) []string {
	return []string{
		"sh", "-lc",
		fmt.Sprintf("curl -Ivs --connect-timeout 3 --max-time 8 https://%s 2>&1", host),
	}
}

func agentProxyEnvCommand() []string {
	return []string{
		"sh", "-lc",
		`printf 'HTTPS_PROXY=%s\nHTTP_PROXY=%s\n' "$HTTPS_PROXY" "$HTTP_PROXY"`,
	}
}

func proxyResolvConfCommand() []string {
	return []string{"sh", "-lc", "cat /etc/resolv.conf"}
}

func proxyLookupCommand(host string) []string {
	return []string{
		"sh", "-lc",
		fmt.Sprintf("getent hosts %s || true", host),
	}
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

func (s *Service) resolveMergeTarget(baseRef, explicitTarget string) (string, error) {
	if target := strings.TrimSpace(explicitTarget); target != "" {
		return target, nil
	}

	if candidate := strings.TrimSpace(baseRef); candidate != "" && candidate != "HEAD" {
		exists, err := s.git.BranchExists(candidate)
		if err != nil {
			return "", err
		}
		if exists {
			return candidate, nil
		}
	}

	if currentBranch, err := s.git.CurrentBranch(s.runtime.RepoRoot); err == nil && strings.TrimSpace(currentBranch) != "" {
		return strings.TrimSpace(currentBranch), nil
	}

	return "", errors.New("could not determine merge target from the current checkout; pass one explicitly")
}

func buildCompletion(opts FinishOptions, touchedPaths []string, commitSHA string, submittedAt time.Time) (*RunCompletion, bool, error) {
	status := opts.Status
	if status == "" && !hasCompletionInput(opts) {
		return nil, false, nil
	}
	if status == "" {
		status = RunCompletionStatusReady
	}
	switch status {
	case RunCompletionStatusReady, RunCompletionStatusBlocked, RunCompletionStatusReadyWithAssumptions:
	default:
		return nil, false, fmt.Errorf("invalid completion status: %s", status)
	}

	needsChanges := sanitizeChangeRequests(opts.NeedsChanges)
	if status == RunCompletionStatusReady && len(needsChanges) > 0 {
		return nil, false, errors.New("ready runs cannot include needs_changes; use blocked or ready_with_assumptions instead")
	}

	completion := &RunCompletion{
		Status:             status,
		Summary:            strings.TrimSpace(opts.Summary),
		NeedsChanges:       needsChanges,
		Assumptions:        sanitizeStringList(opts.Assumptions),
		SuggestedFollowups: sanitizeStringList(opts.Followups),
		TouchedPaths:       append([]string{}, touchedPaths...),
		CommitSHA:          strings.TrimSpace(commitSHA),
		SubmittedAt:        submittedAt,
	}
	return completion, true, nil
}

func hasCompletionInput(opts FinishOptions) bool {
	return opts.Status != "" ||
		strings.TrimSpace(opts.Summary) != "" ||
		len(opts.NeedsChanges) > 0 ||
		len(opts.Assumptions) > 0 ||
		len(opts.Followups) > 0
}

func sanitizeChangeRequests(items []ChangeRequest) []ChangeRequest {
	out := make([]ChangeRequest, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		path := normalizeOwnedPath(item.Path)
		description := strings.TrimSpace(item.Description)
		if path == "" && description == "" {
			continue
		}
		key := path + "\x00" + description + "\x00" + fmt.Sprintf("%t", item.Blocking)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ChangeRequest{
			Path:        path,
			Description: description,
			Blocking:    item.Blocking,
		})
	}
	return out
}

func sanitizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
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
	return out
}

func normalizeOwnedPaths(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalizeOwnedPath(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeOwnedPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == "/" {
		return ""
	}
	value = filepath.ToSlash(filepath.Clean(value))
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimPrefix(value, "/")
	if value == "." {
		return ""
	}
	return value
}

func normalizeClaimMode(mode RunClaimMode) (RunClaimMode, error) {
	mode = RunClaimMode(strings.TrimSpace(string(mode)))
	if mode == "" {
		return RunClaimModeExclusive, nil
	}
	switch mode {
	case RunClaimModeExclusive, RunClaimModeShared:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid claim mode: %s", mode)
	}
}

func validateClaimMode(mode RunClaimMode, ownedPaths []string) error {
	if mode == RunClaimModeShared && len(ownedPaths) == 0 {
		return errors.New("shared claim mode requires owned_paths; refusing to create a shared whole-repo claim")
	}
	return nil
}

func ensureClaimedPaths(ownedPaths, coordinationPaths, touchedPaths []string) error {
	if len(ownedPaths) == 0 {
		return nil
	}

	var outside []string
	for _, path := range touchedPaths {
		if !pathWithinClaim(path, ownedPaths, coordinationPaths) {
			outside = append(outside, path)
		}
	}
	if len(outside) == 0 {
		return nil
	}

	sort.Strings(outside)
	return fmt.Errorf("run touched paths outside its claimed scope: %s", strings.Join(outside, ", "))
}

func (s *Service) ensureClaimsAvailable(requestedOwned []string, requestedMode RunClaimMode, requestedCoordination []string) error {
	items, err := s.loadRuns()
	if err != nil {
		return err
	}

	for _, item := range items {
		status, err := s.EnrichStatus(item)
		if err != nil {
			return err
		}
		if !runBlocksScopeClaims(status) {
			continue
		}

		existingMode, err := normalizeClaimMode(item.ClaimMode)
		if err != nil {
			return err
		}

		if ownedScopesConflict(requestedOwned, requestedMode, item.OwnedPaths, existingMode) {
			return fmt.Errorf(
				"requested %s scope %s overlaps with active run %s on branch %s claiming %s scope %s",
				requestedMode,
				formatOwnedPaths(requestedOwned),
				item.ID,
				item.BranchName,
				existingMode,
				formatOwnedPaths(item.OwnedPaths),
			)
		}

		if claimedPathScopesOverlap(requestedCoordination, item.CoordinationPaths) {
			return fmt.Errorf(
				"requested coordination scope %s overlaps with active run %s on branch %s claiming %s",
				formatOwnedPaths(requestedCoordination),
				item.ID,
				item.BranchName,
				"coordination "+formatOwnedPaths(item.CoordinationPaths),
			)
		}
		if claimedCoordinationVsOwnedOverlap(requestedCoordination, item.OwnedPaths) {
			return fmt.Errorf(
				"requested coordination scope %s overlaps with active run %s on branch %s claiming owned scope %s",
				formatOwnedPaths(requestedCoordination),
				item.ID,
				item.BranchName,
				formatOwnedPaths(item.OwnedPaths),
			)
		}
		if claimedCoordinationVsOwnedOverlap(item.CoordinationPaths, requestedOwned) {
			return fmt.Errorf(
				"requested owned scope %s overlaps with active run %s on branch %s claiming coordination scope %s",
				formatOwnedPaths(requestedOwned),
				item.ID,
				item.BranchName,
				formatOwnedPaths(item.CoordinationPaths),
			)
		}
	}
	return nil
}

func pathWithinOwnedPaths(path string, ownedPaths []string) bool {
	path = normalizeOwnedPath(path)
	if path == "" {
		return true
	}
	for _, scope := range ownedPaths {
		if scope == "" {
			return true
		}
		if path == scope || strings.HasPrefix(path, scope+"/") {
			return true
		}
	}
	return false
}

func pathWithinClaim(path string, ownedPaths, coordinationPaths []string) bool {
	return pathWithinOwnedPaths(path, ownedPaths) || pathWithinReservedPaths(path, coordinationPaths)
}

func pathWithinReservedPaths(path string, reservedPaths []string) bool {
	path = normalizeOwnedPath(path)
	if path == "" {
		return true
	}
	for _, reserved := range reservedPaths {
		if reserved == "" {
			return true
		}
		if path == reserved || strings.HasPrefix(path, reserved+"/") {
			return true
		}
	}
	return false
}

func ownedPathScopesOverlap(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return true
	}
	for _, leftPath := range left {
		for _, rightPath := range right {
			if pathScopesOverlap(leftPath, rightPath) {
				return true
			}
		}
	}
	return false
}

func claimedPathScopesOverlap(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for _, leftPath := range left {
		for _, rightPath := range right {
			if pathScopesOverlap(leftPath, rightPath) {
				return true
			}
		}
	}
	return false
}

func pathScopesOverlap(left, right string) bool {
	left = normalizeOwnedPath(left)
	right = normalizeOwnedPath(right)
	if left == "" || right == "" {
		return true
	}
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func formatOwnedPaths(paths []string) string {
	if len(paths) == 0 {
		return "<entire-repo>"
	}
	return strings.Join(paths, ", ")
}

func ownedScopesConflict(requestedOwned []string, requestedMode RunClaimMode, existingOwned []string, existingMode RunClaimMode) bool {
	if !ownedPathScopesOverlap(requestedOwned, existingOwned) {
		return false
	}
	return requestedMode == RunClaimModeExclusive || existingMode == RunClaimModeExclusive
}

func claimedCoordinationVsOwnedOverlap(coordination, owned []string) bool {
	if len(coordination) == 0 {
		return false
	}
	if len(owned) == 0 {
		return true
	}
	for _, claimed := range coordination {
		if pathWithinOwnedPaths(claimed, owned) {
			return true
		}
	}
	return false
}

func runBlocksScopeClaims(status RunStatus) bool {
	if status.Status == "removed" {
		return false
	}
	if status.Running {
		return true
	}
	if status.Completion == nil {
		return true
	}
	return status.Completion.Status != RunCompletionStatusReady
}

func withRunsLock[T any](lockPath string, fn func() (T, error)) (T, error) {
	var zero T

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return zero, fmt.Errorf("open runs lock: %w", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return zero, fmt.Errorf("lock runs state: %w", err)
	}
	defer func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	}()

	return fn()
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
	if err := s.hydrateLegacyMetadata(&meta); err != nil {
		return RunMetadata{}, err
	}
	return meta, nil
}

func (s *Service) hydrateLegacyMetadata(meta *RunMetadata) error {
	claimMode, err := normalizeClaimMode(meta.ClaimMode)
	if err != nil {
		return err
	}
	meta.ClaimMode = claimMode
	meta.OwnedPaths = normalizeOwnedPaths(meta.OwnedPaths)
	meta.CoordinationPaths = normalizeOwnedPaths(meta.CoordinationPaths)
	if strings.TrimSpace(meta.MergeTarget) == "" {
		if candidate := strings.TrimSpace(meta.BaseRef); candidate != "" && candidate != "HEAD" {
			exists, err := s.git.BranchExists(candidate)
			if err != nil {
				return err
			}
			if exists {
				meta.MergeTarget = candidate
			}
		}
		if strings.TrimSpace(meta.MergeTarget) == "" {
			if currentBranch, err := s.git.CurrentBranch(s.runtime.RepoRoot); err == nil {
				meta.MergeTarget = strings.TrimSpace(currentBranch)
			}
		}
	}
	if strings.TrimSpace(meta.BaseCommit) != "" {
		return nil
	}
	if candidate := strings.TrimSpace(meta.BaseRef); candidate != "" && candidate != "HEAD" {
		if commit, err := s.git.ResolveCommit(s.runtime.RepoRoot, candidate); err == nil && strings.TrimSpace(commit) != "" {
			meta.BaseCommit = commit
			return nil
		}
	}
	if strings.TrimSpace(meta.MergeTarget) == "" || strings.TrimSpace(meta.BranchName) == "" {
		return nil
	}
	if commit, err := s.git.MergeBase(meta.MergeTarget, meta.BranchName); err == nil && strings.TrimSpace(commit) != "" {
		meta.BaseCommit = commit
	}
	return nil
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

func egressDNSServers() []string {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return defaultEgressDNSServers(nil)
	}
	return defaultEgressDNSServers(parseNameservers(string(data)))
}

func defaultEgressDNSServers(nameservers []string) []string {
	switch len(nameservers) {
	case 0:
		return []string{"1.1.1.1", "8.8.8.8"}
	case 1:
		return []string{nameservers[0], nameservers[0]}
	default:
		return []string{nameservers[0], nameservers[1]}
	}
}

func parseNameservers(resolvConf string) []string {
	lines := strings.Split(resolvConf, "\n")
	servers := make([]string, 0, len(lines))
	seen := map[string]struct{}{}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		server := fields[1]
		if !usableNameserver(server) {
			continue
		}
		if _, ok := seen[server]; ok {
			continue
		}
		seen[server] = struct{}{}
		servers = append(servers, server)
	}
	return servers
}

func usableNameserver(server string) bool {
	addr, err := netip.ParseAddr(server)
	if err != nil {
		return false
	}
	if addr.IsLoopback() || addr.IsUnspecified() {
		return false
	}
	if server == "127.0.0.10" || server == "127.0.0.11" {
		return false
	}
	return true
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
