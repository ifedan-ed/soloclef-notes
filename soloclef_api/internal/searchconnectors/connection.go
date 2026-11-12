package searchconnectors

import (
	"errors"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Connection is one row of search_source_connectors, with its config decoded.
type Connection struct {
	ID   int64
	Name string
	Type string
	// Enabled is the account's switch for this connection. Off keeps the
	// row and its credentials; the chat and the indexing schedule leave it
	// alone.
	Enabled       bool
	IsIndexable   bool
	LastIndexedAt *time.Time
	// Config is the column as readColumn reads it: any JSON value.
	Config                   any
	EnableVisionLLM          bool
	PeriodicIndexingEnabled  bool
	IndexingFrequencyMinutes *int64
	NextScheduledAt          *time.Time
	// UserID is the owner: the account whose credentials the row holds.
	UserID    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsServer reports whether the row is an MCP server.
func (c Connection) IsServer() bool {
	return c.Type == serverTypeTag
}

// errRowRefused is SearchSourceConnectorRead.model_validate raising for a row
// the database holds. Every read calls it, inside the route's `try`, so it is
// the route's own 500.
var errRowRefused = errors.New("searchconnectors: the read model refuses this row")

// errNotAnObject is a truthy value that is not an object where the route
// calls `.get` on it, or `in` on a number: an AttributeError or a TypeError,
// caught by the same `except Exception`.
var errNotAnObject = errors.New("searchconnectors: a json value is not the object the route reads it as")

// validated is SearchSourceConnectorRead.model_validate(connector), for the
// three things it can refuse about a row:
//
//   - `config: dict[str, Any]` -- the column is NOT NULL but holds any JSON,
//     the scalar `null` included;
//   - validate_connector_config, which for a mailbox connection requires
//     `accounts` to be absent, null or a list whose every entry is an object
//     with a truthy `_encrypted`;
//   - validate_periodic_indexing, over the connection's own columns, which
//     are vestigial for every type but MCP and are validated all the same.
//
// It returns the config, which from here on is known to be an object.
func validated(connection Connection) (*pythonjson.Object, error) {
	config, isObject := connection.Config.(*pythonjson.Object)
	if !isObject {
		return nil, errRowRefused
	}
	if connection.Type == mailboxType {
		// assert_accounts_are_encrypted(config.get("accounts")).
		switch accounts := config.GetOrNil("accounts").(type) {
		case nil:
		case []any:
			for _, entry := range accounts {
				account, isAccount := entry.(*pythonjson.Object)
				if !isAccount || !truthy(account.GetOrNil("_encrypted")) {
					return nil, errRowRefused
				}
			}
		default:
			return nil, errRowRefused
		}
	}
	if connection.PeriodicIndexingEnabled {
		if !connection.IsIndexable || connection.IndexingFrequencyMinutes == nil ||
			*connection.IndexingFrequencyMinutes <= 0 {
			return nil, errRowRefused
		}
	}
	return config, nil
}

// shownConfig is the first half of _readable: the row validated over its
// whole stored config, and then only the public part of that config shown
// (public_config.go). The order matters and is Python's: a row the read
// model refuses is refused whatever a reader would have been shown of it.
func shownConfig(connection Connection) (*pythonjson.Object, error) {
	config, err := validated(connection)
	if err != nil {
		return nil, err
	}
	return publicConfig(connection.Type, config)
}

// renderConnection is one connection as the list, the read by id and the
// switch all answer it. The indexing fields are the row's own columns: with
// one owner there is one schedule, and nothing overrides it.
func renderConnection(connection Connection, config *pythonjson.Object) ([]byte, error) {
	written, err := writeMapping(config)
	if err != nil {
		return nil, err
	}

	var body fields
	for _, field := range []struct {
		name  string
		value any
	}{
		{"created_at", pythonjson.PydanticDateTime(connection.CreatedAt)},
		{"id", connection.ID},
		{"name", connection.Name},
		{"connector_type", connection.Type},
		{"enabled", connection.Enabled},
		{"is_indexable", connection.IsIndexable},
		{"last_indexed_at", momentOrNull(connection.LastIndexedAt)},
		{"config", written},
		{"enable_vision_llm", connection.EnableVisionLLM},
		{"periodic_indexing_enabled", connection.PeriodicIndexingEnabled},
		{"indexing_frequency_minutes", numberOrNull(connection.IndexingFrequencyMinutes)},
		{"next_scheduled_at", momentOrNull(connection.NextScheduledAt)},
		{"user_id", connection.UserID},
	} {
		if text, rendered := field.value.([]byte); rendered {
			body.raw(field.name, text)
			continue
		}
		if err := body.set(field.name, field.value); err != nil {
			return nil, err
		}
	}
	return body.close(), nil
}

func momentOrNull(moment *time.Time) any {
	if moment == nil {
		return nil
	}
	return pythonjson.PydanticDateTime(*moment)
}

func numberOrNull(number *int64) any {
	if number == nil {
		return nil
	}
	return *number
}

// The three fields MCPConnectorRead.from_connector keeps of a server's
// stored settings, in the order it asks for them.
var serverFields = []string{"url", "headers", "transport"}

// shownServer is MCPConnectorRead.from_connector(
// SearchSourceConnectorRead.model_validate(connector)), up to the model it
// builds: everything that can fail inside the route's `try`.
//
// It validates the row exactly as the generic read does and then shows much
// less of it: of the whole config only `server_config`, and of that only the
// three fields above, each dropped when it is None, "", [] or {} -- and then
// masked.
func shownServer(connection Connection) (*pythonjson.Object, error) {
	config, err := validated(connection)
	if err != nil {
		return nil, err
	}
	kept, err := keptServerFields(config.GetOrNil("server_config"))
	if err != nil {
		return nil, err
	}
	masked, err := maskServerConfig(kept)
	if err != nil {
		return nil, err
	}
	return masked, nil
}

// renderServer is that model as pydantic-core serialises it, which is the
// one step that happens after the route has returned. The outermost keys of
// `server_config` are the three field names, so writeMapping's rule for a
// key holding a surrogate has nothing to apply to; everything below them is
// `Any`.
func renderServer(connection Connection, masked *pythonjson.Object) ([]byte, error) {
	written, err := writeMapping(masked)
	if err != nil {
		return nil, err
	}
	var body fields
	for _, field := range []struct {
		name  string
		value any
	}{
		{"id", connection.ID},
		{"name", connection.Name},
		{"connector_type", connection.Type},
		{"server_config", written},
		{"enabled", connection.Enabled},
		{"user_id", connection.UserID},
		{"created_at", pythonjson.PydanticDateTime(connection.CreatedAt)},
		{"updated_at", pythonjson.PydanticDateTime(connection.UpdatedAt)},
	} {
		if text, rendered := field.value.([]byte); rendered {
			body.raw(field.name, text)
			continue
		}
		if err := body.set(field.name, field.value); err != nil {
			return nil, err
		}
	}
	return body.close(), nil
}

// keptServerFields is the dict comprehension in from_connector:
//
//	stored = config.get("server_config") or {}
//	{key: stored[key] for key in ("url", "headers", "transport")
//	 if key in stored and stored[key] not in (None, "", [], {})}
//
// It is written for an object and Python runs it on whatever is there. A
// falsy value is the empty object. For a truthy one, `key in stored` is a
// question every container answers its own way, and `stored[key]` then fails
// for all of them but an object:
//
//   - a list holding the text "url" answers true and cannot be indexed by it;
//   - a str is asked whether it *contains* "url", and cannot either;
//   - a number or `true` cannot be asked at all.
//
// So a list or a str that mentions none of the three field names is an empty
// server_config and a 200, and one that mentions any is a 500.
func keptServerFields(stored any) (*pythonjson.Object, error) {
	kept := pythonjson.NewObject()
	if !truthy(stored) {
		return kept, nil
	}
	switch held := stored.(type) {
	case *pythonjson.Object:
		for _, field := range serverFields {
			value, present := held.Get(field)
			if present && !isEmptyServerField(value) {
				kept.Set(field, value)
			}
		}
		return kept, nil
	case []any:
		for _, item := range held {
			if text, isText := item.(string); isText && isServerField(text) {
				return nil, errNotAnObject
			}
		}
		return kept, nil
	case string:
		for _, field := range serverFields {
			if containsText(held, field) {
				return nil, errNotAnObject
			}
		}
		return kept, nil
	}
	return nil, errNotAnObject
}

func isServerField(text string) bool {
	for _, field := range serverFields {
		if text == field {
			return true
		}
	}
	return false
}

// isEmptyServerField is `value in (None, "", [], {})`, which is equality with
// each: a zero or a false is none of the four and is kept.
func isEmptyServerField(value any) bool {
	switch held := value.(type) {
	case nil:
		return true
	case string:
		return held == ""
	case []any:
		return len(held) == 0
	case *pythonjson.Object:
		return held.Len() == 0
	}
	return false
}
