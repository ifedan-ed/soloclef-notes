package deliverables

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Nothing in the Go tiers asks a model to draw: no request to the proxy's
// image route, no image model setting, no generate_image tool. Figures and
// infographics are drawn by code (soloclef_studio/figurekit, posterrender).
// The whole checkout's Go source is read, tests aside, so a picture model
// cannot come back through another package unnoticed.
func TestNothingCallsAnImageModel(t *testing.T) {
	root := filepath.Join("..", "..")
	// (A model connection's "supports_image_generation" flag stays: it
	// describes an account's own model in its connection's list, and is
	// read by the installed phone builds; nothing asks such a model to draw.)
	forbidden := []string{"/images/generations", "CHAT_IMAGE_MODEL", "AUTOMATIONS_IMAGE_MODEL", `"generate_image"`, "ImageModel"}
	read := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "target" || strings.HasPrefix(d.Name(), ".")) && path != root {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		read++
		for _, word := range forbidden {
			if strings.Contains(string(source), word) {
				t.Errorf("%s names %s", path, word)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if read < 500 {
		t.Fatalf("only %d Go files were read: the walk did not reach the checkout", read)
	}
}
