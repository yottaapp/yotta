package desktopapp

import (
	"strings"

	"github.com/yottaapp/yotta/internal/serviceconfig"
	"github.com/yottaapp/yotta/internal/services"
)

func withServiceSettings(config Config, settings services.OnlineServiceSettings) Config {
	if settings.HubURL != "" {
		config.HubURL = settings.HubURL
		config.HubAllowLoopbackHTTP = strings.HasPrefix(settings.HubURL, "http://")
	}
	if settings.RegistryURL != "" {
		config.RegistryURL = settings.RegistryURL
		config.RegistryAllowLoopbackHTTP = strings.HasPrefix(settings.RegistryURL, "http://")
	}
	if config.HubURL == "" {
		config.HubURL = serviceconfig.DefaultHubURL
	}
	if config.RegistryURL == "" {
		config.RegistryURL = serviceconfig.DefaultRegistryURL
	}
	return config
}

// A custom service pair must not reuse the official services' cached session.
func serviceCredentialScope(scope string, config Config) string {
	if strings.TrimRight(config.HubURL, "/") == serviceconfig.DefaultHubURL && strings.TrimRight(config.RegistryURL, "/") == serviceconfig.DefaultRegistryURL {
		return scope
	}
	return scope + "\x00services\x00" + config.HubURL + "\x00" + config.RegistryURL
}
