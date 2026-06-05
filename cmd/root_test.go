package cmd

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
)

func TestApplyRootFlagEnvPreservesExistingEnvWhenFlagOmitted(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	prometheusAddress = "http://default-prometheus"
	t.Setenv("PROMETHEUS_ADDRESS", "http://env-prometheus")

	command := &cobra.Command{}
	command.Flags().String("prometheus-address", "http://default-prometheus", "")

	applyRootFlagEnv(command)

	if got := os.Getenv("PROMETHEUS_ADDRESS"); got != "http://env-prometheus" {
		t.Fatalf("expected existing env to be preserved, got %q", got)
	}
}

func TestApplyRootFlagEnvUsesExplicitFlag(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	prometheusAddress = "http://flag-prometheus"
	t.Setenv("PROMETHEUS_ADDRESS", "http://env-prometheus")

	command := &cobra.Command{}
	command.Flags().String("prometheus-address", "http://default-prometheus", "")
	if err := command.Flags().Set("prometheus-address", "http://flag-prometheus"); err != nil {
		t.Fatalf("set flag failed: %v", err)
	}

	applyRootFlagEnv(command)

	if got := os.Getenv("PROMETHEUS_ADDRESS"); got != "http://flag-prometheus" {
		t.Fatalf("expected explicit flag to override env, got %q", got)
	}
}
