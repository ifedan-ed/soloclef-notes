package pythonjson

import "testing"

func TestDecodeThenMarshalMatchesPythonRoundTrip(t *testing.T) {
	// json.dumps(json.loads(stored), ensure_ascii=False, separators=(",", ":")):
	// the spacing goes, the key order stays, 1.50 becomes 1.5.
	stored := []byte(`{"document_type": "FILE", "document_id": 313, "nested": {"z": 1.50, "a": [true, null]}}`)

	value, err := Decode(stored)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	got, err := Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `{"document_type":"FILE","document_id":313,"nested":{"z":1.5,"a":[true,null]}}`
	if string(got) != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestDecodeReadsFieldsOutOfAnObject(t *testing.T) {
	value, err := Decode([]byte(`{"task_name": "document_reindex", "document_id": 277, "n": 1}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	object, isObject := value.(*Object)
	if !isObject {
		t.Fatalf("got %T, want *Object", value)
	}
	if name := object.GetString("task_name", "Unknown"); name != "document_reindex" {
		t.Errorf("task_name: got %q", name)
	}
	if name := object.GetString("missing", "Unknown"); name != "Unknown" {
		t.Errorf("fallback: got %q", name)
	}
	if name := object.GetString("n", "Unknown"); name != "Unknown" {
		t.Errorf("a non-string text field must read as absent, got %q", name)
	}
	if id := object.GetOrNil("document_id"); id != RawInt("277") {
		t.Errorf("document_id: got %#v", id)
	}
	if id := object.GetOrNil("connector_id"); id != nil {
		t.Errorf("a missing key must be null, got %#v", id)
	}
}

func TestDecodeRefusesTrailingData(t *testing.T) {
	if _, err := Decode([]byte(`{} {}`)); err == nil {
		t.Error("want an error, got none")
	}
}
