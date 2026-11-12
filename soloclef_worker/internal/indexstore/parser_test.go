package indexstore

import "testing"

// The order a document's parser is decided in: its own, then its
// connection's, then the deployment's default. Each case is a row the
// database can actually hold.
func TestChooseParserTakesTheDocumentsThenTheConnectorsThenTheDefault(t *testing.T) {
	name := func(text string) *string { return &text }
	for _, test := range []struct {
		about               string
		document, connector *string
		parser              string
		from                ParserSource
	}{
		{"neither names one", nil, nil, "", ParserFromDefault},
		{"only the document names one", name("datalab"), nil, "datalab", ParserFromDocument},
		{"only the connection names one", nil, name("llamaparse"), "llamaparse", ParserFromConnector},
		// A re-index that asked for another parser must not be undone by
		// the source's setting.
		{"both name one: the document's wins", name("local"), name("unstructured"), "local", ParserFromDocument},
		// The CHECK cannot store "", but if a database ever held one it is
		// "not chosen", and the next step down is asked.
		{"an empty document parser is not a choice", name(""), name("datalab"), "datalab", ParserFromConnector},
		{"an empty connection parser is not a choice", nil, name(""), "", ParserFromDefault},
	} {
		parser, from := chooseParser(test.document, test.connector)
		if parser != test.parser || from != test.from {
			t.Errorf("%s: got %q from %q, want %q from %q", test.about, parser, from, test.parser, test.from)
		}
	}
}
