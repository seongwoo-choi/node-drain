package cmd

import (
	"os"
	"strings"
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

func TestApplyRootFlagEnvUsesGlobalDefaultWhenEnvIsEmpty(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	prometheusAddress = "http://default-prometheus"
	t.Setenv("PROMETHEUS_ADDRESS", "")

	command := &cobra.Command{}
	command.Flags().String("prometheus-address", "http://default-prometheus", "")

	applyRootFlagEnv(command)

	if got := os.Getenv("PROMETHEUS_ADDRESS"); got != "http://default-prometheus" {
		t.Fatalf("expected empty env to be replaced by default, got %q", got)
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

func TestRootFlagDefaultsDoNotUsePlaceholderTargets(t *testing.T) {
	tests := []struct {
		flagName string
		want     string
	}{
		{flagName: "prometheus-address", want: ""},
		{flagName: "prometheus-org-id", want: ""},
		{flagName: "cluster-name", want: ""},
		{flagName: "nodepool-name", want: ""},
	}

	for _, tt := range tests {
		flag := rootCmd.PersistentFlags().Lookup(tt.flagName)
		if flag == nil {
			t.Fatalf("missing flag %s", tt.flagName)
		}
		if flag.DefValue != tt.want {
			t.Fatalf("flag %s default = %q, want %q", tt.flagName, flag.DefValue, tt.want)
		}
	}
}

func TestValidateRequiredEnvValuesRejectsMissing(t *testing.T) {
	t.Setenv("NODEPOOL_NAME", "")

	err := validateRequiredEnvValues(requiredEnvValue{flagName: "nodepool-name", envKey: "NODEPOOL_NAME"})
	if err == nil {
		t.Fatal("expected missing env validation error")
	}
}

func TestValidateRequiredEnvValuesAcceptsEnv(t *testing.T) {
	t.Setenv("NODEPOOL_NAME", "test-nodepool")

	err := validateRequiredEnvValues(requiredEnvValue{flagName: "nodepool-name", envKey: "NODEPOOL_NAME"})
	if err != nil {
		t.Fatalf("expected required env validation to pass: %v", err)
	}
}

func TestBoolFlagSpaceSeparatedFalseIsNotAFalseValue(t *testing.T) {
	var force bool
	command := &cobra.Command{
		Use:  "test",
		Args: cobra.ArbitraryArgs,
	}
	command.Flags().BoolVar(&force, "force", false, "")
	command.SetArgs([]string{"--force", "false"})

	if err := command.Execute(); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !force {
		t.Fatal("expected pflag to treat --force false as --force=true plus a positional arg")
	}
}

func TestDrainWorkflowPassesBooleanInputsWithEquals(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/drain.yml")
	if err != nil {
		t.Fatalf("read workflow failed: %v", err)
	}
	text := string(workflow)
	for _, flagName := range []string{
		"drain-progressive",
		"force",
		"force-problem-pods",
		"pdb-token",
	} {
		badPattern := "--" + flagName + ` "${{ inputs.`
		if strings.Contains(text, badPattern) {
			t.Fatalf("workflow passes bool flag with a separate value: %s", badPattern)
		}
		goodPattern := "--" + flagName + `="${{ inputs.`
		if !strings.Contains(text, goodPattern) {
			t.Fatalf("workflow missing bool flag assignment pattern: %s", goodPattern)
		}
	}
}
