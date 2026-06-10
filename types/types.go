package types

// NodeDrainResult represents the execution result for a single node drain operation.
type NodeDrainResult struct {
	NodeName        string             `json:"node_name"`
	InstanceType    string             `json:"instance_type"`
	NodepoolName    string             `json:"nodepool_name"`
	Age             string             `json:"age"`
	StartedAt       string             `json:"started_at"`
	DurationSeconds int64              `json:"duration_seconds"`
	Success         bool               `json:"success"`
	FailureReason   string             `json:"failure_reason,omitempty"`
	DryRun          bool               `json:"dry_run,omitempty"`
	PlannedPods     []NodeDrainPodPlan `json:"planned_pods,omitempty"`
}

// NodeDrainPodPlan describes a pod that would be removed from a node during dry-run analysis.
type NodeDrainPodPlan struct {
	Namespace     string                `json:"namespace"`
	Name          string                `json:"name"`
	Phase         string                `json:"phase"`
	OwnerKind     string                `json:"owner_kind,omitempty"`
	OwnerName     string                `json:"owner_name,omitempty"`
	Finalizers    []string              `json:"finalizers,omitempty"`
	ProblemState  bool                  `json:"problem_state,omitempty"`
	ProblemReason string                `json:"problem_reason,omitempty"`
	Warnings      []string              `json:"warnings,omitempty"`
	PDBBlockers   []NodeDrainPDBBlocker `json:"pdb_blockers,omitempty"`
}

// NodeDrainPDBBlocker describes a PDB currently blocking a planned pod eviction.
type NodeDrainPDBBlocker struct {
	Namespace          string `json:"namespace"`
	Name               string `json:"name"`
	DisruptionsAllowed int32  `json:"disruptions_allowed"`
}

type NodeDrainReport struct {
	Results []NodeDrainResult `json:"results"`
	Summary NodeDrainSummary  `json:"summary"`
}

type NodeDrainSummary struct {
	TargetNodepool         string `json:"target_nodepool"`
	TotalNodesInNodepool   int    `json:"total_nodes_in_nodepool"`
	PlannedDrainNodeCount  int    `json:"planned_drain_node_count"`
	SelectedDrainNodeCount int    `json:"selected_drain_node_count"`
	CordonedNodeCount      int    `json:"cordoned_node_count"`
	DrainedNodeCount       int    `json:"drained_node_count"`
	SuccessfulNodeCount    int    `json:"successful_node_count"`
	FailedNodeCount        int    `json:"failed_node_count"`
	DryRun                 bool   `json:"dry_run"`

	TotalPods          int `json:"total_pods"`
	PlannedPodCount    int `json:"planned_pod_count"`
	EvictedPods        int `json:"evicted_pods"`
	DeletedPods        int `json:"deleted_pods"`
	ForceDeletedPods   int `json:"force_deleted_pods"`
	PDBBlockedPods     int `json:"pdb_blocked_pods"`
	ProblemPodCount    int `json:"problem_pod_count"`
	UnmanagedPodCount  int `json:"unmanaged_pod_count"`
	PodsWithFinalizers int `json:"pods_with_finalizers"`
	ForcedByFallback   int `json:"forced_by_fallback"`
	ProblemPodsForced  int `json:"problem_pods_forced"`

	StoppedBySafety  bool   `json:"stopped_by_safety"`
	StopSafetyReason string `json:"stop_safety_reason"`

	TopErrorReasons []string `json:"top_error_reasons"`
	Warnings        []string `json:"warnings"`
}
