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
	if got := bytes.Count(data, want); got != 2 {
		t.Fatalf("expected runtime declaration on both services, got %d", got)
	}
}
