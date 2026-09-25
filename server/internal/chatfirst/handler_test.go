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
	result := validateRequest("uid-1", validationRequest{SourceSurface: "main_chat", OwnerFence: "uid-2", ControlGeneration: 0, Blocks: []map[string]any{{"type": "taskCard", "task_id": "task-1"}}})
	if result.Code != "capability_unavailable" || result.Accepted {
		t.Fatalf("result = %+v", result)
	}
}

func TestBlockIdentitySupportsReleasedChatFirstUnion(t *testing.T) {
	cases := []struct {
		kind  string
		block map[string]any
	}{
		{"taskCard", map[string]any{"type": "taskCard", "task_id": "task-1"}},
		{"goalLink", map[string]any{"type": "goalLink", "goal_id": "goal-1"}},
		{"captureLink", map[string]any{"type": "captureLink", "conversation_id": "conv-1"}},
		{"conversationLink", map[string]any{"type": "conversationLink", "conversation_id": "conv-2"}},
		{"memoryLink", map[string]any{"type": "memoryLink", "memory_id": "memory-1"}},
		{"questionCard", map[string]any{"type": "questionCard", "question_id": "question-1"}},
	}
	for _, item := range cases {
		if got := blockIdentity(item.kind, item.block); got == "" {
			t.Errorf("%s identity is empty", item.kind)
		}
	}
}

func TestRequiresEntityCheckForBlocksWithIdentity(t *testing.T) {
	if !requiresEntityCheck(map[string]any{"type": "taskCard", "task_id": "task-1"}) {
		t.Fatal("task block should require entity ownership check")
	}
	if requiresEntityCheck(map[string]any{"type": "questionCard", "question_id": "q-1"}) {
		t.Fatal("question block itself has no direct entity lookup")
	}
}

func TestQuestionSubjectKindsAreRecognized(t *testing.T) {
	for _, kind := range []string{"task", "goal", "capture"} {
		if !subjectKindSupported(kind) {
			t.Errorf("subject kind %q was rejected", kind)
		}
	}
	if subjectKindSupported("cold_start") {
		t.Fatal("cold-start subject must not be admitted through generic validation")
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

func TestReraisedIntentIDIsStableForDeferral(t *testing.T) {
	a := reraisedIntentID("user-1", 3, "def-1")
	if a == "" || a != reraisedIntentID("user-1", 3, "def-1") {
		t.Fatalf("unstable reraised id: %q", a)
	}
	if a == reraisedIntentID("user-1", 4, "def-1") {
		t.Fatal("reraised id must include generation")
	}
}
