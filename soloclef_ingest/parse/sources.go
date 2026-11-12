package parse

import (
	"context"
	"io"
	"strings"
)

// StringSource is a Source over text already in memory: a note typed into
// the app, or a test's input.
func StringSource(name, content string) Source {
	return Source{
		Name: name,
		Size: int64(len(content)),
		Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(content)), nil
		},
	}
}
