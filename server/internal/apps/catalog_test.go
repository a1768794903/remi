package apps

import "testing"

func TestCatalogMetadataMatchesPythonSurface(t *testing.T) {
	categories := Categories()
	if len(categories) != 16 || categories[0].ID != "conversation-analysis" || categories[len(categories)-1].ID != "other" {
		t.Fatalf("unexpected categories: %#v", categories)
	}
	capabilities := Capabilities()
	if len(capabilities) != 4 {
		t.Fatalf("capability count = %d", len(capabilities))
	}
	if len(capabilities[2].Triggers) != 3 || len(capabilities[2].Actions) != 5 {
		t.Fatalf("external capability incomplete: %#v", capabilities[2])
	}
	if len(capabilities[3].Scopes) != 4 {
		t.Fatalf("notification scopes incomplete: %#v", capabilities[3])
	}
	if len(NotificationScopes()) != 4 || len(PaymentPlans()) != 1 {
		t.Fatal("static app metadata incomplete")
	}
}

func TestCatalogFilteringPaginationAndSort(t *testing.T) {
	first, second := 4.2, 3.1
	items := []App{{ID: "a", Name: "Alpha", Category: "productivity-and-organization", Capabilities: []string{"chat"}, Installs: 2, RatingAvg: &first}, {ID: "b", Name: "Beta", Category: "health-and-wellness", Capabilities: []string{"memories"}, Installs: 10, RatingAvg: &second}}
	filtered := filterCatalog(items, "productivity-and-organization", "chat")
	if len(filtered) != 1 || filtered[0].ID != "a" {
		t.Fatalf("filtered apps = %#v", filtered)
	}
	sortApps(items, "installs")
	if items[0].ID != "b" {
		t.Fatalf("install sort = %#v", items)
	}
	if len(sliceApps(items, 1, 20)) != 1 || len(sliceApps(items, 5, 20)) != 0 {
		t.Fatal("pagination bounds incorrect")
	}
}

func TestReviewScoreClampsToPythonRange(t *testing.T) {
	if clampScore(-1) != 0 || clampScore(6) != 5 || clampScore(3.5) != 3.5 {
		t.Fatal("review score clamp mismatch")
	}
}
