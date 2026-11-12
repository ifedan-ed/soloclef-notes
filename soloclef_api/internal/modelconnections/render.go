package modelconnections

import (
	"errors"
	"math"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// ListedConnection is one row of `connections` as list_connections reads it,
// with the models its `selectinload` attached.
//
// It is named apart from the generated `Connection`, which is the row as the
// statement returned it; this is the row as the route holds it.
type ListedConnection struct {
	ID       int64
	Provider string
	BaseURL  *string
	// HasAPIKey is whether a key is saved: Python's `bool(conn.api_key)`,
	// asked of the database (queries.sql). There is no field for the key
	// itself, because this tier never reads it. See the package comment,
	// "The key".
	HasAPIKey bool
	// Extra is connections.extra as the column's text. It may hold
	// credentials (credentialBags); what is rendered of it does not.
	Extra []byte
	// Scope is "user" for every row this package lists: the operator's
	// "global" connections have no owner and are never an account's.
	Scope     string
	UserID    *string
	Enabled   bool
	CreatedAt time.Time
	Models    []ListedModel
}

// ListedModel is one row of `models`.
type ListedModel struct {
	ID                      int64
	ConnectionID            int64
	ModelID                 string
	DisplayName             *string
	Source                  string
	SupportsChat            *bool
	MaxInputTokens          *int64
	SupportsImageInput      *bool
	SupportsTools           *bool
	SupportsImageGeneration *bool
	// CapabilitiesOverride and Catalog are the two jsonb columns as text.
	CapabilitiesOverride []byte
	Enabled              bool
	Catalog              []byte
	CreatedAt            time.Time
}

// errNotAnObject is a jsonb column holding something the response model types
// `dict[str, Any]` and that is not an object. pydantic raises a
// ValidationError while the route is still building its answer, nothing
// catches it, and it is the sanitised 500.
var errNotAnObject = errors.New("modelconnections: a jsonb column the model types as an object is not one")

// AttachModels hands each connection its models, which is what
// `selectinload(Connection.models)` does with the rows of its own statement.
//
// The models arrive ordered by id and are appended in that order, so each
// connection's list is ordered by id -- the relationship's `order_by`. A model
// whose connection is not in the list cannot be returned by the statement and
// is dropped rather than guessed a home.
func AttachModels(connections []ListedConnection, models []ListedModel) {
	position := make(map[int64]int, len(connections))
	for index := range connections {
		position[connections[index].ID] = index
	}
	for _, model := range models {
		index, known := position[model.ConnectionID]
		if !known {
			continue
		}
		connections[index].Models = append(connections[index].Models, model)
	}
}

// BuildConnections is the list comprehension list_connections returns. One row
// that cannot be rendered fails the whole list, as an exception inside a
// comprehension does.
func BuildConnections(rows []ListedConnection) ([]any, error) {
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		item, err := buildConnection(row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// buildConnection is _connection_read for a stored connection, with
// ConnectionRead's fields in the order the model declares them -- which is not
// the order the route names them in, and it is the model's order that pydantic
// writes.
func buildConnection(row ListedConnection) (*pythonjson.Object, error) {
	// `models=[_model_read(model) for model in models]` is evaluated while the
	// arguments are gathered, before the model looks at `extra`. Both
	// failures are the same 500, so the order cannot be seen; it is kept
	// because it is the route's.
	models := make([]any, 0, len(row.Models))
	for _, held := range row.Models {
		model, err := buildModel(held)
		if err != nil {
			return nil, err
		}
		models = append(models, model)
	}

	// `extra=_public_extra(conn.extra)`. That is `conn.extra or {}` first:
	// anything falsy is the empty object, and what is left must be an object
	// -- _public_extra hands on a value that is not one untouched, and the
	// model refuses it. Then the object goes out without its credential bags.
	extra, err := decodeColumn(row.Extra)
	if err != nil {
		return nil, err
	}
	if !truthy(extra) {
		extra = pythonjson.NewObject()
	}
	extraObject, err := objectForModel(extra)
	if err != nil {
		return nil, err
	}
	extraObject = withoutCredentialBags(extraObject)
	// The names of the headers the connection sends, never their values
	// (ownmodels/headers.go): kept in `extra` beside the sealed text and
	// answered here as a field of their own.
	headerNames := ownmodels.HeaderNamesOf(row.Extra)

	item := pythonjson.NewObject()
	item.Set("id", row.ID)
	item.Set("provider", row.Provider)
	item.Set("base_url", optionalText(row.BaseURL))
	// No "api_key". ConnectionRead has no such field: the stored key is not
	// answered, to anybody (package comment, "The key").
	item.Set("extra", extraObject)
	// The text the column holds: "user".
	item.Set("scope", row.Scope)
	// A uuid.UUID is written as its canonical text.
	item.Set("user_id", optionalText(row.UserID))
	item.Set("enabled", row.Enabled)
	// `has_api_key=bool(conn.api_key)`: None and the empty string are false,
	// and every other string is true -- a single space included. The
	// statement asked exactly that of the column.
	item.Set("has_api_key", row.HasAPIKey)
	names := make([]any, len(headerNames))
	for index, name := range headerNames {
		names[index] = name
	}
	item.Set("header_names", names)
	item.Set("models", models)
	// A response model, so the datetime is pydantic's and ends in "Z".
	item.Set("created_at", pythonjson.PydanticDateTime(row.CreatedAt))
	return item, nil
}

// credentialBags is _CREDENTIAL_BAGS: the two keys of `extra` that the Python
// tier merges, unread, into the call it makes to a provider. They are where a
// credential that is not a plain API key is kept -- Amazon Bedrock's access
// key, secret and bearer token, and Vertex AI's whole service-account file,
// all sit in `extra.litellm_params` -- and so neither is answered.
//
// Both are left out whole. They are not filtered name by name, because
// anything may be put in them and a list of "the secret names" would always
// be one short.
//
// A connection's headers are kept in `extra` too, sealed, with their names
// beside them (ownmodels.ExtraKeys). They are left out of `extra` for the
// same reason: the sealed text is a credential's, and the names are
// answered as `header_names`.
var credentialBags = map[string]bool{"litellm_params": true, "kwargs": true, "headers_sealed": true, "header_names": true}

// withoutCredentialBags is _public_extra for an object: a copy without the two
// bags, every other key kept in its place. Only the top level is looked at,
// as there: a key of the same name further down is not one of the bags.
func withoutCredentialBags(extra *pythonjson.Object) *pythonjson.Object {
	public := pythonjson.NewObject()
	for _, key := range extra.Keys() {
		if credentialBags[key] {
			continue
		}
		public.Set(key, extra.GetOrNil(key))
	}
	return public
}

// buildModel is `ModelRead.model_validate(model)`, in the model's field order.
func buildModel(row ListedModel) (*pythonjson.Object, error) {
	// No `or {}` here, unlike `extra`: the attribute is handed to the model
	// as it is, so a column holding null, a list or a zero is refused.
	override, err := decodeColumn(row.CapabilitiesOverride)
	if err != nil {
		return nil, err
	}
	overrideObject, err := objectForModel(override)
	if err != nil {
		return nil, err
	}
	catalog, err := decodeColumn(row.Catalog)
	if err != nil {
		return nil, err
	}
	catalogObject, err := objectForModel(catalog)
	if err != nil {
		return nil, err
	}

	item := pythonjson.NewObject()
	item.Set("id", row.ID)
	item.Set("connection_id", row.ConnectionID)
	item.Set("model_id", row.ModelID)
	item.Set("display_name", optionalText(row.DisplayName))
	item.Set("source", row.Source)
	item.Set("supports_chat", optionalTruth(row.SupportsChat))
	item.Set("max_input_tokens", optionalNumber(row.MaxInputTokens))
	item.Set("supports_image_input", optionalTruth(row.SupportsImageInput))
	item.Set("supports_tools", optionalTruth(row.SupportsTools))
	item.Set("supports_image_generation", optionalTruth(row.SupportsImageGeneration))
	item.Set("capabilities_override", overrideObject)
	item.Set("enabled", row.Enabled)
	item.Set("catalog", catalogObject)
	item.Set("created_at", pythonjson.PydanticDateTime(row.CreatedAt))
	return item, nil
}

// decodeColumn is what SQLAlchemy hands the route for a jsonb column:
// json.loads of the text. The three columns read here are NOT NULL, so an
// empty text is a row this package was handed wrongly; it is read as the
// Python None, which each caller then treats as it treats the JSON null.
func decodeColumn(raw []byte) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	return pythonjson.Decode(raw)
}

// objectForModel is a `dict[str, Any]` field given a value json.loads
// produced: the object, with every number in it as pydantic-core writes one,
// or errNotAnObject.
func objectForModel(value any) (*pythonjson.Object, error) {
	object, isObject := value.(*pythonjson.Object)
	if !isObject {
		return nil, errNotAnObject
	}
	rendered, _ := pydanticAny(object).(*pythonjson.Object)
	return rendered, nil
}

// pydanticAny is a value typed `Any` as pydantic-core serialises it to JSON.
//
// Two things differ from json.dumps, and both are in the recorded table:
//
//   - a float is written by pydantic-core's own formatter, so `0.0000001` is
//     `1e-7` and not `1e-07` (pythonjson.PydanticFloat).
//   - a float that is infinite is written `null`. That is pydantic's default
//     `ser_json_inf_nan`, and it is reachable: jsonb holds a number as a
//     decimal of any size, json.loads turns one with a fraction into a float,
//     and Python's float of four hundred digits and a ".5" is `inf`.
//     json.dumps would have raised on it; this route answers 200 with a null.
//     pythonjson.PydanticFloat refuses an infinity, so it is replaced here,
//     before that formatter sees it. (A NaN cannot arrive: jsonb has none.)
//
// An integer is not a float at any width: json.loads keeps it exact and
// pydantic writes every digit, so it stays the text it came as.
func pydanticAny(value any) any {
	return pythonjson.PydanticValues(withoutInfinities(value))
}

// withoutInfinities copies a decoded value with every infinite float replaced
// by null, and leaves everything else -- key order included -- as it is.
func withoutInfinities(value any) any {
	switch held := value.(type) {
	case float64:
		if math.IsInf(held, 0) {
			return nil
		}
		return held
	case []any:
		copied := make([]any, 0, len(held))
		for _, item := range held {
			copied = append(copied, withoutInfinities(item))
		}
		return copied
	case *pythonjson.Object:
		copied := pythonjson.NewObject()
		for _, key := range held.Keys() {
			copied.Set(key, withoutInfinities(held.GetOrNil(key)))
		}
		return copied
	}
	return value
}

// truthy is Python's bool() of a value json.loads produced, which is what
// `conn.extra or {}` asks without asking the value's type first: None, False,
// zero in either number form, and the empty string, list and object are
// false, and everything else is true.
//
// internal/artifacts has the same function for the same reason. It is
// unexported there and this package may not reach into it; the place for one
// copy is internal/pythonjson, beside the value model it is a question about.
func truthy(value any) bool {
	switch held := value.(type) {
	case nil:
		return false
	case bool:
		return held
	case string:
		return held != ""
	case pythonjson.RawInt:
		// An integer kept as its text. It is zero when it has no digit other
		// than 0, whatever its sign.
		return strings.Trim(strings.TrimPrefix(string(held), "-"), "0") != ""
	case float64:
		return held != 0
	case []any:
		return len(held) > 0
	case *pythonjson.Object:
		return held.Len() > 0
	}
	// pythonjson.Decode produces nothing else. Reading an unknown value as
	// true sends it on to objectForModel, which refuses it, rather than
	// quietly answering an empty object for it.
	return true
}

// The three kinds of nullable column: null, or the value.

func optionalText(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func optionalNumber(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func optionalTruth(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}
