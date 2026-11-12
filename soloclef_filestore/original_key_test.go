package filestore

import (
	"strings"
	"testing"
)

// The key is the owner's prefix, the document, and a random name: nothing
// of the file's own name but a plain extension reaches it.
func TestAnOriginalsKeyIsUnderItsOwnerAndTakesOnlyAPlainExtension(t *testing.T) {
	const owner = "11111111-1111-4111-8111-111111111111"
	for name, extension := range map[string]string{
		"Report.PDF": ".pdf", "notes": "", "../../etc/passwd": "", "a.b c": "", "x.tar.gz": ".gz",
		"long." + strings.Repeat("e", 20): "",
	} {
		key, err := OriginalKey(owner, 7, name)
		if err != nil {
			t.Fatal(err)
		}
		prefix := "documents/" + owner + "/7/original/"
		random := strings.TrimSuffix(strings.TrimPrefix(key, prefix), extension)
		if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, extension) || len(random) != 32 {
			t.Errorf("%q gave the key %q", name, key)
		}
		if validKey(key) != nil {
			t.Errorf("%q gave a key the store refuses: %q", name, key)
		}
	}
	first, _ := OriginalKey(owner, 7, "a.pdf")
	second, _ := OriginalKey(owner, 7, "a.pdf")
	if first == second {
		t.Error("two keys for one file are the same: a second upload would overwrite the first")
	}
}
