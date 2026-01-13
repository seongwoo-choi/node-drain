package node

import (
	"app/config"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	prometheusModel "github.com/prometheus/common/model"
)

type DrainPolicy string

const (
	DrainPolicyFormula DrainPolicy = "formula"
	DrainPolicyStep    DrainPolicy = "step"
)

type DrainRounding string

const (
	DrainRoundingFloor DrainRounding = "floor"
	DrainRoundingRound DrainRounding = "round"
	DrainRoundingCeil  DrainRounding = "ceil"
)

type StepRule struct {
	MaxAllocateRate int // maxAllocateRate가 이 값 이하일 때 적용
	DrainCount      int
}

type DrainPolicyOptions struct {
	Policy                DrainPolicy
	Rounding              DrainRounding
	MinDrain              int
	MaxDrainAbsolute      int     // 0 이면 비활성
	MaxDrainFraction      float64 // 0 이면 비활성 (예: 0.2 = 최대 20%)
	StepRules             []StepRule
	SafetyMaxAllocateRate int      // 0 이면 비활성 (예: 90이면 maxAllocateRate>=90일 때 0대로 강제)
	SafetyQueries         []string // PromQL; 하나라도 결과가 >0이면 0대로 강제
	SafetyFailClosed      bool     // safety query 실패 시 0대로 강제할지
}

// GetDrainPolicyOptionsFromEnv는 drain 정책 관련 환경 변수를 파싱합니다.
// 기본값은 "기존 동작 유지"를 목표로 합니다.
func GetDrainPolicyOptionsFromEnv() DrainPolicyOptions {
	opts := DrainPolicyOptions{
		Policy:           DrainPolicyFormula,
		Rounding:         DrainRoundingFloor,
		MinDrain:         0,
		MaxDrainAbsolute: 0,
		MaxDrainFraction: 0,
		StepRules:        nil,
		SafetyQueries:    nil,
		// 기존 동작은 allocate rate로만 결정하므로, 안전 조건은 기본 비활성
		SafetyMaxAllocateRate: 0,
		SafetyFailClosed:      true, // safety query를 쓰는 경우엔 보수적으로
	}

	if v := strings.TrimSpace(os.Getenv("DRAIN_POLICY")); v != "" {
		switch DrainPolicy(strings.ToLower(v)) {
		case DrainPolicyFormula, DrainPolicyStep:
			opts.Policy = DrainPolicy(strings.ToLower(v))
		}
	}

	if v := strings.TrimSpace(os.Getenv("DRAIN_ROUNDING")); v != "" {
		switch DrainRounding(strings.ToLower(v)) {
		case DrainRoundingFloor, DrainRoundingRound, DrainRoundingCeil:
			opts.Rounding = DrainRounding(strings.ToLower(v))
		}
	}

	opts.MinDrain = parseEnvInt("DRAIN_MIN", opts.MinDrain)
	opts.MaxDrainAbsolute = parseEnvInt("DRAIN_MAX_ABSOLUTE", opts.MaxDrainAbsolute)
	opts.SafetyMaxAllocateRate = parseEnvInt("DRAIN_SAFETY_MAX_ALLOCATE_RATE", opts.SafetyMaxAllocateRate)

	opts.MaxDrainFraction = parseEnvFloat("DRAIN_MAX_FRACTION", opts.MaxDrainFraction)

	if v := strings.TrimSpace(os.Getenv("DRAIN_SAFETY_FAIL_CLOSED")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			opts.SafetyFailClosed = b
		}
	}

	if v := strings.TrimSpace(os.Getenv("DRAIN_STEP_RULES")); v != "" {
		if rules, err := parseStepRules(v); err == nil {
			opts.StepRules = rules
		}
	}

	if v := strings.TrimSpace(os.Getenv("DRAIN_SAFETY_QUERIES")); v != "" {
		opts.SafetyQueries = splitQueries(v)
	}

	return opts
}

func parseEnvInt(key string, defaultValue int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultValue
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return defaultValue
	}
	return i
}

func parseEnvFloat(key string, defaultValue float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultValue
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return defaultValue
	}
	return f
}

func splitQueries(s string) []string {
	// 세미콜론/개행 구분 지원
	s = strings.ReplaceAll(s, "\n", ";")
	parts := strings.Split(s, ";")
	var out []string
	for _, p := range parts {
		q := strings.TrimSpace(p)
		if q != "" {
			out = append(out, q)
		}
	}
	return out
}

// parseStepRules: "80:1,60:2" (콤마/세미콜론 지원)
func parseStepRules(s string) ([]StepRule, error) {
	s = strings.ReplaceAll(s, ";", ",")
	parts := strings.Split(s, ",")
	var rules []StepRule
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		kv := strings.Split(p, ":")
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid step rule: %q", p)
		}
		thr, err := strconv.Atoi(strings.TrimSpace(kv[0]))
		if err != nil {
			return nil, fmt.Errorf("invalid threshold: %q", kv[0])
		}
		cnt, err := strconv.Atoi(strings.TrimSpace(kv[1]))
		if err != nil {
			return nil, fmt.Errorf("invalid drain count: %q", kv[1])
		}
		rules = append(rules, StepRule{MaxAllocateRate: thr, DrainCount: cnt})
	}

	sort.Slice(rules, func(i, j int) bool {
		return rules[i].MaxAllocateRate < rules[j].MaxAllocateRate
	})
	return rules, nil
}

// ShouldBlockDrainBySafetyConditions는 안전 조건에 의해 0대 드레인을 강제해야 하는지 판단합니다.
func ShouldBlockDrainBySafetyConditions(maxAllocateRate int, opts DrainPolicyOptions) (bool, string, error) {
	if opts.SafetyMaxAllocateRate > 0 && maxAllocateRate >= opts.SafetyMaxAllocateRate {
		return true, fmt.Sprintf("maxAllocateRate(%d) >= safetyMaxAllocateRate(%d)", maxAllocateRate, opts.SafetyMaxAllocateRate), nil
	}

	if len(opts.SafetyQueries) == 0 {
		return false, "", nil
	}

	promClient, err := config.CreatePrometheusClient()
	if err != nil {
		if opts.SafetyFailClosed {
			return true, "prometheus client init failed (fail-closed)", err
		}
		return false, "prometheus client init failed (fail-open)", err
	}

	for _, q := range opts.SafetyQueries {
		vec, qErr := config.QueryPrometheus(promClient, q)
		if qErr != nil {
			if opts.SafetyFailClosed {
				return true, fmt.Sprintf("safety query failed (fail-closed): %s", q), qErr
			}
			slog.Warn("안전 조건 쿼리 실패(무시하고 진행)", "query", q, "error", qErr)
			continue
		}
		if prometheusVectorHasPositive(vec) {
			return true, fmt.Sprintf("safety query triggered: %s", q), nil
		}
	}

	return false, "", nil
}

func prometheusVectorHasPositive(vec prometheusModel.Vector) bool {
	for _, s := range vec {
		if float64(s.Value) > 0 {
			return true
		}
	}
	return false
}

// CalculateDrainNodeCount는 정책/라운딩/클램프를 적용해 최종 드레인 대상 노드 수를 계산합니다.
func CalculateDrainNodeCount(lenNodes int, maxAllocateRate int, opts DrainPolicyOptions) int {
	if lenNodes <= 0 {
		return 0
	}

	base := 0
	switch opts.Policy {
	case DrainPolicyStep:
		base = stepPolicyCount(maxAllocateRate, opts.StepRules)
	default:
		base = formulaPolicyCount(lenNodes, maxAllocateRate, opts)
	}

	// 최종 클램프/상한 적용
	base = clampInt(base, 0, lenNodes)

	// minDrain은 "드레인을 하기로 결정된 경우" 최소값으로 적용 (작은 클러스터 0대 방지용)
	if base > 0 && opts.MinDrain > 0 && base < opts.MinDrain {
		base = opts.MinDrain
	}

	// 상한(퍼센트/절대) 적용
	base = applyCaps(lenNodes, base, opts)

	return clampInt(base, 0, lenNodes)
}

func stepPolicyCount(maxAllocateRate int, rules []StepRule) int {
	if len(rules) == 0 {
		// 규칙이 없으면 안전하게 0
		return 0
	}
	for _, r := range rules {
		if maxAllocateRate <= r.MaxAllocateRate {
			if r.DrainCount < 0 {
				return 0
			}
			return r.DrainCount
		}
	}
	return 0
}

func formulaPolicyCount(lenNodes int, maxAllocateRate int, opts DrainPolicyOptions) int {
	drainRate := float64(99-maxAllocateRate) / 100.0
	if drainRate < 0 {
		drainRate = 0
	}
	if drainRate > 1 {
		drainRate = 1
	}

	raw := float64(lenNodes) * drainRate

	var base int
	switch opts.Rounding {
	case DrainRoundingRound:
		base = int(math.Round(raw))
	case DrainRoundingCeil:
		base = int(math.Ceil(raw))
	default:
		base = int(math.Floor(raw))
	}

	// 작은 클러스터에서 floor로 0이 되는 케이스를 minDrain로 보정
	// (drainRate가 0이 아닌데 0으로 떨어졌다면 최소 1대로 올리고 싶을 때 사용)
	if base == 0 && drainRate > 0 && opts.MinDrain > 0 {
		base = opts.MinDrain
	}

	return base
}

func applyCaps(lenNodes int, base int, opts DrainPolicyOptions) int {
	if base <= 0 {
		return 0
	}

	capValue := lenNodes

	if opts.MaxDrainFraction > 0 {
		// "최대 X%"는 ceil로 계산해 작은 클러스터에서도 정책이 의미 있게 동작하도록
		fCap := int(math.Ceil(float64(lenNodes) * opts.MaxDrainFraction))
		if fCap < 0 {
			fCap = 0
		}
		if fCap < capValue {
			capValue = fCap
		}
	}

	if opts.MaxDrainAbsolute > 0 && opts.MaxDrainAbsolute < capValue {
		capValue = opts.MaxDrainAbsolute
	}

	if capValue < 0 {
		capValue = 0
	}
	if base > capValue {
		base = capValue
	}
	return base
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
