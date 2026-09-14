package services

import (
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
)

func TestOnlineServicesPersistRejectInvalidAndReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	app := newTestApp(t, path, nil, zerolog.Nop())
	svc := NewSettingsService(app, nil)
	if err := svc.Update(`{"onlineServices":{"hubURL":" http://localhost:8094/api/hub/ ","registryURL":"http://127.0.0.1:8090"}}`); err != nil {
		t.Fatal(err)
	}
	_, saved, err := OpenSettingsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.OnlineServices.HubURL != "http://localhost:8094/api/hub" || saved.OnlineServices.RegistryURL != "http://127.0.0.1:8090" {
		t.Fatalf("saved: %+v", saved.OnlineServices)
	}
	if err := svc.Update(`{"onlineServices":{"hubURL":"https://user:password@example.com"}}`); err == nil {
		t.Fatal("accepted credentials in URL")
	}
	if got := svc.Get().OnlineServices; got != saved.OnlineServices {
		t.Fatalf("invalid update changed settings: %+v", got)
	}
	if err := svc.Update(`{"onlineServices":{"hubURL":"","registryURL":""}}`); err != nil {
		t.Fatal(err)
	}
	_, restored, err := OpenSettingsStore(path)
	if err != nil || restored.OnlineServices != (OnlineServiceSettings{}) {
		t.Fatalf("restore: %+v %v", restored, err)
	}
}
