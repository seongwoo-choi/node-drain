package pod

// EvictionReport는 노드 단위(또는 drain run 단위)로 파드 제거 결과를 집계하기 위한 구조체입니다.
// Slack/로그 요약(관측성 강화)에 사용합니다.
type EvictionReport struct {
	NodeName          string
	TotalPods         int
	EvictedPods       int
	DeletedPods       int
	ForceDeletedPods  int
	PDBBlockedPods    int
	ErrorsByReason    map[string]int
	ForcedByFallback  int // eviction 실패 후 force(delete)로 전환한 횟수
	ProblemPodsForced int // 문제 파드를 즉시 강제 삭제한 횟수
}

func (r *EvictionReport) addErrorReason(reason string) {
	if reason == "" {
		reason = "unknown"
	}
	if r.ErrorsByReason == nil {
		r.ErrorsByReason = map[string]int{}
	}
	r.ErrorsByReason[reason]++
}
