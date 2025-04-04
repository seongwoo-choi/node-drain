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
