// Package installedbuilds holds what the phone app builds already installed
// from the App Store (and the Android preview) expect of this server, so
// that a server change that would break one fails a test instead of a
// phone.
//
// Each installed build has a snapshot (snapshots/<build>/): the openapi.json
// its client was typed from, byte for byte, and the routes its source calls
// with the query values it sends (calls.json, made by takesnapshot/). The
// server's tests in cmd/api hold the server to them: every route a build
// calls is served, and the contract test sends each request a build makes
// through the real server and a real Postgres and checks the answer against
// the snapshot (README.md says how, and when a snapshot may go).
//
// Code the server keeps only for an installed build is registered here
// (compat.go), so removing a snapshot shows exactly what can be deleted.
//
// Test support only: nothing the server runs imports this package.
package installedbuilds

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed snapshots
var snapshots embed.FS

// A Build is one installed phone build and the contract it was compiled
// against.
type Build struct {
	// Dir is the snapshot's directory name, its id ("ios-1.1.0-build4").
	Dir string `json:"-"`
	// Name is how a person names it: "iPhone 1.1.0 (App Store build 4)".
	Name       string `json:"build"`
	Platform   string `json:"platform"`
	AppVersion string `json:"app_version"`
	StoreBuild string `json:"store_build"`
	// UpdateChannel is the EAS channel its over-the-air updates come on.
	UpdateChannel string `json:"update_channel"`
	// SourceCommit is the commit its running update was published from;
	// SourceBranch is the branch its next update will come from.
	SourceCommit string `json:"source_commit"`
	SourceBranch string `json:"source_branch"`
	// ReportsAppVersion says whether the bundle sends `app_version`,
	// `update_id` and `runtime_version` when it registers for push
	// (push_devices); bundles before the updates of 2026-11-14 send none,
	// and are counted together as "an older bundle" (README.md).
	ReportsAppVersion bool `json:"reports_app_version"`
	// Published says whether the update was published: a snapshot of an
	// update not yet published holds the server to it before it ships.
	Published *bool `json:"published"`
	// UpdateIDs are expo-updates' ids of the published update, one per
	// platform it went to, which a phone running it reports
	// (push_devices.update_id). Filled in after publishing; empty for a
	// bundle that does not report one.
	UpdateIDs []string `json:"update_ids"`
	// OpenAPISHA256 is the sha256 the app's generated types were made from
	// (its src/lib/api/openapi.sha256), which openapi.json must match.
	OpenAPISHA256 string `json:"openapi_sha256"`
	Taken         string `json:"taken"`

	Calls []Call `json:"-"`
	// Exceptions is the snapshot's documented exceptions (exceptions.go).
	Exceptions []Exception `json:"-"`
	spec       map[string]any
}

// A Call is one operation a build's source calls.
type Call struct {
	Method string `json:"method"`
	// Path is the snapshot's template ("/api/v1/notebooks/{notebook_id}"),
	// or the path as the app wrote it when the snapshot has none.
	Path string `json:"path"`
	// InSnapshot is false for a route the build calls that its openapi.json
	// does not describe (the presence beat): it must still be served, and
	// there is no schema to check its answer against.
	InSnapshot bool `json:"in_snapshot"`
	// Sites are where the app calls it, as file:line under soloclef_mobile/src.
	Sites []string `json:"sites"`
	// Query is each query parameter the build sends, with the values it can
	// take where its source pins them; nil means any value.
	Query map[string][]string `json:"query"`
}

// Operation is the call as servedRoutes() names a route: "GET /api/v1/…".
func (c Call) Operation() string { return c.Method + " " + c.Path }

// Label names the build in a failure: which phones break.
func (b Build) Label() string {
	what := "running the update from"
	if !*b.Published {
		what = "the update not yet published from"
	}
	return fmt.Sprintf("%s, %s %s (snapshots/%s)", b.Name, what, short(b.SourceCommit), b.Dir)
}

func short(commit string) string {
	if len(commit) > 9 {
		return commit[:9]
	}
	return commit
}

// All reads every snapshot, refusing one whose openapi.json is not the one
// its build was typed from or whose files do not agree.
func All() ([]Build, error) {
	entries, err := fs.ReadDir(snapshots, "snapshots")
	if err != nil {
		return nil, err
	}
	var builds []Build
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		build, err := load(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("snapshots/%s: %w", entry.Name(), err)
		}
		builds = append(builds, build)
	}
	if len(builds) == 0 {
		return nil, fmt.Errorf("no snapshots: a server with no installed builds to keep is a decision, not an empty directory")
	}
	sort.Slice(builds, func(i, j int) bool { return builds[i].Dir < builds[j].Dir })
	return builds, nil
}

func load(dir string) (Build, error) {
	read := func(name string) ([]byte, error) { return snapshots.ReadFile(path.Join("snapshots", dir, name)) }
	var build Build
	raw, err := read("build.json")
	if err != nil {
		return build, err
	}
	if err := json.Unmarshal(raw, &build); err != nil {
		return build, fmt.Errorf("build.json: %w", err)
	}
	build.Dir = dir
	if build.Published == nil {
		return build, fmt.Errorf("build.json does not say whether the update is published (\"published\": true or false)")
	}
	if *build.Published && build.ReportsAppVersion && len(build.UpdateIDs) == 0 {
		return build, fmt.Errorf("build.json: a published update that reports its id needs update_ids, read from its publication, so app-versions can count it")
	}
	for name, value := range map[string]string{"build": build.Name, "platform": build.Platform, "app_version": build.AppVersion,
		"source_commit": build.SourceCommit, "openapi_sha256": build.OpenAPISHA256} {
		if value == "" {
			return build, fmt.Errorf("build.json names no %s", name)
		}
	}
	document, err := read("openapi.json")
	if err != nil {
		return build, err
	}
	sum := sha256.Sum256(document)
	if got := hex.EncodeToString(sum[:]); got != build.OpenAPISHA256 {
		return build, fmt.Errorf("openapi.json is %s, but the build was typed from %s: retake the snapshot (takesnapshot/take-snapshot.sh), never edit it", got, build.OpenAPISHA256)
	}
	if err := json.Unmarshal(document, &build.spec); err != nil {
		return build, fmt.Errorf("openapi.json: %w", err)
	}
	calls, err := read("calls.json")
	if err != nil {
		return build, err
	}
	if err := json.Unmarshal(calls, &build.Calls); err != nil {
		return build, fmt.Errorf("calls.json: %w", err)
	}
	if build.Exceptions, err = loadExceptions(dir, build.Calls); err != nil {
		return build, err
	}
	for _, call := range build.Calls {
		if call.InSnapshot != (build.operation(call) != nil) {
			return build, fmt.Errorf("calls.json says %s is%s in openapi.json, and it is%s", call.Operation(),
				map[bool]string{true: "", false: " not"}[call.InSnapshot], map[bool]string{true: "", false: " not"}[build.operation(call) != nil])
		}
	}
	return build, nil
}

// operation is the snapshot's description of a call, or nil.
func (b Build) operation(call Call) map[string]any {
	paths, _ := b.spec["paths"].(map[string]any)
	item, _ := paths[call.Path].(map[string]any)
	op, _ := item[strings.ToLower(call.Method)].(map[string]any)
	return op
}

// SuccessStatuses is the success answers the snapshot declares for a call
// (200, 201, 204, …), in order.
func (b Build) SuccessStatuses(call Call) []string {
	responses, _ := b.operation(call)["responses"].(map[string]any)
	var out []string
	for status := range responses {
		if strings.HasPrefix(status, "2") {
			out = append(out, status)
		}
	}
	sort.Strings(out)
	return out
}

// responseSchema is the JSON schema the snapshot gives an answer of this
// status, or nil when it declares none (no body, or not JSON).
func (b Build) responseSchema(call Call, status string) schema {
	responses, _ := b.operation(call)["responses"].(map[string]any)
	response, _ := responses[status].(map[string]any)
	content, _ := response["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	return asSchema(media["schema"])
}

// CheckAnswer says how an answer of this status would break the build, or
// nothing when the build reads it as it was compiled to, and which of the
// build's exceptions excused a missing field (by index). A status the
// snapshot does not declare as a success is not checked here: the caller
// decides which statuses a case expects.
func (b Build) CheckAnswer(call Call, status int, body []byte) (problems []string, excused []int) {
	node := b.responseSchema(call, fmt.Sprint(status))
	// A success the snapshot does not declare (201 where it says 200) is
	// still a success to the build, whose client reads any 2xx alike: its
	// body is read against the success the snapshot does declare.
	if node == nil && status >= 200 && status <= 299 {
		for _, declared := range b.SuccessStatuses(call) {
			if node = b.responseSchema(call, declared); node != nil {
				break
			}
		}
	}
	// No schema, or the empty one FastAPI gives a route it could not
	// describe (a stream, a file): there is nothing the build was compiled
	// to read in it.
	if len(node) == 0 {
		return nil, nil
	}
	value, err := decode(body)
	if err != nil {
		return []string{fmt.Sprintf("the answer is not JSON (%v); the build parses it as JSON", err)}, nil
	}
	components, _ := b.spec["components"].(map[string]any)
	check := compatibility{components: components}
	check.check("", value, node)

	for _, found := range check.found {
		// A missing field this bundle never reads is excused by its
		// snapshot's exceptions.json, and said to have been used.
		if found.problem == missingProblem {
			if index := b.excusedField(call, found.at); index >= 0 {
				excused = append(excused, index)
				continue
			}
		}
		problems = append(problems, found.String())
	}
	return problems, excused
}

// RequiresField says whether the snapshot's success answer for a call
// requires a field, by its dotted path ("announcements.total_unread"; a
// list's items are stepped into): the field a build breaks without. The
// compatibility registry uses it to tell whether a kept field is still
// needed; a field the snapshot only declares as optional is not.
func (b Build) RequiresField(call Call, dotted string) bool {
	components, _ := b.spec["components"].(map[string]any)
	resolver := compatibility{components: components}
	for _, status := range b.SuccessStatuses(call) {
		node := b.responseSchema(call, status)
		found := true
		for _, field := range strings.Split(dotted, ".") {
			node = resolver.follow(node)
			properties, _ := node["properties"].(map[string]any)
			required, _ := node["required"].([]any)
			next := asSchema(properties[field])
			if next == nil || !listed(required, field) {
				found = false
				break
			}
			node = next
		}
		if found {
			return true
		}
	}
	return false
}

func listed(names []any, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// follow resolves a $ref, a list's items, and the one non-null alternative
// of an optional field's anyOf, to the object schema under it.
func (c *compatibility) follow(node schema) schema {
	for depth := 0; node != nil && depth < maxDepth; depth++ {
		if ref, ok := node["$ref"].(string); ok {
			node, _ = c.resolve(ref)
			continue
		}
		if items := asSchema(node["items"]); items != nil && node["properties"] == nil {
			node = items
			continue
		}
		alternatives, ok := node["anyOf"].([]any)
		if !ok {
			return node
		}
		var objects []schema
		for _, alternative := range alternatives {
			if typesOf(asSchema(alternative)["type"]) == nil || typesOf(asSchema(alternative)["type"])[0] != "null" {
				objects = append(objects, asSchema(alternative))
			}
		}
		if len(objects) != 1 {
			return node
		}
		node = objects[0]
	}
	return node
}
