package node

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseStepRules(t *testing.T) {
	rules, err := parseStepRules("80:1,60:2")
	assert.NoError(t, err)
	assert.Equal(t, []StepRule{
		{MaxAllocateRate: 60, DrainCount: 2},
		{MaxAllocateRate: 80, DrainCount: 1},
	}, rules)
}

func TestValidateDrainPolicyEnvRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
		want string
	}{
		{name: "invalid policy", key: "DRAIN_POLICY", val: "bad", want: "DRAIN_POLICY"},
		{name: "negative min", key: "DRAIN_MIN", val: "-1", want: "DRAIN_MIN"},
		{name: "fraction above one", key: "DRAIN_MAX_FRACTION", val: "1.5", want: "DRAIN_MAX_FRACTION"},
		{name: "invalid bool", key: "DRAIN_SAFETY_FAIL_CLOSED", val: "maybe", want: "DRAIN_SAFETY_FAIL_CLOSED"},
		{name: "invalid progressive bool", key: "DRAIN_PROGRESSIVE", val: "maybe", want: "DRAIN_PROGRESSIVE"},
		{name: "invalid step rule", key: "DRAIN_STEP_RULES", val: "80:-1", want: "DRAIN_STEP_RULES"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearDrainPolicyEnv(t)
			t.Setenv(tt.key, tt.val)

			err := ValidateDrainPolicyEnv()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q missing %q", err.Error(), tt.want)
			}
		})
	}
}

func TestValidateDrainPolicyEnvAcceptsValidValues(t *testing.T) {
	clearDrainPolicyEnv(t)
	t.Setenv("DRAIN_POLICY", "step")
	t.Setenv("DRAIN_ROUNDING", "ceil")
	t.Setenv("DRAIN_MIN", "1")
	t.Setenv("DRAIN_MAX_ABSOLUTE", "2")
	t.Setenv("DRAIN_MAX_FRACTION", "0.2")
	t.Setenv("DRAIN_SAFETY_MAX_ALLOCATE_RATE", "90")
	t.Setenv("DRAIN_SAFETY_FAIL_CLOSED", "true")
	t.Setenv("DRAIN_PROGRESSIVE", "true")
	t.Setenv("DRAIN_STEP_RULES", "60:2,80:1")

	if err := ValidateDrainPolicyEnv(); err != nil {
		t.Fatalf("ValidateDrainPolicyEnv() error = %v", err)
	}
}

func TestCalculateDrainNodeCount_Formula_Floor_DefaultBehavior(t *testing.T) {
	opts := DrainPolicyOptions{
		Policy:   DrainPolicyFormula,
		Rounding: DrainRoundingFloor,
		MinDrain: 0,
	}

	// lenNodes=8, max=63 => drainRate=(99-63)/100=0.36 => floor(2.88)=2
	assert.Equal(t, 2, CalculateDrainNodeCount(8, 63, opts))

	// lenNodes=8, max=90 => 0.09 => floor(0.72)=0
	assert.Equal(t, 0, CalculateDrainNodeCount(8, 90, opts))
}

func TestCalculateDrainNodeCount_Formula_Round_MinDrain(t *testing.T) {
	opts := DrainPolicyOptions{
		Policy:   DrainPolicyFormula,
		Rounding: DrainRoundingFloor,
		MinDrain: 1,
	}

	// lenNodes=8, max=90 => raw=0.72, floor=0 이지만 drainRate>0 + minDrain=1 => 1로 보정
	assert.Equal(t, 1, CalculateDrainNodeCount(8, 90, opts))
}

func TestCalculateDrainNodeCount_Caps(t *testing.T) {
	opts := DrainPolicyOptions{
		Policy:           DrainPolicyFormula,
		Rounding:         DrainRoundingCeil,
		MinDrain:         1,
		MaxDrainAbsolute: 2,
		MaxDrainFraction: 0.2, // 8대면 floor(1.6)=1
	}

	// max=20 => drainRate=0.79 => raw=6.32 => ceil=7, min=1 => 7, capAbs=2/capFrac=1 => 1
	assert.Equal(t, 1, CalculateDrainNodeCount(8, 20, opts))
}

func TestCalculateDrainNodeCount_MaxFractionCapDoesNotRoundUp(t *testing.T) {
	opts := DrainPolicyOptions{
		Policy:           DrainPolicyFormula,
		Rounding:         DrainRoundingCeil,
		MinDrain:         0,
		MaxDrainFraction: 0.01,
	}

	assert.Equal(t, 0, CalculateDrainNodeCount(23, 20, opts))
}

func TestCalculateDrainNodeCount_StepPolicy(t *testing.T) {
	opts := DrainPolicyOptions{
		Policy:    DrainPolicyStep,
		StepRules: []StepRule{{MaxAllocateRate: 60, DrainCount: 2}, {MaxAllocateRate: 80, DrainCount: 1}},
	}

	assert.Equal(t, 2, CalculateDrainNodeCount(10, 55, opts))
	assert.Equal(t, 1, CalculateDrainNodeCount(10, 75, opts))
	assert.Equal(t, 0, CalculateDrainNodeCount(10, 90, opts))
}

func TestShouldBlockDrainBySafetyMaxAllocateRate(t *testing.T) {
	opts := DrainPolicyOptions{
		SafetyMaxAllocateRate: 90,
	}

	blocked, reason, err := ShouldBlockDrainBySafetyConditions(90, opts)
	assert.NoError(t, err)
	assert.True(t, blocked)
	assert.Contains(t, reason, ">= safetyMaxAllocateRate")
}

func clearDrainPolicyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"DRAIN_POLICY",
		"DRAIN_ROUNDING",
		"DRAIN_MIN",
		"DRAIN_MAX_ABSOLUTE",
		"DRAIN_MAX_FRACTION",
		"DRAIN_SAFETY_MAX_ALLOCATE_RATE",
		"DRAIN_SAFETY_FAIL_CLOSED",
		"DRAIN_PROGRESSIVE",
		"DRAIN_STEP_RULES",
	} {
		t.Setenv(key, "")
	}
}
