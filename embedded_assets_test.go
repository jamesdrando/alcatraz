package assets

import (
	"bytes"
	"testing"
)

func TestComposeYamlDefaultsToRunscRuntime(t *testing.T) {
	data, err := bundledFS.ReadFile("compose.yaml")
	if err != nil {
		t.Fatalf("read compose.yaml: %v", err)
	}

	want := []byte("runtime: ${ALCATRAZ_CONTAINER_RUNTIME:-runsc}")
	if got := bytes.Count(data, want); got != 1 {
		t.Fatalf("expected runsc runtime declaration for agent only, got %d", got)
	}
}

func TestComposeYamlDefaultsProxyToSeparateRuntime(t *testing.T) {
	data, err := bundledFS.ReadFile("compose.yaml")
	if err != nil {
		t.Fatalf("read compose.yaml: %v", err)
	}

	want := []byte("runtime: ${ALCATRAZ_EGRESS_PROXY_RUNTIME:-runc}")
	if got := bytes.Count(data, want); got != 1 {
		t.Fatalf("expected separate proxy runtime declaration, got %d", got)
	}
}

func TestComposeYamlUsesInjectableProxyURL(t *testing.T) {
	data, err := bundledFS.ReadFile("compose.yaml")
	if err != nil {
		t.Fatalf("read compose.yaml: %v", err)
	}

	want := []byte("${ALCATRAZ_EGRESS_PROXY:-http://egress-proxy:3128}")
	if got := bytes.Count(data, want); got != 3 {
		t.Fatalf("expected injectable proxy env on HTTP/HTTPS/ALL proxy, got %d", got)
	}
}

func TestComposeYamlInjectsExplicitProxyDNS(t *testing.T) {
	data, err := bundledFS.ReadFile("compose.yaml")
	if err != nil {
		t.Fatalf("read compose.yaml: %v", err)
	}

	want := []byte("${ALCATRAZ_EGRESS_DNS_")
	if got := bytes.Count(data, want); got != 2 {
		t.Fatalf("expected two injectable DNS entries for egress proxy, got %d", got)
	}
}

func TestComposeYamlUsesRootChatGPTDomainOnce(t *testing.T) {
	data, err := bundledFS.ReadFile("compose.yaml")
	if err != nil {
		t.Fatalf("read compose.yaml: %v", err)
	}

	if bytes.Contains(data, []byte(".chatgpt.com")) {
		t.Fatal("expected bundled compose config not to include redundant .chatgpt.com allowlist entry")
	}
}

func TestAgentEntrypointSeedsWorkspaceTrust(t *testing.T) {
	data, err := bundledFS.ReadFile("docker/agent/docker-entrypoint.sh")
	if err != nil {
		t.Fatalf("read docker-entrypoint.sh: %v", err)
	}

	want := []byte(`[projects."/workspace"]`)
	if !bytes.Contains(data, want) {
		t.Fatalf("expected agent entrypoint to seed workspace trust")
	}
}

func TestInitTemplatesAreEmbedded(t *testing.T) {
	paths := []string{
		"templates/init/skills/alcatraz-orchestrator/SKILL.md",
		"templates/init/skills/alcatraz-worker/SKILL.md",
	}

	for _, path := range paths {
		data, err := bundledFS.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if len(data) == 0 {
			t.Fatalf("expected embedded data for %s", path)
		}
	}
}
