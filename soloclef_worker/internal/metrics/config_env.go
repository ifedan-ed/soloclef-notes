package metrics

import "strings"

// EnvAddr is where the scrape is served. Unset it is DefaultAddr, because a
// tier nobody can see is the thing phase 5 exists to end; the one value that
// turns it off is spelled "off".
const EnvAddr = "SOLOCLEF_WORKER_METRICS_ADDR"

// DefaultAddr matches this host's convention: its other exporters serve
// /metrics, and its Go services serve it on 9464 (quiz-go-api-1,
// tools-search-1 in monitoring/prometheus/prometheus.yml).
const DefaultAddr = ":9464"

// Off is the one value that stops the server being started.
const Off = "off"

// LoadAddr reads the address to serve on, or "" for no server at all.
func LoadAddr(getenv func(string) string) string {
	switch addr := strings.TrimSpace(getenv(EnvAddr)); addr {
	case "":
		return DefaultAddr
	case Off:
		return ""
	default:
		return addr
	}
}
