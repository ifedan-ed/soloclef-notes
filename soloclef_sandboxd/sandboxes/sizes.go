package sandboxes

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseCPU reads a CPU limit in the spelling the settings use, which is
// Kubernetes' (and was the OpenSandbox server's): a number of CPUs ("2",
// "1.5") or thousandths of one ("500m"). It answers Docker's unit, billionths
// of a CPU.
func ParseCPU(text string) (int64, error) {
	text = strings.TrimSpace(text)
	scale := 1e9
	if strings.HasSuffix(text, "m") {
		text, scale = strings.TrimSuffix(text, "m"), 1e6
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || value <= 0 || math.IsInf(value, 0) {
		return 0, fmt.Errorf("a CPU limit is a positive number of CPUs (2, 1.5) or of thousandths (500m)")
	}
	nano := int64(value * scale)
	// Docker refuses less than a hundredth of a CPU.
	if nano < 1e7 {
		return 0, fmt.Errorf("a CPU limit must be at least 10m")
	}
	return nano, nil
}

// memoryUnits are the suffixes a memory limit may carry: binary units, as
// the settings have always been written ("6Gi"), and plain bytes.
var memoryUnits = []struct {
	suffix string
	bytes  int64
}{{"Ti", 1 << 40}, {"Gi", 1 << 30}, {"Mi", 1 << 20}, {"Ki", 1 << 10}}

// minimumMemory is Docker's own floor for a memory limit, raised to what the
// image needs to start a Python interpreter at all.
const minimumMemory = 64 << 20

// ParseMemory reads a memory limit ("6Gi", "512Mi", or bytes) as bytes.
func ParseMemory(text string) (int64, error) {
	text = strings.TrimSpace(text)
	unit := int64(1)
	for _, candidate := range memoryUnits {
		if strings.HasSuffix(text, candidate.suffix) {
			text, unit = strings.TrimSuffix(text, candidate.suffix), candidate.bytes
			break
		}
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value <= 0 || value > math.MaxInt64/unit {
		return 0, fmt.Errorf("a memory limit is a whole number with Ki, Mi, Gi or Ti (6Gi), or bytes")
	}
	if value*unit < minimumMemory {
		return 0, fmt.Errorf("a memory limit must be at least 64Mi")
	}
	return value * unit, nil
}
