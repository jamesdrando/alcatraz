package gitops

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Client struct {
	RepoRoot string
}

type WorktreeEntry struct {
	Path   string
	Branch string
}

func New(repoRoot string) *Client {
	return &Client{RepoRoot: repoRoot}
}

func DiscoverRepoRoot(startDir string) (string, error) {
	out, err := execInDir(startDir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", errors.New("must be run inside a git repository")
	}
	return strings.TrimSpace(out), nil
}

func DiscoverGitDir(repoRoot string) (string, error) {
	out, err := execInDir(repoRoot, "git", "rev-parse", "--git-dir")
	if err != nil {
		return "", fmt.Errorf("resolve git dir: %w", err)
	}
	gitDir := strings.TrimSpace(out)
	if filepath.IsAbs(gitDir) {
		return gitDir, nil
	}
	return filepath.Join(repoRoot, gitDir), nil
}

func (c *Client) EnsureCleanCheckout() error {
	out, err := execInDir(c.RepoRoot, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return errors.New("working tree is not clean; commit or stash changes first, or rerun with --allow-dirty")
	}
	return nil
}

func (c *Client) BranchExists(branchName string) (bool, error) {
	return commandSucceededInDir(c.RepoRoot, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+branchName)
}

func (c *Client) ResolveCommit(dir, ref string) (string, error) {
	out, err := execInDir(dir, "git", "rev-parse", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (c *Client) MergeBase(left, right string) (string, error) {
	out, err := execInDir(c.RepoRoot, "git", "merge-base", left, right)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (c *Client) CreateWorktree(worktreePath, branchName, baseRef string) error {
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(worktreePath); err == nil {
		return fmt.Errorf("worktree path already exists: %s", worktreePath)
	} else if !os.IsNotExist(err) {
		return err
	}

	branchExists, err := c.BranchExists(branchName)
	if err != nil {
		return err
	}
	if branchExists {
		return fmt.Errorf("branch already exists: %s", branchName)
	}

	if _, err := execInDir(c.RepoRoot, "git", "worktree", "add", "-b", branchName, worktreePath, baseRef); err != nil {
		return fmt.Errorf("create worktree: %w", err)
	}
	return nil
}

func (c *Client) RemoveWorktree(worktreePath string) error {
	if _, err := execInDir(c.RepoRoot, "git", "worktree", "remove", "--force", worktreePath); err != nil {
		return err
	}
	return nil
}

func (c *Client) DeleteBranch(branchName string) error {
	if _, err := execInDir(c.RepoRoot, "git", "branch", "-D", branchName); err != nil {
		return err
	}
	return nil
}

func (c *Client) WorktreeDirty(worktreePath string) (bool, error) {
	out, err := execInDir(worktreePath, "git", "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func (c *Client) CurrentBranch(dir string) (string, error) {
	out, err := execInDir(dir, "git", "branch", "--show-current")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (c *Client) SwitchBranch(dir, branchName string) error {
	if _, err := execInDir(dir, "git", "switch", branchName); err != nil {
		return err
	}
	return nil
}

func (c *Client) StageAll(dir string) error {
	if _, err := execInDir(dir, "git", "add", "-A"); err != nil {
		return err
	}
	return nil
}

func (c *Client) Commit(dir, message string) (bool, error) {
	dirty, err := c.WorktreeDirty(dir)
	if err != nil {
		return false, err
	}
	if !dirty {
		return false, nil
	}
	if err := c.StageAll(dir); err != nil {
		return false, err
	}
	if _, err := execInDir(dir, "git", "commit", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Client) MergeIntoCurrent(dir, branchName string) error {
	if _, err := execInDir(dir, "git", "merge", "--no-ff", "--no-edit", branchName); err != nil {
		return err
	}
	return nil
}

func (c *Client) ChangedPaths(baseCommit, ref string) ([]string, error) {
	out, err := execInDir(c.RepoRoot, "git", "diff", "--name-only", "--no-renames", baseCommit+".."+ref)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(out, "\n")
	paths := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		paths = append(paths, line)
	}
	return paths, nil
}

func (c *Client) Diff(worktreePath, baseCommit, branchName string, stat bool) (string, error) {
	dirty, err := c.WorktreeDirty(worktreePath)
	if err != nil {
		return "", err
	}
	if dirty {
		args := []string{"git", "diff"}
		if stat {
			args = append(args, "--stat")
		}
		return execInDir(worktreePath, args[0], args[1:]...)
	}

	args := []string{"git", "diff"}
	if stat {
		args = append(args, "--stat")
	}
	args = append(args, baseCommit+".."+branchName)
	return execInDir(c.RepoRoot, args[0], args[1:]...)
}

func (c *Client) ListWorktrees() ([]WorktreeEntry, error) {
	out, err := execInDir(c.RepoRoot, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var entries []WorktreeEntry
	var current *WorktreeEntry
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			if current != nil {
				entries = append(entries, *current)
			}
			current = &WorktreeEntry{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "branch ") && current != nil:
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "" && current != nil:
			entries = append(entries, *current)
			current = nil
		}
	}
	if current != nil {
		entries = append(entries, *current)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func commandSucceededInDir(dir, name string, args ...string) (bool, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	if stderr.Len() > 0 {
		return false, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return false, err
}

func execInDir(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return "", err
	}
	return stdout.String(), nil
}
