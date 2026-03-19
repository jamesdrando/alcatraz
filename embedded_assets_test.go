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
