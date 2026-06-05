package cmd

import (
	"app/types"
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

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

	err := drainCmd.RunE(drainCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "쿠버네티스 클라이언트 생성 실패") {
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

func TestLocalDrainRunLockReleaseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	lockFile, err := acquireLocalDrainRunLock("test-cluster", "test-idempotent-nodepool")
	if err != nil {
		t.Fatalf("acquireDrainRunLock failed: %v", err)
	}

	releaseDrainRunLock(ctx, lockFile)
	releaseDrainRunLock(ctx, lockFile)
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
	}
	for _, key := range keys {
		t.Setenv(key, os.Getenv(key))
	}
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
	}
}
