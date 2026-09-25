package devkeys

import (
	"net/http"
	"testing"
)

func TestRequiredScopeCoversDeveloperMemoryAndActionItemRoutes(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   string
	}{
		{http.MethodGet, "/v1/dev/user/memories", "memories:read"},
		{http.MethodPost, "/v1/dev/user/memories", "memories:write"},
		{http.MethodGet, "/v1/dev/user/action-items", "action_items:read"},
		{http.MethodDelete, "/v1/dev/user/action-items/1", "action_items:write"},
	}
	for _, tc := range cases {
		if got := requiredScope(tc.method, tc.path); got != tc.want {
			t.Errorf("requiredScope(%q, %q) = %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
}
