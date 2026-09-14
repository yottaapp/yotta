package serviceconfig

import "testing"

func TestServiceEndpointValidation(t *testing.T) {
	for _, value := range []string{"", DefaultHubURL, "http://localhost:8094/api/hub", "http://127.0.0.1:8090", "http://[::1]:8090"} {
		if _, err := NormalizeEndpoint(value); err != nil {
			t.Errorf("%q: %v", value, err)
		}
	}
	for _, value := range []string{"/api/hub", "http://example.com", "https://u:p@example.com", "https://example.com?a=b", "https://example.com?", "https://example.com/#x", "ftp://example.com", "http://localhost:65536", "https://:443"} {
		if _, err := NormalizeEndpoint(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	if value, err := NormalizeEndpoint(" https://EXAMPLE.com/api/hub/ "); err != nil || value != "https://example.com/api/hub" {
		t.Fatalf("normalized %q: %v", value, err)
	}
}
