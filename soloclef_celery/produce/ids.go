package produce

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

// NewID is the id Celery gives a task, in the one version Celery uses.
func NewID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("produce: generating an id: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(raw)
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:], nil
}

// Origin is the publisher Celery names in every envelope's origin header:
// gen<pid>@<hostname>, which is unique per replica without configuration.
func Origin() string {
	return fmt.Sprintf("gen%d@%s", os.Getpid(), hostname())
}

func hostname() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}
