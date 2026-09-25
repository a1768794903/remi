package jit

import "testing"

func TestDecisionDefaultsToDisabledWhenAuthorityIsNotConfigured(t *testing.T) {
	t.Setenv("JIT_ROLLOUT_ENABLED", "")
	t.Setenv("JIT_KILL_SWITCH_ENABLED", "")
	decision := resolveDecision("ordinary-user")
	if decision.Effective != "disabled" || decision.Rollout != "disabled" || decision.KillSwitch != "disabled" {
		t.Fatalf("decision = %+v", decision)
	}
	if decision.Reason != "configuration_missing" {
		t.Fatalf("reason = %q", decision.Reason)
	}
}

func TestKillSwitchOverridesConfiguredRollout(t *testing.T) {
	t.Setenv("JIT_ROLLOUT_ENABLED", "true")
	t.Setenv("JIT_KILL_SWITCH_ENABLED", "true")
	decision := resolveDecision("ordinary-user")
	if decision.Effective != "disabled" || decision.KillSwitch != "enabled" || decision.Reason != "kill_switch_enabled" {
		t.Fatalf("decision = %+v", decision)
	}
}
