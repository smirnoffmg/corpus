package api

import (
	"net"
	"net/http"
	"slices"
	"strings"
)

// Guard keeps other web sites out of a server that has no authentication and
// holds a personal diary. Listening on 127.0.0.1 is not enough on its own: a
// page open in the browser can repoint its own domain at 127.0.0.1 and read
// the answers (DNS rebinding), and a form on it can post an upload without the
// browser asking anyone (CSRF).
//
// Against the first, the Host header must name this machine — a rebound
// request still carries the attacker's domain. Against the second, writes
// from another origin are refused; requests without Origin or Sec-Fetch-Site,
// which is how curl and MCP clients arrive, are let through.
func Guard(next http.Handler, hosts []string) http.Handler {
	protect := http.NewCrossOriginProtection()
	protected := protect.Handler(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(hosts, hostname(r.Host)) {
			http.Error(w, "Forbidden: this server answers only to "+strings.Join(hosts, ", "), http.StatusForbidden)
			return
		}
		protected.ServeHTTP(w, r)
	})
}

func hostname(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = strings.Trim(hostport, "[]")
	}
	return strings.ToLower(host)
}
