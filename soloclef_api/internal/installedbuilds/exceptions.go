package installedbuilds

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
)

// An Exception is the one documented way the server's answer to a bundle
// may differ from its snapshot (exceptions.json beside the snapshot): a
// status the bundle gets instead of the one it needs, because the bundle
// itself sends something wrong and an over-the-air update fixes it. FixedBy
// names the commit of that update; a phone on this bundle gets the fix at
// its next start once the update is published.
//
// A missing response field is never excused: the installed bundles check
// every answer against their openapi.json at run time, so a required field
// they do not get is a broken screen (CheckAnswer).
//
// The contract test fails on an exception it never used, so one goes as
// soon as the server or the bundle stops needing it.
type Exception struct {
	Operation string `json:"operation"`
	Status    int    `json:"status"`
	Why       string `json:"why"`
	FixedBy   string `json:"fixed_by"`
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
	// An exceptions.json written for the field excuses that are gone (a
	// "fields" key) must fail here rather than load as an empty excuse.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&exceptions); err != nil {
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
		case exception.Status == 0:
			return nil, fmt.Errorf("%s: excuses no status", where)
		case exception.FixedBy == "":
			return nil, fmt.Errorf("%s: excuses a status without naming the update that fixes the bundle", where)
		}
	}
	return exceptions, nil
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
