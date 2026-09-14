package desktopapp

import (
	"testing"

	"github.com/yottaapp/yotta/internal/serviceconfig"
	"github.com/yottaapp/yotta/internal/services"
)

func TestServiceSettingsApplyOnStartup(t *testing.T) {
	base := Config{HubURL: serviceconfig.DefaultHubURL, RegistryURL: serviceconfig.DefaultRegistryURL, AccountURL: "https://account.yuelili.com"}
	local := withServiceSettings(base, services.OnlineServiceSettings{HubURL: "http://localhost:8094", RegistryURL: "http://127.0.0.1:8090"})
	if local.HubURL != "http://localhost:8094" || local.RegistryURL != "http://127.0.0.1:8090" || !local.HubAllowLoopbackHTTP || !local.RegistryAllowLoopbackHTTP || local.AccountURL != base.AccountURL {
		t.Fatalf("local: %+v", local)
	}
	if serviceCredentialScope("profile", local) == serviceCredentialScope("profile", base) {
		t.Fatal("custom services reused official credentials")
	}
	other := local
	other.HubURL = "http://localhost:9094"
	if serviceCredentialScope("profile", local) == serviceCredentialScope("profile", other) {
		t.Fatal("different service pairs share credentials")
	}
	reset := withServiceSettings(base, services.OnlineServiceSettings{})
	if reset.HubURL != base.HubURL || reset.RegistryURL != base.RegistryURL || serviceCredentialScope("profile", reset) != "profile" {
		t.Fatal("default service restore failed")
	}
	https := withServiceSettings(local, services.OnlineServiceSettings{HubURL: base.HubURL, RegistryURL: base.RegistryURL})
	if https.HubAllowLoopbackHTTP || https.RegistryAllowLoopbackHTTP {
		t.Fatal("HTTP flag survived HTTPS override")
	}
}
