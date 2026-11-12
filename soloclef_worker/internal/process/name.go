package process

import (
	"os"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

// WorkerName is what every result row says ran the job, as Celery's hostname
// does. Unset it is this container's host, which is unique per replica and
// needs no configuration.
func WorkerName(getenv func(string) string) string {
	if given := strings.TrimSpace(getenv(broker.EnvName)); given != "" {
		return given
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return "go-worker@" + host
}
