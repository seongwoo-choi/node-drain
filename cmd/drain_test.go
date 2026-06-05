package cmd

import (
	"app/types"
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDrainCommandReturnsKubeClientError(t *testing.T) {
	if drainCmd.RunE == nil {
		t.Fatal("drain command RunE is nil")
	}

	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	prometheusAddress = "http://localhost:8080/prometheus"
	prometheusOrgID = "organization-dev"
	slackWebhookURL = ""
	kubeConfig = "invalid-mode"
	kubeConfigPath = ""
	clusterName = "test-cluster"
	nodepoolName = "test-nodepool"

	drainPolicy = "formula"
	drainRounding = "floor"
	drainMin = 0
	drainMaxAbsolute = 0
	drainMaxFraction = 0
	drainStepRules = ""
	drainSafetyMaxAllocateRate = 0
	drainSafetyQueries = ""
	drainSafetyFailClosed = true
	drainProgressive = true
	drainDryRun = false
	drainLockMode = "local"
	drainLockNamespace = "kube-system"
	drainLockLeaseDuration = "10m"
	drainNodeSelectionStrategy = "oldest"
	drainSkipUnschedulable = false
	drainOutputFormat = "text"

	podEvictionMode = "evict"
	podForce = false
	podForceProblemPods = true
	podDeleteAfterEviction = false
	podPDBToken = true
	podPDBTokenMaxInFlight = 1
	podMaxConcurrent = 30
	podMaxRetries = 3
	podRetryBackoff = "10s"
	podDeletionTimeout = "2m"
	podCheckInterval = "20s"
	podEvictionTimeout = "10m"
	podNodeTerminationTimeout = "10m"
	podNodeTerminationCheckTick = "15s"
	podPostEvictionNodeDelay = "50s"

	err := drainCmd.RunE(drainCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "쿠버네티스 클라이언트 생성 실패") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDrainCommandRequiresTargetSettings(t *testing.T) {
	if drainCmd.RunE == nil {
		t.Fatal("drain command RunE is nil")
	}

	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	prometheusAddress = ""
	clusterName = ""
	nodepoolName = ""
	t.Setenv("PROMETHEUS_ADDRESS", "")
	t.Setenv("CLUSTER_NAME", "")
	t.Setenv("NODEPOOL_NAME", "")

	err := drainCmd.RunE(&cobra.Command{}, nil)
	if err == nil {
		t.Fatal("expected required setting error, got nil")
	}
	if !strings.Contains(err.Error(), "prometheus-address") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDrainCommandRejectsUnexpectedArgs(t *testing.T) {
	if drainCmd.Args == nil {
		t.Fatal("drain command Args is nil")
	}

	err := drainCmd.Args(drainCmd, []string{"false"})
	if err == nil {
		t.Fatal("expected unexpected arg error")
	}
}

func TestDrainCommandRejectsInvalidDrainEnvBeforeKubeClient(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)
	configureDrainCommandForValidationTest(t)
	t.Setenv("DRAIN_MAX_FRACTION", "1.5")

	err := drainCmd.RunE(&cobra.Command{}, nil)
	if err == nil {
		t.Fatal("expected invalid drain env error")
	}
	if !strings.Contains(err.Error(), "DRAIN_MAX_FRACTION") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDrainCommandRejectsPrometheusAPIEndpointBeforeKubeClient(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)
	configureDrainCommandForValidationTest(t)
	t.Setenv("PROMETHEUS_ADDRESS", "http://localhost:8080/prometheus/api/v1/query")

	err := drainCmd.RunE(&cobra.Command{}, nil)
	if err == nil {
		t.Fatal("expected invalid prometheus address error")
	}
	if !strings.Contains(err.Error(), "PROMETHEUS_ADDRESS") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), "쿠버네티스 클라이언트") {
		t.Fatalf("expected prometheus validation before kube client, got: %v", err)
	}
}

func TestDrainCommandRejectsInvalidPodEnvBeforeKubeClient(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)
	configureDrainCommandForValidationTest(t)
	t.Setenv("POD_FORCE", "maybe")

	err := drainCmd.RunE(&cobra.Command{}, nil)
	if err == nil {
		t.Fatal("expected invalid pod env error")
	}
	if !strings.Contains(err.Error(), "POD_FORCE") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDrainCommandConfigIgnoresLeaseDurationOutsideKubernetesLock(t *testing.T) {
	for _, mode := range []string{"local", "none"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			restore := snapshotCommandGlobals()
			defer restore()
			restoreCommandEnv(t)
			configureDrainCommandForValidationTest(t)

			drainLockMode = mode
			drainLockLeaseDuration = "not-a-duration"

			if err := validateDrainCommandConfig(); err != nil {
				t.Fatalf("validateDrainCommandConfig() error = %v", err)
			}
		})
	}
}

func TestValidateDrainCommandConfigRejectsSubsecondKubernetesLeaseDuration(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)
	configureDrainCommandForValidationTest(t)

	drainLockMode = "kubernetes"
	drainLockLeaseDuration = "500ms"

	err := validateDrainCommandConfig()
	if err == nil {
		t.Fatal("expected subsecond lease duration error")
	}
	if !strings.Contains(err.Error(), "drain-lock-lease-duration") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAcquireDrainRunLockBlocksDuplicate(t *testing.T) {
	lockFile, err := acquireLocalDrainRunLock("test-cluster", "test-nodepool")
	if err != nil {
		t.Fatalf("acquireDrainRunLock failed: %v", err)
	}
	defer releaseDrainRunLock(context.Background(), lockFile)

	duplicateLockFile, err := acquireLocalDrainRunLock("test-cluster", "test-nodepool")
	if err == nil {
		releaseDrainRunLock(context.Background(), duplicateLockFile)
		t.Fatal("expected duplicate lock error, got nil")
	}
	if !strings.Contains(err.Error(), "이미 동일 cluster/nodepool 드레인이 실행 중입니다") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAcquireDrainRunLockAllowsDifferentNodepool(t *testing.T) {
	lockFile, err := acquireLocalDrainRunLock("test-cluster", "test-nodepool-a")
	if err != nil {
		t.Fatalf("acquireDrainRunLock failed: %v", err)
	}
	defer releaseDrainRunLock(context.Background(), lockFile)

	otherLockFile, err := acquireLocalDrainRunLock("test-cluster", "test-nodepool-b")
	if err != nil {
		t.Fatalf("different nodepool should acquire lock: %v", err)
	}
	defer releaseDrainRunLock(context.Background(), otherLockFile)
}

func TestAcquireDrainRunLockFromEnvUsesEffectiveEnvTarget(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	clusterName = ""
	nodepoolName = ""
	t.Setenv("CLUSTER_NAME", "env-cluster")
	t.Setenv("NODEPOOL_NAME", "env-nodepool")

	ctx := context.Background()
	clientSet := fake.NewSimpleClientset()

	lock, err := acquireDrainRunLockFromEnv(ctx, clientSet, "kubernetes", "default", "10m")
	if err != nil {
		t.Fatalf("acquireDrainRunLockFromEnv failed: %v", err)
	}
	defer releaseDrainRunLock(ctx, lock)

	leaseName := kubernetesDrainLockLeaseName("env-cluster", "env-nodepool")
	if _, err = clientSet.CoordinationV1().Leases("default").Get(ctx, leaseName, metaV1.GetOptions{}); err != nil {
		t.Fatalf("expected env-target lease to exist: %v", err)
	}

	wrongLeaseName := kubernetesDrainLockLeaseName("", "")
	if _, err = clientSet.CoordinationV1().Leases("default").Get(ctx, wrongLeaseName, metaV1.GetOptions{}); err == nil {
		t.Fatalf("did not expect lock to use empty global target lease %s", wrongLeaseName)
	}
}

func TestLocalDrainRunLockReleaseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	lockFile, err := acquireLocalDrainRunLock("test-cluster", "test-idempotent-nodepool")
	if err != nil {
		t.Fatalf("acquireDrainRunLock failed: %v", err)
	}

	releaseDrainRunLock(ctx, lockFile)
	releaseDrainRunLock(ctx, lockFile)
}

func TestApplyDrainFlagEnvPreservesExistingEnvWhenFlagOmitted(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	drainMaxAbsolute = 0
	t.Setenv("DRAIN_MAX_ABSOLUTE", "1")

	command := &cobra.Command{}
	command.Flags().Int("drain-max-absolute", 0, "")

	applyDrainFlagEnv(command)

	if got := os.Getenv("DRAIN_MAX_ABSOLUTE"); got != "1" {
		t.Fatalf("expected existing env to be preserved, got %q", got)
	}
}

func TestApplyDrainFlagEnvUsesExplicitFlag(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	drainMaxAbsolute = 2
	t.Setenv("DRAIN_MAX_ABSOLUTE", "1")

	command := &cobra.Command{}
	command.Flags().Int("drain-max-absolute", 0, "")
	if err := command.Flags().Set("drain-max-absolute", "2"); err != nil {
		t.Fatalf("set flag failed: %v", err)
	}

	applyDrainFlagEnv(command)

	if got := os.Getenv("DRAIN_MAX_ABSOLUTE"); got != "2" {
		t.Fatalf("expected explicit flag to override env, got %q", got)
	}
}

func TestApplyDrainFlagEnvSetsExtendedPodTimeoutFlags(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	podEvictionTimeout = "3m"
	podNodeTerminationTimeout = "4m"
	podNodeTerminationCheckTick = "5s"
	podPostEvictionNodeDelay = "0s"

	command := &cobra.Command{}
	command.Flags().String("pod-eviction-timeout", "10m", "")
	command.Flags().String("pod-node-termination-timeout", "10m", "")
	command.Flags().String("pod-node-termination-check-tick", "15s", "")
	command.Flags().String("pod-post-eviction-node-delay", "50s", "")
	for flag, value := range map[string]string{
		"pod-eviction-timeout":            "3m",
		"pod-node-termination-timeout":    "4m",
		"pod-node-termination-check-tick": "5s",
		"pod-post-eviction-node-delay":    "0s",
	} {
		if err := command.Flags().Set(flag, value); err != nil {
			t.Fatalf("set %s failed: %v", flag, err)
		}
	}

	applyDrainFlagEnv(command)

	for key, want := range map[string]string{
		"POD_EVICTION_TIMEOUT":            "3m",
		"POD_NODE_TERMINATION_TIMEOUT":    "4m",
		"POD_NODE_TERMINATION_CHECK_TICK": "5s",
		"POD_POST_EVICTION_NODE_DELAY":    "0s",
	} {
		if got := os.Getenv(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestApplyDrainRuntimeEnvUsesExistingEnvWhenFlagsOmitted(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	drainDryRun = false
	drainLockMode = "local"
	drainLockNamespace = "kube-system"
	drainLockLeaseDuration = "10m"
	drainNodeSelectionStrategy = "oldest"
	drainSkipUnschedulable = false
	drainOutputFormat = "text"

	t.Setenv("DRAIN_DRY_RUN", "true")
	t.Setenv("DRAIN_LOCK_MODE", "kubernetes")
	t.Setenv("DRAIN_LOCK_NAMESPACE", "default")
	t.Setenv("DRAIN_LOCK_LEASE_DURATION", "30m")
	t.Setenv("DRAIN_NODE_SELECTION", "empty-first")
	t.Setenv("DRAIN_SKIP_UNSCHEDULABLE", "true")
	t.Setenv("DRAIN_OUTPUT_FORMAT", "json")

	command := newDrainRuntimeFlagCommand()
	applyDrainFlagEnv(command)
	if err := applyDrainRuntimeEnv(); err != nil {
		t.Fatalf("applyDrainRuntimeEnv failed: %v", err)
	}

	if !drainDryRun {
		t.Fatal("expected DRAIN_DRY_RUN env to enable dry-run")
	}
	if drainLockMode != "kubernetes" {
		t.Fatalf("drainLockMode = %q, want kubernetes", drainLockMode)
	}
	if drainLockNamespace != "default" {
		t.Fatalf("drainLockNamespace = %q, want default", drainLockNamespace)
	}
	if drainLockLeaseDuration != "30m" {
		t.Fatalf("drainLockLeaseDuration = %q, want 30m", drainLockLeaseDuration)
	}
	if drainNodeSelectionStrategy != "empty-first" {
		t.Fatalf("drainNodeSelectionStrategy = %q, want empty-first", drainNodeSelectionStrategy)
	}
	if !drainSkipUnschedulable {
		t.Fatal("expected DRAIN_SKIP_UNSCHEDULABLE env to enable skipping")
	}
	if drainOutputFormat != "json" {
		t.Fatalf("drainOutputFormat = %q, want json", drainOutputFormat)
	}
}

func TestApplyDrainRuntimeEnvUsesExplicitFlagOverEnv(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	drainDryRun = false
	drainLockMode = "none"
	drainOutputFormat = "text"

	t.Setenv("DRAIN_DRY_RUN", "true")
	t.Setenv("DRAIN_LOCK_MODE", "kubernetes")
	t.Setenv("DRAIN_OUTPUT_FORMAT", "json")

	command := newDrainRuntimeFlagCommand()
	if err := command.Flags().Set("dry-run", "false"); err != nil {
		t.Fatalf("set dry-run failed: %v", err)
	}
	if err := command.Flags().Set("drain-lock-mode", "none"); err != nil {
		t.Fatalf("set drain-lock-mode failed: %v", err)
	}
	if err := command.Flags().Set("output", "text"); err != nil {
		t.Fatalf("set output failed: %v", err)
	}

	applyDrainFlagEnv(command)
	if err := applyDrainRuntimeEnv(); err != nil {
		t.Fatalf("applyDrainRuntimeEnv failed: %v", err)
	}

	if drainDryRun {
		t.Fatal("expected explicit --dry-run=false to override env")
	}
	if drainLockMode != "none" {
		t.Fatalf("expected explicit lock mode to override env, got %q", drainLockMode)
	}
	if drainOutputFormat != "text" {
		t.Fatalf("expected explicit output to override env, got %q", drainOutputFormat)
	}
}

func TestApplyDrainRuntimeEnvRejectsInvalidBool(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	t.Setenv("DRAIN_DRY_RUN", "maybe")

	err := applyDrainRuntimeEnv()
	if err == nil {
		t.Fatal("expected invalid DRAIN_DRY_RUN error")
	}
	if !strings.Contains(err.Error(), "DRAIN_DRY_RUN") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReleaseDrainRunLockUsesFreshContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lock := &recordingDrainRunLock{}

	releaseDrainRunLock(ctx, lock)

	if !lock.released {
		t.Fatal("expected lock to be released")
	}
	if lock.releaseContextErr != nil {
		t.Fatalf("expected fresh release context, got context error: %v", lock.releaseContextErr)
	}
}

func TestAcquireKubernetesDrainRunLockBlocksDuplicate(t *testing.T) {
	ctx := context.Background()
	clientSet := fake.NewSimpleClientset()

	lock, err := acquireDrainRunLock(ctx, clientSet, "test-cluster", "test-nodepool", "kubernetes", "default", "10m")
	if err != nil {
		t.Fatalf("acquireDrainRunLock kubernetes failed: %v", err)
	}
	defer releaseDrainRunLock(ctx, lock)

	duplicateLock, err := acquireDrainRunLock(ctx, clientSet, "test-cluster", "test-nodepool", "kubernetes", "default", "10m")
	if err == nil {
		releaseDrainRunLock(ctx, duplicateLock)
		t.Fatal("expected duplicate kubernetes lock error, got nil")
	}
	if !strings.Contains(err.Error(), "이미 동일 cluster/nodepool 드레인이 실행 중입니다") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAcquireKubernetesDrainRunLockRejectsSubsecondLeaseDuration(t *testing.T) {
	ctx := context.Background()
	clientSet := fake.NewSimpleClientset()

	lock, err := acquireDrainRunLock(ctx, clientSet, "test-cluster", "test-nodepool", "kubernetes", "default", "500ms")
	if err == nil {
		releaseDrainRunLock(ctx, lock)
		t.Fatal("expected subsecond kubernetes lease duration error")
	}
	if !strings.Contains(err.Error(), "at least 1s") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAcquireKubernetesDrainRunLockRoundsLeaseDurationUp(t *testing.T) {
	ctx := context.Background()
	clientSet := fake.NewSimpleClientset()

	lock, err := acquireDrainRunLock(ctx, clientSet, "test-cluster", "test-roundup-nodepool", "kubernetes", "default", "1500ms")
	if err != nil {
		t.Fatalf("acquireDrainRunLock kubernetes failed: %v", err)
	}
	defer releaseDrainRunLock(ctx, lock)

	leaseName := kubernetesDrainLockLeaseName("test-cluster", "test-roundup-nodepool")
	lease, err := clientSet.CoordinationV1().Leases("default").Get(ctx, leaseName, metaV1.GetOptions{})
	if err != nil {
		t.Fatalf("lease lookup failed: %v", err)
	}
	if lease.Spec.LeaseDurationSeconds == nil {
		t.Fatal("expected lease duration seconds")
	}
	if got := *lease.Spec.LeaseDurationSeconds; got != 2 {
		t.Fatalf("lease duration seconds = %d, want=2", got)
	}
}

func TestKubernetesDrainRunLockReleaseDeletesLease(t *testing.T) {
	ctx := context.Background()
	clientSet := fake.NewSimpleClientset()

	lock, err := acquireDrainRunLock(ctx, clientSet, "test-cluster", "test-nodepool", "kubernetes", "default", "10m")
	if err != nil {
		t.Fatalf("acquireDrainRunLock kubernetes failed: %v", err)
	}
	releaseDrainRunLock(ctx, lock)

	leaseName := kubernetesDrainLockLeaseName("test-cluster", "test-nodepool")
	_, err = clientSet.CoordinationV1().Leases("default").Get(ctx, leaseName, metaV1.GetOptions{})
	if err == nil {
		t.Fatal("expected released lease to be deleted")
	}

	nextLock, err := acquireDrainRunLock(ctx, clientSet, "test-cluster", "test-nodepool", "kubernetes", "default", "10m")
	if err != nil {
		t.Fatalf("expected lock reacquire after release: %v", err)
	}
	defer releaseDrainRunLock(ctx, nextLock)
}

func TestKubernetesDrainRunLockReleaseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	clientSet := fake.NewSimpleClientset()

	lock, err := acquireDrainRunLock(ctx, clientSet, "test-cluster", "test-idempotent-nodepool", "kubernetes", "default", "10m")
	if err != nil {
		t.Fatalf("acquireDrainRunLock kubernetes failed: %v", err)
	}

	releaseDrainRunLock(ctx, lock)
	releaseDrainRunLock(ctx, lock)
}

func TestKubernetesLeaseRenewalIntervalStaysBelowShortLeaseDuration(t *testing.T) {
	interval := kubernetesLeaseRenewalInterval(5 * time.Second)
	if interval >= 5*time.Second {
		t.Fatalf("renewal interval = %s, want less than lease duration", interval)
	}
	if interval != 5*time.Second/3 {
		t.Fatalf("renewal interval = %s, want %s", interval, 5*time.Second/3)
	}
}

func TestKubernetesLeaseRenewalIntervalCapsLongLeaseDuration(t *testing.T) {
	interval := kubernetesLeaseRenewalInterval(10 * time.Minute)
	if interval != 10*time.Second {
		t.Fatalf("renewal interval = %s, want 10s", interval)
	}
}

func TestKubernetesLeaseRenewalRequestTimeoutStaysBelowShortLeaseDuration(t *testing.T) {
	timeout := kubernetesLeaseRenewalRequestTimeout(5 * time.Second)
	if timeout >= 5*time.Second {
		t.Fatalf("renewal timeout = %s, want less than lease duration", timeout)
	}
	if timeout != kubernetesLeaseRenewalInterval(5*time.Second) {
		t.Fatalf("renewal timeout = %s, want interval %s", timeout, kubernetesLeaseRenewalInterval(5*time.Second))
	}
}

func TestKubernetesLeaseRenewalRequestTimeoutCapsLongLeaseDuration(t *testing.T) {
	timeout := kubernetesLeaseRenewalRequestTimeout(10 * time.Minute)
	if timeout != 10*time.Second {
		t.Fatalf("renewal timeout = %s, want 10s", timeout)
	}
}

func TestKubernetesLeaseDurationSecondsRoundsUpFractionalDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     int32
	}{
		{name: "zero", duration: 0, want: 1},
		{name: "exact second", duration: time.Second, want: 1},
		{name: "fractional second", duration: 1500 * time.Millisecond, want: 2},
		{name: "minutes", duration: 10 * time.Minute, want: 600},
		{name: "overflow capped", duration: time.Duration(maxKubernetesLeaseDurationSeconds+1) * time.Second, want: int32(maxKubernetesLeaseDurationSeconds)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := kubernetesLeaseDurationSeconds(tt.duration); got != tt.want {
				t.Fatalf("kubernetesLeaseDurationSeconds(%s) = %d, want=%d", tt.duration, got, tt.want)
			}
		})
	}
}

func TestWriteNodeDrainReportJSON(t *testing.T) {
	var buf bytes.Buffer
	report := types.NodeDrainReport{
		Results: []types.NodeDrainResult{
			{
				NodeName: "node-1",
				Success:  true,
			},
		},
		Summary: types.NodeDrainSummary{
			TargetNodepool:         "test-nodepool",
			TotalNodesInNodepool:   3,
			PlannedDrainNodeCount:  1,
			SelectedDrainNodeCount: 1,
			SuccessfulNodeCount:    1,
		},
	}

	if err := writeNodeDrainReport(&buf, report, "json"); err != nil {
		t.Fatalf("writeNodeDrainReport failed: %v", err)
	}
	for _, want := range []string{
		`"node_name": "node-1"`,
		`"target_nodepool": "test-nodepool"`,
		`"selected_drain_node_count": 1`,
	} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("json output missing %q: %s", want, buf.String())
		}
	}
}

func TestWriteNodeDrainReportTextNoop(t *testing.T) {
	var buf bytes.Buffer
	if err := writeNodeDrainReport(&buf, types.NodeDrainReport{}, "text"); err != nil {
		t.Fatalf("writeNodeDrainReport text failed: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("text output should be noop, got: %s", buf.String())
	}
}

func TestParseDrainOutputFormatRejectsInvalid(t *testing.T) {
	if _, err := parseDrainOutputFormat("yaml"); err == nil {
		t.Fatal("expected invalid output format error")
	}
}

func TestSendNodeDrainErrorNotificationUsesFreshContext(t *testing.T) {
	notifier := &recordingNodeDrainSummaryNotifier{}

	if err := sendNodeDrainErrorNotification(notifier, errors.New("drain failed"), types.NodeDrainSummary{}); err != nil {
		t.Fatalf("sendNodeDrainErrorNotification failed: %v", err)
	}

	if !notifier.errorCalled {
		t.Fatal("expected error notification to be called")
	}
	if notifier.errorContextErr != nil {
		t.Fatalf("expected fresh notification context, got context error: %v", notifier.errorContextErr)
	}
	if !notifier.errorHasDeadline {
		t.Fatal("expected error notification context to have deadline")
	}
}

func TestSendNodeDrainCompleteNotificationUsesFreshContext(t *testing.T) {
	notifier := &recordingNodeDrainSummaryNotifier{}

	if err := sendNodeDrainCompleteNotification(notifier, []types.NodeDrainResult{{NodeName: "node-1"}}, types.NodeDrainSummary{}); err != nil {
		t.Fatalf("sendNodeDrainCompleteNotification failed: %v", err)
	}

	if !notifier.completeCalled {
		t.Fatal("expected complete notification to be called")
	}
	if notifier.completeContextErr != nil {
		t.Fatalf("expected fresh notification context, got context error: %v", notifier.completeContextErr)
	}
	if !notifier.completeHasDeadline {
		t.Fatal("expected complete notification context to have deadline")
	}
}

type recordingDrainRunLock struct {
	released          bool
	releaseContextErr error
}

func (l *recordingDrainRunLock) Release(ctx context.Context) error {
	l.released = true
	l.releaseContextErr = ctx.Err()
	return nil
}

type recordingNodeDrainSummaryNotifier struct {
	errorCalled         bool
	errorContextErr     error
	errorHasDeadline    bool
	completeCalled      bool
	completeContextErr  error
	completeHasDeadline bool
}

func (n *recordingNodeDrainSummaryNotifier) SendNodeDrainErrorWithSummary(ctx context.Context, err error, summary types.NodeDrainSummary) error {
	n.errorCalled = true
	n.errorContextErr = ctx.Err()
	_, n.errorHasDeadline = ctx.Deadline()
	return nil
}

func (n *recordingNodeDrainSummaryNotifier) SendNodeDrainCompleteWithSummary(ctx context.Context, results []types.NodeDrainResult, summary types.NodeDrainSummary) error {
	n.completeCalled = true
	n.completeContextErr = ctx.Err()
	_, n.completeHasDeadline = ctx.Deadline()
	return nil
}

func configureDrainCommandForValidationTest(t *testing.T) {
	t.Helper()

	for _, key := range []string{
		"DRAIN_POLICY",
		"DRAIN_ROUNDING",
		"DRAIN_MIN",
		"DRAIN_MAX_ABSOLUTE",
		"DRAIN_MAX_FRACTION",
		"DRAIN_STEP_RULES",
		"DRAIN_SAFETY_MAX_ALLOCATE_RATE",
		"DRAIN_SAFETY_QUERIES",
		"DRAIN_SAFETY_FAIL_CLOSED",
		"DRAIN_PROGRESSIVE",
		"DRAIN_DRY_RUN",
		"DRAIN_LOCK_MODE",
		"DRAIN_LOCK_NAMESPACE",
		"DRAIN_LOCK_LEASE_DURATION",
		"DRAIN_NODE_SELECTION",
		"DRAIN_SKIP_UNSCHEDULABLE",
		"DRAIN_OUTPUT_FORMAT",
		"POD_EVICTION_MODE",
		"POD_FORCE",
		"POD_FORCE_PROBLEM_PODS",
		"POD_DELETE_AFTER_EVICTION",
		"POD_PDB_TOKEN",
		"POD_PDB_TOKEN_MAX_IN_FLIGHT",
		"POD_MAX_CONCURRENT",
		"POD_MAX_RETRIES",
		"POD_RETRY_BACKOFF",
		"POD_DELETION_TIMEOUT",
		"POD_CHECK_INTERVAL",
		"POD_EVICTION_TIMEOUT",
		"POD_NODE_TERMINATION_TIMEOUT",
		"POD_NODE_TERMINATION_CHECK_TICK",
		"POD_POST_EVICTION_NODE_DELAY",
	} {
		t.Setenv(key, "")
	}

	prometheusAddress = "http://localhost:8080/prometheus"
	prometheusOrgID = "organization-dev"
	slackWebhookURL = ""
	kubeConfig = "invalid-mode"
	kubeConfigPath = ""
	clusterName = "test-cluster"
	nodepoolName = "test-nodepool"

	drainPolicy = "formula"
	drainRounding = "floor"
	drainMin = 0
	drainMaxAbsolute = 0
	drainMaxFraction = 0
	drainStepRules = ""
	drainSafetyMaxAllocateRate = 0
	drainSafetyQueries = ""
	drainSafetyFailClosed = true
	drainProgressive = true
	drainDryRun = false
	drainLockMode = "local"
	drainLockNamespace = "kube-system"
	drainLockLeaseDuration = "10m"
	drainNodeSelectionStrategy = "oldest"
	drainSkipUnschedulable = false
	drainOutputFormat = "text"

	podEvictionMode = "evict"
	podForce = false
	podForceProblemPods = true
	podDeleteAfterEviction = false
	podPDBToken = true
	podPDBTokenMaxInFlight = 1
	podMaxConcurrent = 30
	podMaxRetries = 3
	podRetryBackoff = "10s"
	podDeletionTimeout = "2m"
	podCheckInterval = "20s"
	podEvictionTimeout = "10m"
	podNodeTerminationTimeout = "10m"
	podNodeTerminationCheckTick = "15s"
	podPostEvictionNodeDelay = "50s"

	t.Setenv("PROMETHEUS_ADDRESS", "http://localhost:8080/prometheus")
	t.Setenv("CLUSTER_NAME", "test-cluster")
	t.Setenv("NODEPOOL_NAME", "test-nodepool")
}

func restoreCommandEnv(t *testing.T) {
	t.Helper()

	keys := []string{
		"PROMETHEUS_ADDRESS",
		"PROMETHEUS_SCOPE_ORG_ID",
		"SLACK_WEBHOOK_URL",
		"KUBE_CONFIG",
		"KUBECONFIG",
		"CLUSTER_NAME",
		"NODEPOOL_NAME",
		"DRAIN_POLICY",
		"DRAIN_ROUNDING",
		"DRAIN_MIN",
		"DRAIN_MAX_ABSOLUTE",
		"DRAIN_MAX_FRACTION",
		"DRAIN_STEP_RULES",
		"DRAIN_SAFETY_MAX_ALLOCATE_RATE",
		"DRAIN_SAFETY_QUERIES",
		"DRAIN_SAFETY_FAIL_CLOSED",
		"DRAIN_PROGRESSIVE",
		"DRAIN_DRY_RUN",
		"DRAIN_LOCK_MODE",
		"DRAIN_LOCK_NAMESPACE",
		"DRAIN_LOCK_LEASE_DURATION",
		"DRAIN_NODE_SELECTION",
		"DRAIN_SKIP_UNSCHEDULABLE",
		"DRAIN_OUTPUT_FORMAT",
		"POD_EVICTION_MODE",
		"POD_FORCE",
		"POD_FORCE_PROBLEM_PODS",
		"POD_DELETE_AFTER_EVICTION",
		"POD_PDB_TOKEN",
		"POD_PDB_TOKEN_MAX_IN_FLIGHT",
		"POD_MAX_CONCURRENT",
		"POD_MAX_RETRIES",
		"POD_RETRY_BACKOFF",
		"POD_DELETION_TIMEOUT",
		"POD_CHECK_INTERVAL",
		"POD_EVICTION_TIMEOUT",
		"POD_NODE_TERMINATION_TIMEOUT",
		"POD_NODE_TERMINATION_CHECK_TICK",
		"POD_POST_EVICTION_NODE_DELAY",
	}
	for _, key := range keys {
		t.Setenv(key, "")
	}
}

func newDrainRuntimeFlagCommand() *cobra.Command {
	command := &cobra.Command{}
	command.Flags().Bool("dry-run", false, "")
	command.Flags().String("drain-lock-mode", "local", "")
	command.Flags().String("drain-lock-namespace", "kube-system", "")
	command.Flags().String("drain-lock-lease-duration", "10m", "")
	command.Flags().String("drain-node-selection", "oldest", "")
	command.Flags().Bool("drain-skip-unschedulable", false, "")
	command.Flags().String("output", "text", "")
	return command
}

func snapshotCommandGlobals() func() {
	origPrometheusAddress := prometheusAddress
	origPrometheusOrgID := prometheusOrgID
	origSlackWebhookURL := slackWebhookURL
	origKubeConfig := kubeConfig
	origKubeConfigPath := kubeConfigPath
	origClusterName := clusterName
	origNodepoolName := nodepoolName

	origDrainPolicy := drainPolicy
	origDrainRounding := drainRounding
	origDrainMin := drainMin
	origDrainMaxAbsolute := drainMaxAbsolute
	origDrainMaxFraction := drainMaxFraction
	origDrainStepRules := drainStepRules
	origDrainSafetyMaxAllocateRate := drainSafetyMaxAllocateRate
	origDrainSafetyQueries := drainSafetyQueries
	origDrainSafetyFailClosed := drainSafetyFailClosed
	origDrainProgressive := drainProgressive
	origDrainDryRun := drainDryRun
	origDrainLockMode := drainLockMode
	origDrainLockNamespace := drainLockNamespace
	origDrainLockLeaseDuration := drainLockLeaseDuration
	origDrainNodeSelectionStrategy := drainNodeSelectionStrategy
	origDrainSkipUnschedulable := drainSkipUnschedulable
	origDrainOutputFormat := drainOutputFormat

	origPodEvictionMode := podEvictionMode
	origPodForce := podForce
	origPodForceProblemPods := podForceProblemPods
	origPodDeleteAfterEviction := podDeleteAfterEviction
	origPodPDBToken := podPDBToken
	origPodPDBTokenMaxInFlight := podPDBTokenMaxInFlight
	origPodMaxConcurrent := podMaxConcurrent
	origPodMaxRetries := podMaxRetries
	origPodRetryBackoff := podRetryBackoff
	origPodDeletionTimeout := podDeletionTimeout
	origPodCheckInterval := podCheckInterval
	origPodEvictionTimeout := podEvictionTimeout
	origPodNodeTerminationTimeout := podNodeTerminationTimeout
	origPodNodeTerminationCheckTick := podNodeTerminationCheckTick
	origPodPostEvictionNodeDelay := podPostEvictionNodeDelay

	return func() {
		prometheusAddress = origPrometheusAddress
		prometheusOrgID = origPrometheusOrgID
		slackWebhookURL = origSlackWebhookURL
		kubeConfig = origKubeConfig
		kubeConfigPath = origKubeConfigPath
		clusterName = origClusterName
		nodepoolName = origNodepoolName

		drainPolicy = origDrainPolicy
		drainRounding = origDrainRounding
		drainMin = origDrainMin
		drainMaxAbsolute = origDrainMaxAbsolute
		drainMaxFraction = origDrainMaxFraction
		drainStepRules = origDrainStepRules
		drainSafetyMaxAllocateRate = origDrainSafetyMaxAllocateRate
		drainSafetyQueries = origDrainSafetyQueries
		drainSafetyFailClosed = origDrainSafetyFailClosed
		drainProgressive = origDrainProgressive
		drainDryRun = origDrainDryRun
		drainLockMode = origDrainLockMode
		drainLockNamespace = origDrainLockNamespace
		drainLockLeaseDuration = origDrainLockLeaseDuration
		drainNodeSelectionStrategy = origDrainNodeSelectionStrategy
		drainSkipUnschedulable = origDrainSkipUnschedulable
		drainOutputFormat = origDrainOutputFormat

		podEvictionMode = origPodEvictionMode
		podForce = origPodForce
		podForceProblemPods = origPodForceProblemPods
		podDeleteAfterEviction = origPodDeleteAfterEviction
		podPDBToken = origPodPDBToken
		podPDBTokenMaxInFlight = origPodPDBTokenMaxInFlight
		podMaxConcurrent = origPodMaxConcurrent
		podMaxRetries = origPodMaxRetries
		podRetryBackoff = origPodRetryBackoff
		podDeletionTimeout = origPodDeletionTimeout
		podCheckInterval = origPodCheckInterval
		podEvictionTimeout = origPodEvictionTimeout
		podNodeTerminationTimeout = origPodNodeTerminationTimeout
		podNodeTerminationCheckTick = origPodNodeTerminationCheckTick
		podPostEvictionNodeDelay = origPodPostEvictionNodeDelay
	}
}
