package toolschema_test

import (
	"encoding/json"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
)

// The schema shapes below are pydantic's, as the Python server published them.
// The whole published surface is checked against a capture of that server in
// internal/toolmanifest; this checks each shape on its own, so a change to the
// rules says which rule it broke.
func TestEachParameterShapeMatchesPydantic(t *testing.T) {
	signature := toolschema.Signature{
		ArgumentsTitle: "exampleArguments",
		Parameters: []toolschema.Parameter{
			{Name: "text", Title: "Text", Description: "Some text.",
				Kind: toolschema.Text, Required: true,
				MinLength: toolschema.Limit(1)},
			{Name: "count", Title: "Count", Description: "How many.",
				Kind: toolschema.Number, Default: 10,
				Minimum: toolschema.Limit(1), Maximum: toolschema.Limit(30)},
			{Name: "flag", Title: "Flag", Description: "On or off.",
				Kind: toolschema.Flag, Default: false},
			{Name: "names", Title: "Names", Description: "A list.",
				Kind: toolschema.TextList, Required: true,
				MinItems: toolschema.Limit(1)},
			{Name: "choice", Title: "Choice", Description: "One of two.",
				Kind: toolschema.Text, Choices: []string{"a", "b"}, Default: "a"},
			{Name: "maybe_text", Title: "Maybe Text", Description: "Or nothing.",
				Kind: toolschema.Text, Nullable: true},
			{Name: "maybe_count", Title: "Maybe Count", Description: "Or nothing.",
				Kind: toolschema.Number, Nullable: true},
			{Name: "maybe_names", Title: "Maybe Names", Description: "Or nothing.",
				Kind: toolschema.TextList, Nullable: true},
			{Name: "maybe_choice", Title: "Maybe Choice", Description: "Or nothing.",
				Kind: toolschema.Text, Choices: []string{"x", "y"}, Nullable: true},
		},
	}
	// Members are alphabetical because pydantic's are, so the two agree byte for
	// byte rather than only after canonicalisation.
	const want = `{"properties":{` +
		`"choice":{"default":"a","description":"One of two.","enum":["a","b"],` +
		`"title":"Choice","type":"string"},` +
		`"count":{"default":10,"description":"How many.","maximum":30,"minimum":1,` +
		`"title":"Count","type":"integer"},` +
		`"flag":{"default":false,"description":"On or off.","title":"Flag","type":"boolean"},` +
		`"maybe_choice":{"anyOf":[{"enum":["x","y"],"type":"string"},{"type":"null"}],` +
		`"default":null,"description":"Or nothing.","title":"Maybe Choice"},` +
		`"maybe_count":{"anyOf":[{"type":"integer"},{"type":"null"}],"default":null,` +
		`"description":"Or nothing.","title":"Maybe Count"},` +
		`"maybe_names":{"anyOf":[{"items":{"type":"string"},"type":"array"},{"type":"null"}],` +
		`"default":null,"description":"Or nothing.","title":"Maybe Names"},` +
		`"maybe_text":{"anyOf":[{"type":"string"},{"type":"null"}],"default":null,` +
		`"description":"Or nothing.","title":"Maybe Text"},` +
		`"names":{"description":"A list.","items":{"type":"string"},"minItems":1,` +
		`"title":"Names","type":"array"},` +
		`"text":{"description":"Some text.","minLength":1,"title":"Text","type":"string"}},` +
		`"required":["text","names"],"title":"exampleArguments","type":"object"}`

	schema, err := signature.JSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	if string(schema) != want {
		t.Errorf("schema is\n %s\nwant\n %s", schema, want)
	}
}

// Required names are in declaration order, not alphabetical: the Python server
// lists them in the order the handler declares its parameters.
func TestRequiredNamesKeepDeclarationOrder(t *testing.T) {
	signature := toolschema.Signature{
		ArgumentsTitle: "exampleArguments",
		Parameters: []toolschema.Parameter{
			{Name: "zebra", Title: "Zebra", Kind: toolschema.Text, Required: true},
			{Name: "apple", Title: "Apple", Kind: toolschema.Text, Required: true},
		},
	}
	schema, err := signature.JSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schema, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Required) != 2 || decoded.Required[0] != "zebra" {
		t.Errorf("required is %v, want declaration order", decoded.Required)
	}
}

// A signature with no required parameter has no "required" member at all, which
// is what pydantic emits.
func TestASignatureWithNoRequiredParameterOmitsTheMember(t *testing.T) {
	signature := toolschema.Signature{
		ArgumentsTitle: "exampleArguments",
		Parameters: []toolschema.Parameter{
			{Name: "flag", Title: "Flag", Kind: toolschema.Flag, Default: false},
		},
	}
	schema, err := signature.JSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(schema, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, present := decoded["required"]; present {
		t.Errorf("schema carries a required member: %s", schema)
	}
}

// A description with a character Go's encoder escapes by default must reach the
// client unescaped, because pydantic's does.
func TestADescriptionIsNotHTMLEscaped(t *testing.T) {
	signature := toolschema.Signature{
		ArgumentsTitle: "exampleArguments",
		Parameters: []toolschema.Parameter{{
			Name: "text", Title: "Text", Kind: toolschema.Text,
			Description: "A job page ('/cmp/<slug>/jobs') & more.",
		}},
	}
	schema, err := signature.JSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(schema), `'/cmp/<slug>/jobs') & more.`) {
		t.Errorf("the description was escaped: %s", schema)
	}
}
