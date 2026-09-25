package trends

import "testing"

func TestCleanTopicsSortsByMemoryCountAndDropsIDs(t *testing.T) {
	got := cleanTopics([]topic{{ID: "a", Topic: "Microsoft", MemoryIDs: []string{"1"}}, {ID: "b", Topic: "Nvidia", MemoryIDs: []string{"1", "2"}}, {ID: "c", Topic: "not-a-valid-trend", MemoryIDs: []string{"1", "2", "3"}}})
	if len(got) != 2 || got[0].Topic != "Nvidia" || got[0].MemoriesCount != 2 || got[1].MemoriesCount != 1 {
		t.Fatalf("unexpected cleaned topics: %#v", got)
	}
}

func TestTopicAllowlistMatchesPythonTrendOptionCount(t *testing.T) {
	if len(validTopics) != 172 {
		t.Fatalf("topic allowlist count=%d, Python source currently defines 172 valid options", len(validTopics))
	}
}
