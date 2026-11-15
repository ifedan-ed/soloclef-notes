package figurekit

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	figures "github.com/ifedan-ed/figures-go"
)

// The description offers no browser-drawn type anywhere: not in its
// sections, its rules or its advice. (Each edit applying exactly once is
// held by the package loading at all.)
func TestTheDescriptionOffersOnlySVGFigures(t *testing.T) {
	d := strings.ToLower(Description())
	for _, gone := range []string{"mermaid", "html", "### html", "### mermaid"} {
		if strings.Contains(d, gone) {
			t.Errorf("the description still mentions %q", gone)
		}
	}
	for _, typ := range Types() {
		if !strings.Contains(Description(), "\n### "+typ+"\n") {
			t.Errorf("the description lacks the section of %s", typ)
		}
	}
	if !strings.HasPrefix(Catalogue(), "Which type to use:") || strings.Contains(strings.ToLower(Catalogue()), "mermaid") {
		t.Errorf("the catalogue: %.80q", Catalogue())
	}
}

// The schema's type enum is the offered types, and every offered type's
// own worked example draws.
func TestEveryOfferedTypeDraws(t *testing.T) {
	var schema struct {
		Properties struct {
			Type struct {
				Enum []string `json:"enum"`
			} `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if strings.Join(schema.Properties.Type.Enum, ",") != strings.Join(Types(), ",") {
		t.Errorf("the enum %v is not the types %v", schema.Properties.Type.Enum, Types())
	}
	if len(Types()) != len(figures.Types())-len(browserDrawn) {
		t.Errorf("%d types offered of figures-go's %d", len(Types()), len(figures.Types()))
	}
	for _, typ := range Types() {
		example, _ := figures.Example(typ)
		f, err := Render([]byte(example))
		if err != nil {
			t.Errorf("%s: %v", typ, err)
			continue
		}
		if f.Form != "svg" || f.SVG == "" || f.SVGDark == "" {
			t.Errorf("%s: drawn as %q without both SVGs", typ, f.Form)
		}
		if _, err := DrawSVG([]byte(example), &Palette{Background: "#FFFFFF", Text: "#1F2937"}, true); err != nil {
			t.Errorf("%s in a palette: %v", typ, err)
		}
	}
}

// A browser-drawn type is refused in words naming what to use instead.
func TestBrowserTypesAreRefusedInWords(t *testing.T) {
	for _, typ := range browserDrawn {
		example, _ := figures.Example(typ)
		_, err := Render([]byte(example))
		var r *Refusal
		if !errors.As(err, &r) || !strings.Contains(err.Error(), "is not drawn here") || !strings.Contains(err.Error(), "flowchart") {
			t.Errorf("%s: %v", typ, err)
		}
		if _, err := DrawSVG([]byte(example), nil, false); !errors.As(err, &r) {
			t.Errorf("%s in a palette: %v", typ, err)
		}
	}
}

// An edit that no longer applies is an error, not a description that
// quietly still offers mermaid.
func TestAnEditThatNoLongerAppliesFails(t *testing.T) {
	if _, err := withoutBrowserTypes("a description without the sentences"); err == nil {
		t.Fatal("no error")
	}
}
