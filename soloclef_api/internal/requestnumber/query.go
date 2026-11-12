package requestnumber

import "net/url"

// QueryValue is the value a query parameter carries, and whether it was named
// at all — which for a parameter with a default is the difference between the
// default and a 422.
//
// It exists because `url.Values.Get` is **not** how Starlette reads one. A
// query string may name a parameter twice, and a parameter typed as a single
// value then takes one of the two: Starlette builds `request.query_params`
// from an ordered list of pairs into a dict, so a later pair overwrites an
// earlier one and the **last** value wins. Go's `Get` returns the first. So
// `?hours=1&hours=2` was 2 on the Python tier and 1 here, which is a
// divergence on every parameter this tier reads out of a query — found while
// porting the prompts slice, whose list route takes one, and fixed for the log
// summary's `hours` in the same commit.
//
// Found twice over, independently: the folders port hit it on an id in that
// list's query (two tiers answering about two different rows, both 200) and
// wrote the identical function under another name. They were
// collapsed into this one at the merge — two helpers for one rule is the
// drift this package exists to prevent.
//
// Nothing here coerces: a value that is not a whole number is still Parse's
// 422, in the same words.
func QueryValue(query url.Values, name string) (string, bool) {
	values, named := query[name]
	if !named || len(values) == 0 {
		return "", false
	}
	return values[len(values)-1], true
}
