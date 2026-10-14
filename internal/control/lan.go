package control

import (
	"net"
	"net/http"
	"strings"
)

// LANHandler exposes the panel to private IPv4 clients while preserving the
// loopback boundary of the internal administration and its origin checks.
func LANHandler(panel http.Handler, loopbackAddress string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !privateEndpoint(r.RemoteAddr) || !privateEndpoint(r.Host) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Panneau accessible uniquement sur le réseau local."})
			return
		}
		origin := strings.TrimSuffix(r.Header.Get("Origin"), "/")
		if (origin != "" && origin != "http://"+r.Host) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Origin rejected."})
			return
		}
		// Clone before translating the validated external origin for the existing
		// panel and API. Cookies and CSRF tokens retain their normal checks.
		local := r.Clone(r.Context())
		local.Host = loopbackAddress
		if origin != "" {
			local.Header.Set("Origin", "http://"+loopbackAddress)
		}
		panel.ServeHTTP(w, local)
	})
}

func privateEndpoint(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() != nil && (ip.IsPrivate() || ip.IsLoopback())
}
