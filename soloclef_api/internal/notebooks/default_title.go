package notebooks

import (
	"strings"

	"golang.org/x/text/cases"
)

// defaultTitle is what a notebook is called when the request names nothing.
const defaultTitle = "Untitled notebook"

// defaultTitles is every translation of "Untitled notebook" the web and
// mobile apps create a notebook with (`notebooks.untitled` in each locale
// file, and app/notebooks/autoname/defaults.py on the Python tier). Keep the
// three in step: a title missing here is a notebook that never names itself.
var defaultTitles = []string{
	defaultTitle,          // en
	"Cuaderno sin título", // es
	"Caderno sem título",  // pt
	"बिना शीर्षक की नोटबुक", // hi
	"제목 없는 노트북",             // ko
	"未命名笔记本",                // zh
}

var foldedDefaults = func() map[string]bool {
	folded := make(map[string]bool, len(defaultTitles))
	for _, title := range defaultTitles {
		folded[foldTitle(title)] = true
	}
	return folded
}()

// isDefaultTitle is whether a title is one of the apps' own "Untitled
// notebook" strings, which is what lets a new notebook name itself later.
// Case and spacing do not count: "  UNTITLED   notebook" is the default.
func isDefaultTitle(title string) bool {
	return foldedDefaults[foldTitle(title)]
}

// foldTitle is a title with its runs of whitespace made single spaces and
// its case folded by Unicode's own rules, so "TÍTULO" and "título" compare
// equal and a title is not a default merely by looking like one.
func foldTitle(title string) string {
	return cases.Fold().String(strings.Join(strings.Fields(title), " "))
}
