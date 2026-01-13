package pod

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// GetEvictionConfigFromEnv는 파드 제거(=eviction/delete) 정책을 환경 변수에서 읽어 EvictionConfig로 변환합니다.
// cmd에서 플래그→env 주입 후 pkg/pod에서 공통으로 사용하기 위한 함수입니다.
func GetEvictionConfigFromEnv() *EvictionConfig {
	cfg := DefaultEvictionConfig()

	if v := strings.TrimSpace(os.Getenv("POD_EVICTION_MODE")); v != "" {
		switch EvictionMode(strings.ToLower(v)) {
		case EvictionModeEvict, EvictionModeDelete:
			cfg.EvictionMode = EvictionMode(strings.ToLower(v))
		}
	}

	if v := strings.TrimSpace(os.Getenv("POD_FORCE")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Force = b
		}
	}

	if v := strings.TrimSpace(os.Getenv("POD_FORCE_PROBLEM_PODS")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.ForceProblemPods = b
		}
	}

	if v := strings.TrimSpace(os.Getenv("POD_PDB_TOKEN")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.PDBToken = b
		}
	}

	if v := strings.TrimSpace(os.Getenv("POD_PDB_TOKEN_MAX_IN_FLIGHT")); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.PDBTokenMaxInFlight = i
		}
	}

	if v := strings.TrimSpace(os.Getenv("POD_MAX_CONCURRENT")); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.MaxConcurrentEvictions = i
		}
	}

	if v := strings.TrimSpace(os.Getenv("POD_MAX_RETRIES")); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.MaxRetries = i
		}
	}

	if v := strings.TrimSpace(os.Getenv("POD_RETRY_BACKOFF")); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.RetryBackoffDuration = d
		}
	}

	if v := strings.TrimSpace(os.Getenv("POD_DELETION_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.PodDeletionTimeout = d
		}
	}

	if v := strings.TrimSpace(os.Getenv("POD_CHECK_INTERVAL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.CheckInterval = d
		}
	}

	// 안전 클램프
	if cfg.MaxConcurrentEvictions <= 0 {
		cfg.MaxConcurrentEvictions = 1
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.PDBTokenMaxInFlight <= 0 {
		cfg.PDBTokenMaxInFlight = 1
	}

	return cfg
}
