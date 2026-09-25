package account

import "testing"

func TestBuildCutoverControlDefaultsToLegacy(t *testing.T) {
	got := BuildCutoverControl(CutoverRecord{UID: "u"}, "ios", "1+0")
	if got.State != "legacy" || got.ClientAction != "none" || !got.LegacyWritesAllowed || !got.ProductTrafficAllowed {
		t.Fatalf("unexpected default control: %+v", got)
	}
}

func TestBuildCutoverControlEntersMaintenanceAndQuarantinesQueue(t *testing.T) {
	got := BuildCutoverControl(CutoverRecord{UID: "u", State: "migrating", AccountGeneration: 3}, "ios", "1+1")
	if got.ClientAction != "migration_maintenance" || got.OfflineQueueInstruction != "quarantine" || got.ProductTrafficAllowed {
		t.Fatalf("unexpected migrating control: %+v", got)
	}
}
