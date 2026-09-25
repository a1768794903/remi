package firmware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseVersion(t *testing.T) {
	got, ok := parseVersion("v1.2")
	if !ok || compare(got, []int{1, 2, 0}) != 0 {
		t.Fatalf("parseVersion = %#v, %v", got, ok)
	}
	if _, ok := parseVersion("garbage"); ok {
		t.Fatal("invalid version accepted")
	}
}

func TestMetadataAndExtract(t *testing.T) {
	x := release{TagName: "Omi_CV1_v1.2.3", PublishedAt: "2026-01-01", Body: "<!-- KEY_VALUE_START\nrelease_firmware_version: 1.2.3\nminimum_firmware_required: 1.0.0\nota_update_steps: a, b\nchangelog: one|two\nKEY_VALUE_END -->"}
	x.Assets = append(x.Assets, struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}{Name: "ota.zip", BrowserDownloadURL: "https://example/ota.zip"})
	out, err := extract("Omi CV 1", x)
	if err != nil || out.Version != "1.2.3" || len(out.OTAUpdateSteps) != 2 {
		t.Fatalf("extract = %#v, %v", out, err)
	}
}

func TestUnknownDevice(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v2/firmware/stable?device_model=unknown", nil)
	w := httptest.NewRecorder()
	(Handler{}).ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}
