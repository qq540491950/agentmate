package agentkit

import (
	"strings"
	"testing"
)

func TestValidatePlanStepsRejectsMultipleInProgressAndCycles(t *testing.T) {
	if err := validatePlanSteps([]todoItem{
		{ID: "a", Status: "in_progress"},
		{ID: "b", Status: "in_progress"},
	}); err == nil || !strings.Contains(err.Error(), "in_progress") {
		t.Fatalf("multiple in_progress = %v", err)
	}
	if err := validatePlanSteps([]todoItem{
		{ID: "a", DependsOn: []string{"b"}},
		{ID: "b", DependsOn: []string{"a"}},
	}); err == nil || !strings.Contains(err.Error(), "环") {
		t.Fatalf("cycle = %v", err)
	}
	if err := validatePlanSteps([]todoItem{
		{ID: "a", DependsOn: []string{"missing"}},
	}); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("missing dependency = %v", err)
	}
}
