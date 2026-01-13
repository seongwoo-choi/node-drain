package node

import (
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
		MaxDrainFraction: 0.2, // 8대면 ceil(1.6)=2
	}

	// max=20 => drainRate=0.79 => raw=6.32 => ceil=7, min=1 => 7, capAbs=2/capFrac=2 => 2
	assert.Equal(t, 2, CalculateDrainNodeCount(8, 20, opts))
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
