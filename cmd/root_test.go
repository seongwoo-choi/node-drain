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

func TestApplyRootFlagEnvUsesExplicitPrometheusTenantFlag(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	prometheusTenantID = "tenant-from-flag"
	t.Setenv("PROMETHEUS_TENANT_ID", "tenant-from-env")
	t.Setenv("PROMETHEUS_SCOPE_ORG_ID", "legacy-from-env")

	command := &cobra.Command{}
	command.Flags().String("prometheus-tenant-id", "", "")
	command.Flags().String("prometheus-org-id", "", "")
	if err := command.Flags().Set("prometheus-tenant-id", "tenant-from-flag"); err != nil {
		t.Fatalf("set prometheus-tenant-id failed: %v", err)
	}

	applyRootFlagEnv(command)

	if got := os.Getenv("PROMETHEUS_TENANT_ID"); got != "tenant-from-flag" {
		t.Fatalf("expected explicit tenant flag to override env, got %q", got)
	}
	if got := os.Getenv("PROMETHEUS_SCOPE_ORG_ID"); got != "legacy-from-env" {
		t.Fatalf("legacy env should be preserved when tenant flag is non-empty, got %q", got)
	}
}

func TestApplyRootFlagEnvSupportsLegacyPrometheusOrgFlag(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	prometheusOrgID = "legacy-from-flag"
	t.Setenv("PROMETHEUS_TENANT_ID", "")
	t.Setenv("PROMETHEUS_SCOPE_ORG_ID", "")

	command := &cobra.Command{}
	command.Flags().String("prometheus-tenant-id", "", "")
	command.Flags().String("prometheus-org-id", "", "")
	if err := command.Flags().Set("prometheus-org-id", "legacy-from-flag"); err != nil {
		t.Fatalf("set prometheus-org-id failed: %v", err)
	}

	applyRootFlagEnv(command)

	if got := os.Getenv("PROMETHEUS_SCOPE_ORG_ID"); got != "legacy-from-flag" {
		t.Fatalf("expected legacy org flag to populate legacy env, got %q", got)
	}
}

func TestRootFlagDefaultsDoNotUsePlaceholderTargets(t *testing.T) {
	tests := []struct {
		flagName string
		want     string
	}{
		{flagName: "prometheus-address", want: ""},
		{flagName: "prometheus-tenant-id", want: ""},
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

func TestDrainWorkflowCanConfigureKubeconfigFromSecret(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/drain.yml")
	if err != nil {
		t.Fatalf("read workflow failed: %v", err)
	}
	text := string(workflow)
	for _, required := range []string{
		"KUBECONFIG_B64: ${{ secrets.KUBECONFIG_B64 }}",
		`base64 -d > "${HOME}/.kube/config"`,
		`chmod 600 "${HOME}/.kube/config"`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("drain workflow missing kubeconfig setup pattern: %s", required)
		}
	}
}

func TestDrainWorkflowPassesPodTimeoutControls(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/drain.yml")
	if err != nil {
		t.Fatalf("read workflow failed: %v", err)
	}
	text := string(workflow)
	for _, required := range []string{
		"pod_eviction_timeout:",
		"pod_node_termination_timeout:",
		"pod_node_termination_check_tick:",
		"pod_post_eviction_node_delay:",
		`--pod-eviction-timeout "${{ inputs.pod_eviction_timeout }}"`,
		`--pod-node-termination-timeout "${{ inputs.pod_node_termination_timeout }}"`,
		`--pod-node-termination-check-tick "${{ inputs.pod_node_termination_check_tick }}"`,
		`--pod-post-eviction-node-delay "${{ inputs.pod_post_eviction_node_delay }}"`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("drain workflow missing pod timeout control: %s", required)
		}
	}
}

func TestDrainWorkflowUsesKubernetesLockByDefault(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/drain.yml")
	if err != nil {
		t.Fatalf("read workflow failed: %v", err)
	}
	text := string(workflow)
	for _, required := range []string{
		"drain_lock_mode:",
		`default: "kubernetes"`,
		"drain_lock_namespace:",
		"drain_lock_lease_duration:",
		`--drain-lock-mode "${{ inputs.drain_lock_mode }}"`,
		`--drain-lock-namespace "${{ inputs.drain_lock_namespace }}"`,
		`--drain-lock-lease-duration "${{ inputs.drain_lock_lease_duration }}"`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("drain workflow missing kubernetes lock control: %s", required)
		}
	}
}

func TestDrainWorkflowUsesPrometheusTenantID(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/drain.yml")
	if err != nil {
		t.Fatalf("read workflow failed: %v", err)
	}
	text := string(workflow)
	for _, required := range []string{
		"prometheus_tenant_id:",
		"prometheus_tenant_id=\"${{ inputs.prometheus_tenant_id }}\"",
		"prometheus_tenant_id=\"${{ inputs.prometheus_org_id }}\"",
		`--prometheus-tenant-id "${prometheus_tenant_id}"`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("drain workflow missing prometheus tenant id pattern: %s", required)
		}
	}
	if strings.Contains(text, `--prometheus-org-id "${{ inputs.prometheus_org_id }}"`) {
		t.Fatal("drain workflow should use --prometheus-tenant-id")
	}
}

func TestLegacyWorkflowAvoidsPlaceholderDefaults(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/eks-node-drain-based-on-karpenter-allocate-rate.yaml")
	if err != nil {
		t.Fatalf("read workflow failed: %v", err)
	}
	text := string(workflow)
	for _, forbidden := range []string{
		"worker-nodepool-name",
		"프로메테우스.com",
		"123456789012",
		"hooks.slack.com/services/T00000000",
		"contents: write",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("legacy workflow still contains unsafe placeholder or permission: %s", forbidden)
		}
	}
}

func TestLegacyWorkflowPassesDrainSafetyFlags(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/eks-node-drain-based-on-karpenter-allocate-rate.yaml")
	if err != nil {
		t.Fatalf("read workflow failed: %v", err)
	}
	text := string(workflow)
	for _, required := range []string{
		"cmd=(go run main.go)",
		`cmd+=(--drain-max-absolute "${{ inputs.DRAIN_MAX_ABSOLUTE }}")`,
		`cmd+=(--drain-max-fraction "${{ inputs.DRAIN_MAX_FRACTION }}")`,
		`cmd+=(--drain-safety-max-allocate-rate "${{ inputs.DRAIN_SAFETY_MAX_ALLOCATE_RATE }}")`,
		`cmd+=(--drain-lock-mode "${{ inputs.DRAIN_LOCK_MODE }}")`,
		`cmd+=(--drain-lock-namespace "${{ inputs.DRAIN_LOCK_NAMESPACE }}")`,
		`cmd+=(--drain-lock-lease-duration "${{ inputs.DRAIN_LOCK_LEASE_DURATION }}")`,
		`cmd+=(--force="${{ inputs.FORCE }}")`,
		`cmd+=(--pdb-token="${{ inputs.PDB_TOKEN }}")`,
		`cmd+=(--pod-eviction-timeout "${{ inputs.POD_EVICTION_TIMEOUT }}")`,
		`cmd+=(--pod-node-termination-timeout "${{ inputs.POD_NODE_TERMINATION_TIMEOUT }}")`,
		`cmd+=(--pod-node-termination-check-tick "${{ inputs.POD_NODE_TERMINATION_CHECK_TICK }}")`,
		`cmd+=(--pod-post-eviction-node-delay "${{ inputs.POD_POST_EVICTION_NODE_DELAY }}")`,
		`"${cmd[@]}"`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("legacy workflow missing required safety pattern: %s", required)
		}
	}
}

func TestLegacyWorkflowUsesKubernetesLockByDefault(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/eks-node-drain-based-on-karpenter-allocate-rate.yaml")
	if err != nil {
		t.Fatalf("read workflow failed: %v", err)
	}
	text := string(workflow)
	for _, required := range []string{
		"DRAIN_LOCK_MODE:",
		"default: 'kubernetes'",
		"DRAIN_LOCK_NAMESPACE:",
		"DRAIN_LOCK_LEASE_DURATION:",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("legacy workflow missing kubernetes lock default: %s", required)
		}
	}
}

func TestLegacyWorkflowUsesPrometheusTenantID(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/eks-node-drain-based-on-karpenter-allocate-rate.yaml")
	if err != nil {
		t.Fatalf("read workflow failed: %v", err)
	}
	text := string(workflow)
	for _, required := range []string{
		"PROMETHEUS_TENANT_ID:",
		"prometheus_tenant_id=\"${{ inputs.PROMETHEUS_TENANT_ID }}\"",
		"prometheus_tenant_id=\"${{ inputs.PROMETHEUS_ORG_ID }}\"",
		`cmd+=(--prometheus-tenant-id "${prometheus_tenant_id}")`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("legacy workflow missing prometheus tenant id pattern: %s", required)
		}
	}
	if strings.Contains(text, `cmd+=(--prometheus-org-id "${{ inputs.PROMETHEUS_ORG_ID }}")`) {
		t.Fatal("legacy workflow should use --prometheus-tenant-id")
	}
}
