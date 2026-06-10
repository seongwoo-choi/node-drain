package cmd

import (
	"app/pkg/node"
	"app/types"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	coreV1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func TestAnalyzeCommandRequiresTargetSettings(t *testing.T) {
	if analyzeCmd.RunE == nil {
		t.Fatal("analyze command RunE is nil")
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

	err := analyzeCmd.RunE(&cobra.Command{}, nil)
	if err == nil {
		t.Fatal("expected required setting error, got nil")
	}
	if !strings.Contains(err.Error(), "prometheus-address") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAnalyzeCommandRejectsUnexpectedArgs(t *testing.T) {
	if analyzeCmd.Args == nil {
		t.Fatal("analyze command Args is nil")
	}

	err := analyzeCmd.Args(analyzeCmd, []string{"false"})
	if err == nil {
		t.Fatal("expected unexpected arg error")
	}
}

func TestAnalyzeCommandRejectsPrometheusAPIEndpointBeforeKubeClient(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	prometheusAddress = ""
	clusterName = ""
	nodepoolName = ""
	t.Setenv("PROMETHEUS_ADDRESS", "http://localhost:8080/prometheus/api/v1/query")
	t.Setenv("CLUSTER_NAME", "test-cluster")
	t.Setenv("NODEPOOL_NAME", "test-nodepool")

	err := analyzeCmd.RunE(&cobra.Command{}, nil)
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

func TestBuildNodeDrainAnalysisConfigAlwaysDryRun(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	t.Setenv("NODEPOOL_NAME", " test-nodepool ")
	drainNodeSelectionStrategy = "empty-first"
	drainSkipUnschedulable = true

	cfg := buildNodeDrainAnalysisConfig()
	if !cfg.DryRun {
		t.Fatal("expected analyze config to force dry-run")
	}
	if cfg.NodepoolName != "test-nodepool" {
		t.Fatalf("nodepool = %q, want test-nodepool", cfg.NodepoolName)
	}
	if cfg.NodeSelectionStrategy != node.DrainNodeSelectionEmptyFirst {
		t.Fatalf("node selection = %q, want empty-first", cfg.NodeSelectionStrategy)
	}
	if !cfg.SkipUnschedulable {
		t.Fatal("expected skip unschedulable to be enabled")
	}
}

func TestRunNodeDrainAnalysisOutputsDryRunReportWithoutMutatingCluster(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	t.Setenv("NODEPOOL_NAME", "test-nodepool")
	t.Setenv("DRAIN_POLICY", "formula")
	t.Setenv("DRAIN_ROUNDING", "floor")
	t.Setenv("DRAIN_MAX_ABSOLUTE", "1")

	clientSet := fake.NewSimpleClientset()
	for i := 1; i <= 2; i++ {
		n := analyzeTestNode("test-nodepool", i)
		if _, err := clientSet.CoreV1().Nodes().Create(context.Background(), n, metaV1.CreateOptions{}); err != nil {
			t.Fatalf("node create failed: %v", err)
		}
	}

	p := analyzeTestPod("default", "workload-pod", "node-1")
	if _, err := clientSet.CoreV1().Pods(p.Namespace).Create(context.Background(), p, metaV1.CreateOptions{}); err != nil {
		t.Fatalf("pod create failed: %v", err)
	}
	pdb := analyzeTestPDB("default", "blocking-pdb", 0)
	if _, err := clientSet.PolicyV1().PodDisruptionBudgets("default").Create(context.Background(), pdb, metaV1.CreateOptions{}); err != nil {
		t.Fatalf("pdb create failed: %v", err)
	}

	var buf bytes.Buffer
	err := runNodeDrainAnalysis(context.Background(), clientSet, node.DrainDependencies{
		AllocateRateProvider: analyzeFakeAllocateRateProvider{
			rates: map[string]int{
				"memory": 30,
				"cpu":    30,
			},
		},
	}, buildNodeDrainAnalysisConfig(), &buf, "json")
	if err != nil {
		t.Fatalf("runNodeDrainAnalysis failed: %v", err)
	}

	for _, want := range []string{
		`"dry_run": true`,
		`"planned_drain_node_count": 1`,
		`"planned_pod_count": 1`,
		`"pdb_blocked_pods": 1`,
		`"node_name": "node-1"`,
		`"name": "workload-pod"`,
		`"pdb_blockers": [`,
		`"name": "blocking-pdb"`,
	} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("analysis json missing %q: %s", want, buf.String())
		}
	}

	nodeAfter, err := clientSet.CoreV1().Nodes().Get(context.Background(), "node-1", metaV1.GetOptions{})
	if err != nil {
		t.Fatalf("node get failed: %v", err)
	}
	if nodeAfter.Spec.Unschedulable {
		t.Fatal("analyze must not cordon nodes")
	}
	if _, err = clientSet.CoreV1().Pods("default").Get(context.Background(), "workload-pod", metaV1.GetOptions{}); err != nil {
		t.Fatalf("analyze must not delete pods: %v", err)
	}
}

func TestRunNodeDrainAnalysisOutputsReportEvenWhenPlanFails(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	var buf bytes.Buffer
	err := runNodeDrainAnalysis(context.Background(), nil, node.DrainDependencies{}, node.DrainConfig{}, &buf, "json")
	if err == nil {
		t.Fatal("expected analysis error")
	}
	if !strings.Contains(err.Error(), "드레인 사전 진단 실패") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), `"summary"`) {
		t.Fatalf("expected partial report output, got: %s", buf.String())
	}
}

func TestWriteNodeDrainAnalysisReportText(t *testing.T) {
	var buf bytes.Buffer
	report := types.NodeDrainReport{
		Summary: types.NodeDrainSummary{
			TargetNodepool:         "test-nodepool",
			TotalNodesInNodepool:   3,
			PlannedDrainNodeCount:  1,
			SelectedDrainNodeCount: 1,
			PlannedPodCount:        2,
			DryRun:                 true,
		},
	}

	if err := writeNodeDrainAnalysisReport(&buf, report, "text"); err != nil {
		t.Fatalf("writeNodeDrainAnalysisReport text failed: %v", err)
	}
	for _, want := range []string{"test-nodepool", "planned=1", "selected=1", "plannedPods=2", "dryRun=true"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("text output missing %q: %s", want, buf.String())
		}
	}
}

type analyzeFakeAllocateRateProvider struct {
	rates map[string]int
}

func (p analyzeFakeAllocateRateProvider) GetAllocateRate(ctx context.Context, resourceType string) (int, error) {
	return p.rates[resourceType], nil
}

func analyzeTestNode(nodepoolName string, index int) *coreV1.Node {
	return &coreV1.Node{
		ObjectMeta: metaV1.ObjectMeta{
			Name:              fmt.Sprintf("node-%d", index),
			CreationTimestamp: metaV1.NewTime(time.Date(2026, 1, index, 0, 0, 0, 0, time.UTC)),
			Labels: map[string]string{
				"karpenter.sh/nodepool":            nodepoolName,
				"beta.kubernetes.io/instance-type": "m7g.large",
			},
		},
	}
}

func analyzeTestPod(namespace string, name string, nodeName string) *coreV1.Pod {
	return &coreV1.Pod{
		ObjectMeta: metaV1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"app": "test",
			},
			OwnerReferences: []metaV1.OwnerReference{
				{
					Kind: "ReplicaSet",
					Name: "workload-rs",
				},
			},
		},
		Spec: coreV1.PodSpec{
			NodeName: nodeName,
		},
		Status: coreV1.PodStatus{
			Phase: coreV1.PodRunning,
		},
	}
}

func analyzeTestPDB(namespace string, name string, disruptionsAllowed int32) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metaV1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
			Selector: &metaV1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
		},
		Status: policyv1.PodDisruptionBudgetStatus{
			DisruptionsAllowed: disruptionsAllowed,
		},
	}
}
