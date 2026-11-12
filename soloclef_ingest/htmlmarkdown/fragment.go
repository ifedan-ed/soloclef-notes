package htmlmarkdown

import (
	"strings"
	"sync"
)

// shared is built on first use: Fragment has no Parser to carry one, and a
// converter is safe to share between goroutines.
var shared = sync.OnceValue(newConverter)

// Fragment converts a piece of HTML already in memory, such as the table a
// hosted parser returns as HTML, with the same rules a file gets.
func Fragment(html string) (string, error) {
	markdown, err := shared().ConvertString(html)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(markdown), nil
}
