package installedbuilds

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

// An Exception is one documented way the server's answer to a bundle
// differs from its snapshot, accepted on purpose (exceptions.json beside the
// snapshot). There are two kinds, and nothing else is ever excused:
//
//   - Fields: response fields the snapshot requires that the server no
//     longer sends, because the bundle never reads them at run time.
//     ReadsChecked cites where that was checked: every place the bundle's
//     source names the field, and that it has no run-time check of answers
//     (the bundles published on 2026-11-14 and later check every answer, so
//     a field exception is never true of them).
//   - Status: an answer the bundle gets instead of the one it needs, because
//     the bundle itself is wrong and an over-the-air update fixes it.
//     FixedBy names the commit of the update that does; a phone on this
//     bundle gets the fix at its next start once that update is published.
//
// The contract test fails on an exception it never used, so one goes as
// soon as the server or the bundle stops needing it.
type Exception struct {
	Operation    string   `json:"operation"`
	Fields       []string `json:"fields,omitempty"`
	Status       int      `json:"status,omitempty"`
	Why          string   `json:"why"`
	ReadsChecked []string `json:"reads_checked,omitempty"`
	FixedBy      string   `json:"fixed_by,omitempty"`
}

// ExceptionID names one exception in a failure and in the used set.
func ExceptionID(build Build, index int) string {
	return fmt.Sprintf("snapshots/%s/exceptions.json #%d (%s)", build.Dir, index+1, build.Exceptions[index].Operation)
}

func loadExceptions(dir string, calls []Call) ([]Exception, error) {
	raw, err := snapshots.ReadFile(path.Join("snapshots", dir, "exceptions.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var exceptions []Exception
	if err := json.Unmarshal(raw, &exceptions); err != nil {
		return nil, fmt.Errorf("exceptions.json: %w", err)
	}
	called := map[string]bool{}
	for _, call := range calls {
		called[Shape(call.Operation())] = true
	}
	for index, exception := range exceptions {
		where := fmt.Sprintf("exceptions.json #%d (%s)", index+1, exception.Operation)
		switch {
		case !called[Shape(exception.Operation)]:
			return nil, fmt.Errorf("%s: the bundle does not make this call", where)
		case exception.Why == "":
			return nil, fmt.Errorf("%s: says no why", where)
		case len(exception.Fields) > 0 && exception.Status != 0:
			return nil, fmt.Errorf("%s: is both a field and a status exception; write two", where)
		case len(exception.Fields) > 0 && len(exception.ReadsChecked) == 0:
			return nil, fmt.Errorf("%s: excuses fields without citing the reads checked", where)
		case exception.Status != 0 && exception.FixedBy == "":
			return nil, fmt.Errorf("%s: excuses a status without naming the update that fixes the bundle", where)
		case len(exception.Fields) == 0 && exception.Status == 0:
			return nil, fmt.Errorf("%s: excuses nothing", where)
		}
	}
	return exceptions, nil
}

// position is a list's index in a problem's path: "[3]." reads as "[].".
var position = regexp.MustCompile(`\[\d+\]`)

// excusedField is the index of the exception that excuses a missing field
// at a path of a call's answer, or -1.
func (b Build) excusedField(call Call, at string) int {
	at = strings.TrimPrefix(position.ReplaceAllString(at, "[]"), "[].")
	for index, exception := range b.Exceptions {
		if Shape(exception.Operation) != Shape(call.Operation()) {
			continue
		}
		for _, field := range exception.Fields {
			if field == at {
				return index
			}
		}
	}
	return -1
}

// ExcusedStatus is the index of the exception that excuses this status for
// a call, or -1.
func (b Build) ExcusedStatus(operation string, status int) int {
	for index, exception := range b.Exceptions {
		if exception.Status == status && Shape(exception.Operation) == Shape(operation) {
			return index
		}
	}
	return -1
}
