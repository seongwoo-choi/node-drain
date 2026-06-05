package pod

import (
	"strings"
	"testing"
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

	if err := ValidateEvictionConfigEnv(); err != nil {
		t.Fatalf("ValidateEvictionConfigEnv() error = %v", err)
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
	} {
		t.Setenv(key, "")
	}
}
