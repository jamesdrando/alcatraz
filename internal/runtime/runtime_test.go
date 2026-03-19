package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveContainerRuntimePrefersRunscWhenAvailable(t *testing.T) {
	installFakeDocker(t, "io.containerd.runc.v2\nrunsc\nDEFAULT=runc\n")

	rt := &Runtime{Env: map[string]string{}}
	got, err := rt.ResolveContainerRuntime()
	if err != nil {
		t.Fatalf("ResolveContainerRuntime() error = %v", err)
	}
	if got != "runsc" {
		t.Fatalf("ResolveContainerRuntime() = %q, want %q", got, "runsc")
	}
}

func TestResolveContainerRuntimeFallsBackToDockerDefault(t *testing.T) {
	installFakeDocker(t, "io.containerd.runc.v2\nrunc\nDEFAULT=runc\n")

	rt := &Runtime{Env: map[string]string{}}
	got, err := rt.ResolveContainerRuntime()
	if err != nil {
		t.Fatalf("ResolveContainerRuntime() error = %v", err)
	}
	if got != "runc" {
		t.Fatalf("ResolveContainerRuntime() = %q, want %q", got, "runc")
	}
}

func TestResolveContainerRuntimeHonorsExplicitOverride(t *testing.T) {
	rt := &Runtime{Env: map[string]string{
		"ALCATRAZ_CONTAINER_RUNTIME": "io.containerd.runc.v2",
	}}

	got, err := rt.ResolveContainerRuntime()
	if err != nil {
		t.Fatalf("ResolveContainerRuntime() error = %v", err)
	}
	if got != "io.containerd.runc.v2" {
		t.Fatalf("ResolveContainerRuntime() = %q, want %q", got, "io.containerd.runc.v2")
	}
}

func installFakeDocker(t *testing.T, output string) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "docker")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"info\" ] && [ \"$2\" = \"--format\" ]; then\n" +
		"cat <<'EOF'\n" +
		output +
		"EOF\n" +
		"exit 0\n" +
		"fi\n" +
		"echo \"unexpected docker invocation: $*\" >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
