package main

import "testing"

// servedRoutes() is read by the limiter's matcher, the analytics templates and
// the path guard, so a duplicate is a route counted twice: refused here rather
// than found as a doubled rate limit.
func TestServedRoutesHoldEachRouteOnce(t *testing.T) {
	answered := set(servedRoutes())
	if len(answered) == len(servedRoutes()) {
		return
	}
	seen := map[string]bool{}
	var twice []string
	for _, route := range servedRoutes() {
		if seen[route] {
			twice = append(twice, route)
		}
		seen[route] = true
	}
	t.Errorf("servedRoutes() holds %d entries but only %d distinct ones; listed twice: %q",
		len(servedRoutes()), len(answered), twice)
}

func set(routes []string) map[string]struct{} {
	unique := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		unique[route] = struct{}{}
	}
	return unique
}
