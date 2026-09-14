package serviceconfig

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

const DefaultHubURL = "https://yotta.yuelili.com/api/hub"
const DefaultWalletURL = "https://pay.yuelili.com/wallet"

const DefaultRegistryURL = "https://yotta.yuelili.com/api/registry"

// NormalizeEndpoint accepts a service base URL, including loopback development
// servers. Empty values select the app's configured default.
func NormalizeEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("service address must be an absolute base URL without credentials, query or fragment")
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")) {
		return "", errors.New("service address requires HTTPS or loopback HTTP")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("service address has an invalid port")
		}
	}
	u.Host = strings.ToLower(u.Host)
	return strings.TrimRight(u.String(), "/"), nil
}
