package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetKubeClientSetTrimsLocalModeAndPath(t *testing.T) {
	kubeConfigPath := writeTestKubeConfig(t)

	clientSet, err := GetKubeClientSet(" local ", " "+kubeConfigPath+" ")
	if err != nil {
		t.Fatalf("GetKubeClientSet() failed: %v", err)
	}
	if clientSet == nil {
		t.Fatal("expected kubernetes clientset")
	}
}

func TestResolveKubeConfigPathTrimsEnvPath(t *testing.T) {
	kubeConfigPath := writeTestKubeConfig(t)
	t.Setenv("KUBECONFIG", " "+kubeConfigPath+" ")

	got := resolveKubeConfigPath("")
	if got != kubeConfigPath {
		t.Fatalf("resolveKubeConfigPath() = %q, want=%q", got, kubeConfigPath)
	}
}

func writeTestKubeConfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config")
	content := []byte(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://127.0.0.1
users:
- name: test
  user:
    token: test-token
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write kubeconfig failed: %v", err)
	}
	return path
}
