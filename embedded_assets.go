package assets

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const Version = "v1"

type bundledAsset struct {
	Path string
	Mode fs.FileMode
}

type InitTemplate struct {
	Path string
	Mode fs.FileMode
}

var bundledAssets = []bundledAsset{
	{Path: "compose.yaml", Mode: 0o644},
	{Path: "compose.codex.yaml", Mode: 0o644},
	{Path: "compose.chatgpt.yaml", Mode: 0o644},
	{Path: "docker/agent/Dockerfile", Mode: 0o644},
	{Path: "docker/agent/docker-entrypoint.sh", Mode: 0o755},
	{Path: "docker/egress-proxy/Dockerfile", Mode: 0o644},
	{Path: "docker/egress-proxy/docker-entrypoint.sh", Mode: 0o755},
	{Path: "docker/egress-proxy/squid.conf", Mode: 0o644},
}

var composeAssets = map[string]struct{}{
	"compose.yaml":         {},
	"compose.codex.yaml":   {},
	"compose.chatgpt.yaml": {},
}

var initTemplates = []InitTemplate{
	{Path: "templates/init/skills/alcatraz-orchestrator/SKILL.md", Mode: 0o644},
	{Path: "templates/init/skills/alcatraz-worker/SKILL.md", Mode: 0o644},
}

//go:embed compose.yaml compose.codex.yaml compose.chatgpt.yaml docker/agent/Dockerfile docker/agent/docker-entrypoint.sh docker/egress-proxy/Dockerfile docker/egress-proxy/docker-entrypoint.sh docker/egress-proxy/squid.conf templates/init/skills/alcatraz-orchestrator/SKILL.md templates/init/skills/alcatraz-worker/SKILL.md
var bundledFS embed.FS

func Materialize(stateDir string) (string, error) {
	root := filepath.Join(stateDir, "assets", Version)
	for _, asset := range bundledAssets {
		data, err := bundledFS.ReadFile(asset.Path)
		if err != nil {
			return "", fmt.Errorf("read bundled asset %s: %w", asset.Path, err)
		}
		if err := writeBundledFile(filepath.Join(root, asset.Path), data, asset.Mode); err != nil {
			return "", fmt.Errorf("write bundled asset %s: %w", asset.Path, err)
		}
	}
	return root, nil
}

func ResolveComposeFiles(root string, names []string) ([]string, error) {
	files := make([]string, 0, len(names))
	for _, name := range names {
		path, err := ResolveComposeFile(root, name)
		if err != nil {
			return nil, err
		}
		files = append(files, path)
	}
	return files, nil
}

func ResolveComposeFile(root, name string) (string, error) {
	name = strings.TrimSpace(name)
	if _, ok := composeAssets[name]; !ok {
		return "", fmt.Errorf("unsupported bundled compose asset: %s", name)
	}
	return filepath.Join(root, name), nil
}

func InitTemplateFiles() []InitTemplate {
	out := make([]InitTemplate, len(initTemplates))
	copy(out, initTemplates)
	return out
}

func ReadInitTemplate(path string) ([]byte, error) {
	return bundledFS.ReadFile(path)
}

func writeBundledFile(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, data) {
		info, statErr := os.Stat(path)
		if statErr == nil && info.Mode().Perm() != mode {
			return os.Chmod(path, mode)
		}
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return os.WriteFile(path, data, mode)
}
