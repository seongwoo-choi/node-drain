package pod

import (
	"strings"
	"testing"
	"time"
)

func TestValidateEvictionConfigEnvRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
		want string
	}{
		{name: "invalid mode", key: "POD_EVICTION_MODE", val: "replace", want: "POD_EVICTION_MODE"},
		{name: "invalid bool", key: "POD_FORCE", val: "maybe", want: "POD_FORCE"},
		{name: "zero concurrency", key: "POD_MAX_CONCURRENT", val: "0", want: "POD_MAX_CONCURRENT"},
		{name: "zero retries", key: "POD_MAX_RETRIES", val: "0", want: "POD_MAX_RETRIES"},
		{name: "negative retries", key: "POD_MAX_RETRIES", val: "-1", want: "POD_MAX_RETRIES"},
		{name: "invalid duration", key: "POD_RETRY_BACKOFF", val: "soon", want: "POD_RETRY_BACKOFF"},
		{name: "zero eviction timeout", key: "POD_EVICTION_TIMEOUT", val: "0s", want: "POD_EVICTION_TIMEOUT"},
		{name: "zero node termination timeout", key: "POD_NODE_TERMINATION_TIMEOUT", val: "0s", want: "POD_NODE_TERMINATION_TIMEOUT"},
		{name: "zero node termination check tick", key: "POD_NODE_TERMINATION_CHECK_TICK", val: "0s", want: "POD_NODE_TERMINATION_CHECK_TICK"},
		{name: "negative post eviction delay", key: "POD_POST_EVICTION_NODE_DELAY", val: "-1s", want: "POD_POST_EVICTION_NODE_DELAY"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEvictionConfigEnv(t)
			t.Setenv(tt.key, tt.val)

			err := ValidateEvictionConfigEnv()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q missing %q", err.Error(), tt.want)
			}
		})
	}
}

func TestValidateEvictionConfigEnvAcceptsValidValues(t *testing.T) {
	clearEvictionConfigEnv(t)
	t.Setenv("POD_EVICTION_MODE", "evict")
	t.Setenv("POD_FORCE", "false")
	t.Setenv("POD_FORCE_PROBLEM_PODS", "true")
	t.Setenv("POD_DELETE_AFTER_EVICTION", "false")
	t.Setenv("POD_PDB_TOKEN", "true")
	t.Setenv("POD_PDB_TOKEN_MAX_IN_FLIGHT", "1")
	t.Setenv("POD_MAX_CONCURRENT", "30")
	t.Setenv("POD_MAX_RETRIES", "3")
	t.Setenv("POD_RETRY_BACKOFF", "10s")
	t.Setenv("POD_DELETION_TIMEOUT", "2m")
	t.Setenv("POD_CHECK_INTERVAL", "20s")
	t.Setenv("POD_EVICTION_TIMEOUT", "10m")
	t.Setenv("POD_NODE_TERMINATION_TIMEOUT", "10m")
	t.Setenv("POD_NODE_TERMINATION_CHECK_TICK", "15s")
	t.Setenv("POD_POST_EVICTION_NODE_DELAY", "0s")

	if err := ValidateEvictionConfigEnv(); err != nil {
		t.Fatalf("ValidateEvictionConfigEnv() error = %v", err)
	}
}

func TestGetEvictionConfigFromEnvParsesExtendedTimeouts(t *testing.T) {
	clearEvictionConfigEnv(t)
	t.Setenv("POD_EVICTION_TIMEOUT", "3m")
	t.Setenv("POD_NODE_TERMINATION_TIMEOUT", "4m")
	t.Setenv("POD_NODE_TERMINATION_CHECK_TICK", "5s")
	t.Setenv("POD_POST_EVICTION_NODE_DELAY", "0s")

	cfg := GetEvictionConfigFromEnv()

	if cfg.EvictionTimeout != 3*time.Minute {
		t.Fatalf("EvictionTimeout = %s, want=3m", cfg.EvictionTimeout)
	}
	if cfg.NodeTerminationTimeout != 4*time.Minute {
		t.Fatalf("NodeTerminationTimeout = %s, want=4m", cfg.NodeTerminationTimeout)
	}
	if cfg.NodeTerminationCheckTick != 5*time.Second {
		t.Fatalf("NodeTerminationCheckTick = %s, want=5s", cfg.NodeTerminationCheckTick)
	}
	if cfg.PostEvictionNodeDelay != 0 {
		t.Fatalf("PostEvictionNodeDelay = %s, want=0", cfg.PostEvictionNodeDelay)
	}
}

func clearEvictionConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
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
}
