package searchconnectors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// pythonValues is testdata/python_values.json, recorded by asking Python
// itself while the Python version still ran: its real _mask_url, mask_value,
// always_asks and _SENSITIVE_PARAM, and the interpreter's own str(), bool(),
// slicing and Unicode tables.
type pythonValues struct {
	Python  string `json:"python"`
	Unicode string `json:"unicode"`
	URLs    []struct {
		URL    string `json:"url"`
		Masked string `json:"masked"`
		Raises bool   `json:"raises"`
	} `json:"urls"`
	Values []struct {
		JSON   string `json:"json"`
		Str    []rune `json:"str"`
		Masked []rune `json:"masked"`
		Truthy bool   `json:"truthy"`
	} `json:"values"`
	Integers []struct {
		JSON   string `json:"json"`
		Raises bool   `json:"raises"`
	} `json:"integers"`
	Unprintable [][2]rune `json:"unprintable"`
	Lowering    []struct {
		CodePoint rune   `json:"code_point"`
		Lowered   string `json:"lowered"`
	} `json:"lowering"`
	Folding        map[string][]rune `json:"folding"`
	ParameterNames []struct {
		Name      string `json:"name"`
		Sensitive bool   `json:"sensitive"`
	} `json:"parameter_names"`
	ToolNames []struct {
		Name       string `json:"name"`
		AlwaysAsks bool   `json:"always_asks"`
	} `json:"tool_names"`
	Windows []struct {
		Length int    `json:"length"`
		Skip   string `json:"skip"`
		Limit  string `json:"limit"`
		Kept   []int  `json:"kept"`
	} `json:"windows"`
	// PublicKeys is public_config asked of one config per connector type.
	PublicKeys []struct {
		ConnectorType string   `json:"connector_type"`
		Config        string   `json:"config"`
		Shown         []string `json:"shown"`
	} `json:"public_keys"`
	// Storable is cannot_be_stored of a str given as JSON text.
	Storable []struct {
		JSON           string `json:"json"`
		CannotBeStored bool   `json:"cannot_be_stored"`
	} `json:"storable"`
}

func readPythonValues(t *testing.T) pythonValues {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "python_values.json"))
	if err != nil {
		t.Fatalf("reading Python's answers: %v", err)
	}
	var table pythonValues
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatalf("parsing Python's answers: %v", err)
	}
	if len(table.URLs) == 0 || len(table.Values) == 0 || len(table.Integers) == 0 || len(table.Unprintable) == 0 ||
		len(table.Lowering) == 0 || len(table.Folding) == 0 || len(table.ParameterNames) == 0 ||
		len(table.ToolNames) == 0 || len(table.Windows) == 0 ||
		len(table.PublicKeys) == 0 || len(table.Storable) == 0 {
		t.Fatal("one of Python's tables is empty, so it proves nothing")
	}
	return table
}

// TestAddressesAreMaskedAsPythonMasksThem replays every address: what
// _mask_url answered, byte for byte, or the fact that urlsplit refused it.
func TestAddressesAreMaskedAsPythonMasksThem(t *testing.T) {
	table := readPythonValues(t)
	raised, masked := 0, 0
	for _, row := range table.URLs {
		got, err := maskAddress(row.URL)
		switch {
		case row.Raises && err == nil:
			t.Errorf("%q: Python raised and this tier answered %q", row.URL, got)
		case !row.Raises && err != nil:
			t.Errorf("%q: Python answered %q and this tier refused: %v", row.URL, row.Masked, err)
		case !row.Raises && got != row.Masked:
			t.Errorf("%q:\n Go     %q\n Python %q", row.URL, got, row.Masked)
		}
		if row.Raises {
			raised++
		} else if row.Masked != row.URL {
			masked++
		}
	}
	// A table of addresses that all came back untouched would pass a mirror
	// that does nothing.
	if raised < 100 || masked < 1000 {
		t.Errorf("only %d addresses raise and %d are changed; the table has lost its teeth", raised, masked)
	}
	t.Logf("replayed %d addresses: %d refused, %d rewritten", len(table.URLs), raised, masked)
}

// TestValuesReadAsPythonReadsThem replays str(), mask_value() and bool() of
// every recorded JSON value, read the way a column is read.
func TestValuesReadAsPythonReadsThem(t *testing.T) {
	table := readPythonValues(t)
	for _, row := range table.Values {
		read, err := readColumn([]byte(row.JSON))
		if err != nil {
			t.Errorf("%s: %v", row.JSON, err)
			continue
		}
		if got := codePoints(pyStr(read)); !reflect.DeepEqual(got, row.Str) {
			t.Errorf("str(%s):\n Go     %U\n Python %U", row.JSON, got, row.Str)
		}
		if got := codePoints(maskValue(read)); !reflect.DeepEqual(got, row.Masked) {
			t.Errorf("mask_value(%s):\n Go     %U\n Python %U", row.JSON, got, row.Masked)
		}
		if got := truthy(read); got != row.Truthy {
			t.Errorf("bool(%s): Go %v, Python %v", row.JSON, got, row.Truthy)
		}
	}
	t.Logf("replayed %d values three ways", len(table.Values))
}

// TestIntegersTooLongForPythonFailTheRead replays json.loads' refusal of an
// integer of more than 4,300 digits.
func TestIntegersTooLongForPythonFailTheRead(t *testing.T) {
	table := readPythonValues(t)
	raising := 0
	for _, row := range table.Integers {
		_, err := readColumn([]byte(row.JSON))
		if (err != nil) != row.Raises {
			t.Errorf("a %d-character document: Python raised=%v and this tier answered %v",
				len(row.JSON), row.Raises, err)
		}
		if row.Raises {
			raising++
		}
	}
	if raising == 0 || raising == len(table.Integers) {
		t.Errorf("%d of %d documents raise; the table cannot tell a constant from the rule",
			raising, len(table.Integers))
	}
}

// codePoints is a string as Python would list it: one entry per code point,
// a lone surrogate being one.
func codePoints(text string) []rune {
	points := []rune{}
	for len(text) > 0 {
		point, width := decodeCodePoint(text)
		points = append(points, point)
		text = text[width:]
	}
	return points
}

// TestEveryCodePointIsPrintableAsPythonSaysItIs sweeps every Unicode scalar
// value. repr() escapes exactly the ones str.isprintable() refuses, so this
// is what lets pyPrintable be unicode.IsPrint -- for as long as the two
// languages carry the same Unicode tables, which this test is the check of.
func TestEveryCodePointIsPrintableAsPythonSaysItIs(t *testing.T) {
	table := readPythonValues(t)
	unprintable := make(map[rune]bool)
	for _, run := range table.Unprintable {
		for point := run[0]; point <= run[1]; point++ {
			unprintable[point] = true
		}
	}
	checked, wrong := 0, 0
	for point := rune(0); point <= utf8.MaxRune; point++ {
		if point >= 0xd800 && point <= 0xdfff {
			continue
		}
		checked++
		if pyPrintable(point) == unprintable[point] {
			wrong++
			if wrong <= 10 {
				t.Errorf("U+%04X: Go says printable=%v and Python (Unicode %s) says %v",
					point, pyPrintable(point), table.Unicode, !unprintable[point])
			}
		}
	}
	if wrong > 0 {
		t.Errorf("%d code points differ; embed Python's list rather than trusting unicode.IsPrint", wrong)
	}
	t.Logf("swept %d code points", checked)
}

// TestTheLoweringTableIsPythons holds lowersToASCII to Python's sweep: the
// non-ASCII code points whose lower case holds an ASCII character, and what
// each lowers to.
func TestTheLoweringTableIsPythons(t *testing.T) {
	table := readPythonValues(t)
	recorded := make(map[rune]string)
	for _, row := range table.Lowering {
		recorded[row.CodePoint] = row.Lowered
	}
	if !reflect.DeepEqual(recorded, lowersToASCII) {
		t.Errorf("Python lowers %q to something holding ASCII and this tier's table is %q",
			recorded, lowersToASCII)
	}
}

// TestTheFoldingTableIsPythons holds foldsToASCII to Python's sweep of
// re.IGNORECASE: for each letter of the sensitive words, exactly the code
// points that match it.
func TestTheFoldingTableIsPythons(t *testing.T) {
	table := readPythonValues(t)
	letters := map[rune]bool{}
	for _, word := range sensitiveWords {
		for _, letter := range word {
			letters[letter] = true
		}
	}
	if len(table.Folding) != len(letters) {
		t.Fatalf("Python recorded %d letters and the words have %d", len(table.Folding), len(letters))
	}
	for text, matching := range table.Folding {
		letter := []rune(text)[0]
		want := map[rune]bool{}
		for _, point := range matching {
			want[point] = true
		}
		got := map[rune]bool{letter: true, letter - ('a' - 'A'): true}
		for point, folded := range foldsToASCII {
			if folded == letter {
				got[point] = true
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: Python matches %v and this tier matches %v", text, want, got)
		}
	}
	for _, row := range table.ParameterNames {
		if got := isSensitiveName(row.Name); got != row.Sensitive {
			t.Errorf("%q: Go says sensitive=%v and Python says %v", row.Name, got, row.Sensitive)
		}
	}
}

// TestToolsThatAlwaysAsk replays always_asks.
func TestToolsThatAlwaysAsk(t *testing.T) {
	table := readPythonValues(t)
	asking := 0
	for _, row := range table.ToolNames {
		if got := alwaysAsks(row.Name); got != row.AlwaysAsks {
			t.Errorf("%q: Go says %v and Python says %v", row.Name, got, row.AlwaysAsks)
		}
		if row.AlwaysAsks {
			asking++
		}
	}
	if asking == 0 || asking == len(table.ToolNames) {
		t.Errorf("%d of %d names ask; the table cannot tell a constant from the rule", asking, len(table.ToolNames))
	}
}

// TestAReadShowsTheKeysPythonShows holds publicKeys to the Python list it is
// a copy of. Python was asked, for every connector type its enum has, which
// keys of one crowded config a read shows; this tier is asked the same of the
// same config and must show the same keys in the same order.
//
// The types are compared as sets first. A type the Python enum gained is a
// row here with no entry in publicKeys, and a type it lost is an entry with
// no row: either is a list that has drifted, and both fail.
func TestAReadShowsTheKeysPythonShows(t *testing.T) {
	table := readPythonValues(t)
	recorded := map[string]bool{}
	hidden := 0
	for _, row := range table.PublicKeys {
		recorded[row.ConnectorType] = true
		if _, known := publicKeys[row.ConnectorType]; !known {
			t.Errorf("Python has a connector type %q this tier's list does not", row.ConnectorType)
			continue
		}
		read, err := readColumn([]byte(row.Config))
		if err != nil {
			t.Fatalf("%s: the recorded config cannot be read: %v", row.ConnectorType, err)
		}
		config, isObject := read.(*pythonjson.Object)
		if !isObject {
			t.Fatalf("%s: the recorded config is not an object", row.ConnectorType)
		}
		shown, err := publicConfig(row.ConnectorType, config)
		if err != nil {
			t.Errorf("%s: %v", row.ConnectorType, err)
			continue
		}
		if got := append([]string{}, shown.Keys()...); !reflect.DeepEqual(got, append([]string{}, row.Shown...)) {
			t.Errorf("%s:\n Go     %q\n Python %q", row.ConnectorType, got, row.Shown)
		}
		hidden += len(config.Keys()) - len(row.Shown)
	}
	for connectionType := range publicKeys {
		if !recorded[connectionType] {
			t.Errorf("this tier's list has a connector type %q Python's enum does not", connectionType)
		}
	}
	// A config of nothing but shown keys would pass a mirror that shows all.
	if hidden < 100 {
		t.Errorf("only %d keys were left out across all types; the table has lost its teeth", hidden)
	}
	if _, err := publicConfig("NO_SUCH_CONNECTOR", pythonjson.NewObject()); err == nil {
		t.Error("a connector type with no entry was shown something; it must be refused")
	}
	t.Logf("replayed %d connector types; %d keys left out", len(table.PublicKeys), hidden)
}

// TestTextThatCannotBeStoredIsPythons replays cannot_be_stored: U+0000 and
// the surrogates, at each edge of both.
func TestTextThatCannotBeStoredIsPythons(t *testing.T) {
	table := readPythonValues(t)
	refused := 0
	for _, row := range table.Storable {
		read, err := readColumn([]byte(row.JSON))
		if err != nil {
			t.Errorf("%s: %v", row.JSON, err)
			continue
		}
		text, isText := read.(string)
		if !isText {
			t.Fatalf("%s: not a str", row.JSON)
		}
		if got := cannotBeStored(text); got != row.CannotBeStored {
			t.Errorf("cannot_be_stored(%s): Go %v, Python %v", row.JSON, got, row.CannotBeStored)
		}
		if row.CannotBeStored {
			refused++
		}
	}
	if refused < 20 || refused > len(table.Storable)-20 {
		t.Errorf("%d of %d texts are refused; the table has lost its teeth", refused, len(table.Storable))
	}
	t.Logf("replayed %d texts, %d of them refused", len(table.Storable), refused)
}
