package types

type NodeInfo struct {
	NodeName  string  `json:"node_name"`
	NodeUsage float64 `json:"usage"`
}

type NodeDrainResult struct {
	NodeName     string `json:"node_name"`
	InstanceType string `json:"instance_type"`
	NodepoolName string `json:"nodepool_name"`
	Age          string `json:"age"`
}

type NodeDrainSummary struct {
	TargetNodepool        string `json:"target_nodepool"`
	TotalNodesInNodepool  int    `json:"total_nodes_in_nodepool"`
	PlannedDrainNodeCount int    `json:"planned_drain_node_count"`
	DrainedNodeCount      int    `json:"drained_node_count"`

	TotalPods         int `json:"total_pods"`
	EvictedPods       int `json:"evicted_pods"`
	DeletedPods       int `json:"deleted_pods"`
	ForceDeletedPods  int `json:"force_deleted_pods"`
	PDBBlockedPods    int `json:"pdb_blocked_pods"`
	ForcedByFallback  int `json:"forced_by_fallback"`
	ProblemPodsForced int `json:"problem_pods_forced"`

	StoppedBySafety  bool   `json:"stopped_by_safety"`
	StopSafetyReason string `json:"stop_safety_reason"`

	TopErrorReasons []string `json:"top_error_reasons"`
}
