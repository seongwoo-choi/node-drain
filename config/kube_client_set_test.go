package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestGetKubeClientSetNormalizesModeCasing(t *testing.T) {
	kubeConfigPath := writeTestKubeConfig(t)

	clientSet, err := GetKubeClientSet(" LOCAL ", kubeConfigPath)
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

func TestGetClientSetRejectsNilConfig(t *testing.T) {
	clientSet, err := getClientSet(nil)
	if err == nil {
		t.Fatal("expected nil kubernetes config error")
	}
	if clientSet != nil {
		t.Fatalf("expected nil clientset, got %v", clientSet)
	}
	if !strings.Contains(err.Error(), "kubernetes config is required") {
		t.Fatalf("unexpected error: %v", err)
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
