package dockerops

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

type Client struct {
	RepoRoot string
}

type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func New(repoRoot string) *Client {
	return &Client{RepoRoot: repoRoot}
}

func (c *Client) UpDetached(composeFiles, env []string, streams Streams, services ...string) error {
	args := c.composeArgs(composeFiles, "up", "-d", "--build")
	args = append(args, services...)
	return c.runDocker(args, env, streams)
}

func (c *Client) Down(composeFiles, env []string, streams Streams) error {
	args := c.composeArgs(composeFiles, "down", "--remove-orphans")
	return c.runDocker(args, env, streams)
}

func (c *Client) RunService(composeFiles, env []string, streams Streams, service string, command []string) error {
	args := c.composeArgs(composeFiles, "run", "--rm", "--no-deps", "--build", service)
	args = append(args, command...)
	return c.runDocker(args, env, streams)
}

func (c *Client) ExecService(composeFiles, env []string, streams Streams, service string, command []string) error {
	args := c.composeArgs(composeFiles, "exec", "-T", service)
	args = append(args, command...)
	return c.runDocker(args, env, streams)
}

func (c *Client) ExecServiceInteractive(composeFiles, env []string, streams Streams, service string, command []string) error {
	args := c.composeArgs(composeFiles, "exec", service)
	args = append(args, command...)
	return c.runDocker(args, env, streams)
}

func (c *Client) ServiceNetworkIP(composeFiles, env []string, service, network string) (string, error) {
	args := c.composeArgs(composeFiles, "ps", "-q", service)
	containerID, err := c.runDockerOutput(args, env)
	if err != nil {
		return "", err
	}
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return "", fmt.Errorf("no running container found for compose service: %s", service)
	}

	cmd := exec.Command("docker", "inspect", "-f", "{{with index .NetworkSettings.Networks \""+network+"\"}}{{.IPAddress}}{{end}}", containerID)
	cmd.Dir = c.RepoRoot
	cmd.Env = env

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

	ip := strings.TrimSpace(stdout.String())
	if ip == "" {
		return "", fmt.Errorf("compose service %s has no IP on network %s", service, network)
	}
	return ip, nil
}

func (c *Client) ProjectRunning(project string) (bool, error) {
	cmd := exec.Command("docker", "ps", "--filter", "label=com.docker.compose.project="+project, "--format", "{{.ID}}")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return false, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return false, err
	}
	return strings.TrimSpace(stdout.String()) != "", nil
}

func (c *Client) composeArgs(composeFiles []string, rest ...string) []string {
	args := []string{"compose"}
	for _, file := range composeFiles {
		path := file
		if !filepath.IsAbs(path) {
			path = filepath.Join(c.RepoRoot, file)
		}
		args = append(args, "-f", path)
	}
	args = append(args, rest...)
	return args
}

func (c *Client) runDocker(args, env []string, streams Streams) error {
	cmd := exec.Command("docker", args...)
	cmd.Dir = c.RepoRoot
	cmd.Env = env
	if streams.Stdin != nil {
		cmd.Stdin = streams.Stdin
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if streams.Stdout == nil && streams.Stderr == nil {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	} else {
		if streams.Stdout != nil {
			cmd.Stdout = streams.Stdout
		}
		if streams.Stderr != nil {
			cmd.Stderr = streams.Stderr
		}
	}

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return err
	}
	return nil
}

func (c *Client) runDockerOutput(args, env []string) (string, error) {
	cmd := exec.Command("docker", args...)
	cmd.Dir = c.RepoRoot
	cmd.Env = env

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
