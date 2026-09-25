package wrapped

import "testing"

func TestRouteYear(t *testing.T) {
	for _, path := range []string{"/v1/wrapped/2025", "/v1/wrapped/2025/generate"} {
		if got, err := routeYear(path); err != nil || got != 2025 {
			t.Fatalf("path=%s got=%d err=%v", path, got, err)
		}
	}
	if _, err := routeYear("/v1/wrapped/nope"); err == nil {
		t.Fatal("invalid year accepted")
	}
}
