package chatfirst

import "testing"

func TestStableBlockIDIsDeterministicAndGenerationScoped(t *testing.T) {
	block := []byte(`{"type":"taskCard","task_id":"task-1"}`)
	a := stableBlockID("uid-1", 7, block)
	if a == "" || a != stableBlockID("uid-1", 7, block) {
		t.Fatalf("unstable block id: %q", a)
	}
	if a == stableBlockID("uid-1", 8, block) {
		t.Fatal("block id must include account generation")
	}
}

func TestValidateBlockRequestRejectsCrossAccountOwnerFence(t *testing.T) {
	result := validateRequest("uid-1", validationRequest{SourceSurface: "main_chat", OwnerFence: "uid-2", ControlGeneration: 0, Blocks: []block{{Type: "taskCard", ID: "task-1"}}})
	if result.Code != "capability_unavailable" || result.Accepted {
		t.Fatalf("result = %+v", result)
	}
}

func TestJSONEquivalentIgnoresObjectKeyOrder(t *testing.T) {
	if !jsonEquivalent([]byte(`{"subject":{"id":"x","kind":"task"}}`), []byte(`{"subject":{"kind":"task","id":"x"}}`)) {
		t.Fatal("equivalent JSON was treated as different")
	}
	if jsonEquivalent([]byte(`{"id":"x"}`), []byte(`{"id":"y"}`)) {
		t.Fatal("different JSON was treated as equivalent")
	}
}
