package actionitems

import "testing"

func TestTaskShareKeyIsNamespaced(t *testing.T) {
	if taskShareKey("abc") != "task_share:abc" || taskAcceptedKey("abc") != "task_share:abc:accepted" {
		t.Fatal("unexpected share keys")
	}
}
