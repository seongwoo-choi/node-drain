package pod

import (
	"fmt"
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

	if v := strings.TrimSpace(os.Getenv("POD_DELETE_AFTER_EVICTION")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.DeleteAfterEviction = b
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

func ValidateEvictionConfigEnv() error {
	if v := strings.TrimSpace(os.Getenv("POD_EVICTION_MODE")); v != "" {
		switch EvictionMode(strings.ToLower(v)) {
		case EvictionModeEvict, EvictionModeDelete:
		default:
			return fmt.Errorf("invalid POD_EVICTION_MODE: %s", v)
		}
	}

	for _, key := range []string{"POD_FORCE", "POD_FORCE_PROBLEM_PODS", "POD_DELETE_AFTER_EVICTION", "POD_PDB_TOKEN"} {
		if err := validateBoolEnv(key); err != nil {
			return err
		}
	}
	for _, key := range []string{"POD_PDB_TOKEN_MAX_IN_FLIGHT", "POD_MAX_CONCURRENT"} {
		if err := validatePositiveEnvInt(key); err != nil {
			return err
		}
	}
	if err := validatePositiveEnvInt("POD_MAX_RETRIES"); err != nil {
		return err
	}
	for _, key := range []string{"POD_RETRY_BACKOFF", "POD_DELETION_TIMEOUT", "POD_CHECK_INTERVAL"} {
		if err := validatePositiveEnvDuration(key); err != nil {
			return err
		}
	}
	return nil
}

func validateBoolEnv(key string) error {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	if _, err := strconv.ParseBool(v); err != nil {
		return fmt.Errorf("invalid %s: %w", key, err)
	}
	return nil
}

func validatePositiveEnvInt(key string) error {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", key, err)
	}
	if i <= 0 {
		return fmt.Errorf("invalid %s: must be greater than 0", key)
	}
	return nil
}

func validateNonNegativeEnvInt(key string) error {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", key, err)
	}
	if i < 0 {
		return fmt.Errorf("invalid %s: must be non-negative", key)
	}
	return nil
}

func validatePositiveEnvDuration(key string) error {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", key, err)
	}
	if d <= 0 {
		return fmt.Errorf("invalid %s: must be greater than 0", key)
	}
	return nil
}
