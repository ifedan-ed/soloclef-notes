package pagereader

import (
	"io"
	"net/url"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// markdown turns a tree of HTML into Markdown. It is the library the
// indexing pipeline already converts uploaded HTML files with
// (soloclef_ingest/htmlmarkdown), for the reason given there: nested lists,
// escaping and whitespace are a long tail of cases it has already met. One
// converter serves every call; it locks around its own state.
var markdown = converter.NewConverter(converter.WithPlugins(
	base.NewBasePlugin(),
	commonmark.NewCommonmarkPlugin(
		// A link this package took the address from (a script, an anchor
		// within the page) is written as its words, not as "[words]()".
		commonmark.WithLinkEmptyHrefBehavior(commonmark.LinkBehaviorSkip),
		// The library separates two adjacent lists with an HTML comment so
		// that Markdown renderers keep them apart. A model reads the text;
		// to it the comment is noise.
		commonmark.WithListEndComment(false),
	),
	table.NewTablePlugin(
		// The default pads every cell to its column's width: spaces the
		// model is billed for and that count against the page's share of
		// the answer.
		table.WithCellPaddingBehavior(table.CellPaddingBehaviorMinimal),
		// The default leaves a table with a line break in any cell as raw
		// HTML.
		table.WithNewlineBehavior(table.NewlineBehaviorPreserve),
		table.WithSkipEmptyRows(true),
	),
))

// extracted is what a page of HTML came to.
type extracted struct {
	Title string
	Text  string
	// Scripts says the page carries scripts. With next to no text it means
	// the page is drawn by JavaScript, which this reader does not run.
	Scripts bool
}

// extract reads HTML from r and returns the page's readable part as
// Markdown. address is where the page was read from; relative links are
// made absolute against it, so the model can ask for a linked page.
//
// How the readable part is chosen is two rules, and both can be read off
// the page's own markup:
//
//  1. What HTML itself says is not the article is removed: navigation,
//     site header and footer, sidebars, forms, scripts, images, and
//     anything marked hidden.
//  2. If the page marks its main content (<main>, role="main", or one
//     <article> far larger than any other) and that part holds at least
//     half of the text left after rule 1, only that part is kept.
//     Otherwise everything left after rule 1 is.
//
// There is no scoring of blocks by link density or class names. Scoring is
// what drops a pricing grid or a table the page plainly shows, and the
// reader then answers from a page with a hole in it; these rules err the
// other way and keep too much.
func extract(r io.Reader, address *url.URL) (extracted, error) {
	// The tree is built from the stream: the page's bytes are never held
	// as one buffer beside it.
	doc, err := html.Parse(r)
	if err != nil {
		return extracted{}, err
	}
	x := extraction{base: address}
	// Asked of the whole document before anything is removed: a page drawn
	// by script loads it from the head, which prune takes away unread.
	x.scripts = find(doc, func(n *html.Node) bool { return n.DataAtom == atom.Script }) != nil
	x.head(doc)
	x.prune(doc, false)

	body := find(doc, func(n *html.Node) bool { return n.DataAtom == atom.Body })
	if body == nil {
		body = doc
	}
	root := body
	if main := x.main(body); main != nil && textLength(main)*2 >= textLength(body) {
		root = main
	}
	if x.title == "" {
		if h1 := find(body, func(n *html.Node) bool { return n.DataAtom == atom.H1 }); h1 != nil {
			x.title = oneLine(innerText(h1))
		}
	}
	// Converted on its own: the converter walks from the node it is given,
	// and a node still attached would let it see its siblings.
	if root.Parent != nil {
		root.Parent.RemoveChild(root)
	}
	out, err := markdown.ConvertNode(root)
	if err != nil {
		return extracted{}, err
	}
	return extracted{Title: x.title, Text: strings.TrimSpace(string(out)), Scripts: x.scripts}, nil
}

type extraction struct {
	base    *url.URL
	title   string
	scripts bool
}

// head reads what the document's head says before prune removes it: the
// title, and a <base href>, which is what relative links are relative to
// when a page sets one.
func (x *extraction) head(doc *html.Node) {
	head := find(doc, func(n *html.Node) bool { return n.DataAtom == atom.Head })
	if head == nil {
		return
	}
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		switch c.DataAtom {
		case atom.Title:
			x.title = oneLine(innerText(c))
		case atom.Base:
			if ref, err := url.Parse(strings.TrimSpace(attribute(c, "href"))); err == nil && ref.String() != "" {
				x.base = x.base.ResolveReference(ref)
			}
		}
	}
}

// prune removes from n everything that is not the page's content and
// rewrites the links that stay. content says n is inside an <article>,
// <main> or <section>, where a <header> is the piece's own heading and not
// the site's banner.
//
// It recurses, which is safe on a hostile page because html.Parse refuses
// a document nested more than 512 elements deep.
func (x *extraction) prune(n *html.Node, content bool) {
	var next *html.Node
	for c := n.FirstChild; c != nil; c = next {
		next = c.NextSibling
		if c.Type == html.CommentNode {
			n.RemoveChild(c)
			continue
		}
		if c.Type != html.ElementNode {
			continue
		}
		if furniture(c, content) {
			n.RemoveChild(c)
			continue
		}
		if c.DataAtom == atom.A {
			x.link(c)
		}
		inside := content
		switch c.DataAtom {
		case atom.Article, atom.Main, atom.Section:
			inside = true
		}
		x.prune(c, inside)
	}
}

// furniture reports whether an element is part of the page's frame rather
// than its content.
func furniture(n *html.Node, content bool) bool {
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Head,
		atom.Nav, atom.Footer, atom.Aside,
		atom.Form, atom.Button, atom.Select, atom.Input, atom.Textarea, atom.Dialog,
		// Pictures and players are nothing to a reader of text, and an
		// image's address is often longer than the sentence beside it.
		atom.Img, atom.Picture, atom.Svg, atom.Canvas, atom.Video, atom.Audio,
		atom.Iframe, atom.Object, atom.Embed:
		return true
	case atom.Header:
		return !content
	}
	for _, a := range n.Attr {
		switch a.Key {
		case "hidden":
			return true
		case "aria-hidden":
			if a.Val == "true" {
				return true
			}
		case "role":
			switch a.Val {
			case "navigation", "banner", "contentinfo", "complementary", "search", "dialog":
				return true
			}
		case "style":
			// Text a browser does not show is text the person approving
			// the call cannot see either; it is the usual place to hide
			// instructions meant for a model.
			if s := strings.ReplaceAll(a.Val, " ", ""); strings.Contains(s, "display:none") || strings.Contains(s, "visibility:hidden") {
				return true
			}
		}
	}
	return false
}

// link makes an anchor's address absolute, or takes it away when it is not
// an address a reader could be sent to: a script, a place on this same
// page, a file on the visitor's disk.
func (x *extraction) link(a *html.Node) {
	for i := range a.Attr {
		if a.Attr[i].Key != "href" {
			continue
		}
		raw := strings.TrimSpace(a.Attr[i].Val)
		ref, err := url.Parse(raw)
		if err != nil || raw == "" || strings.HasPrefix(raw, "#") {
			a.Attr[i].Val = ""
			continue
		}
		resolved := x.base.ResolveReference(ref)
		switch resolved.Scheme {
		case "http", "https", "mailto":
			a.Attr[i].Val = resolved.String()
		default:
			a.Attr[i].Val = ""
		}
	}
}

// main finds the part of the page marked as its main content: <main> or
// role="main", else the largest <article> when it is at least twice the
// size of the next (several articles of like size are a list of posts, and
// the list is the content).
func (x *extraction) main(body *html.Node) *html.Node {
	if marked := find(body, func(n *html.Node) bool {
		return n.DataAtom == atom.Main || attribute(n, "role") == "main"
	}); marked != nil {
		return marked
	}
	var largest *html.Node
	var size, second int
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.DataAtom == atom.Article {
			// Not searched further: an article inside an article is a
			// comment or a quoted post, part of the outer one.
			switch length := textLength(n); {
			case length > size:
				largest, size, second = n, length, size
			case length > second:
				second = length
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(body)
	if largest == nil || size < 2*second {
		return nil
	}
	return largest
}

// find returns the first element under n, n included, that match accepts,
// in document order.
func find(n *html.Node, match func(*html.Node) bool) *html.Node {
	if n.Type == html.ElementNode && match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := find(c, match); found != nil {
			return found
		}
	}
	return nil
}

func attribute(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// textLength is how much text is under n, in bytes, not counting the white
// space HTML is indented with.
func textLength(n *html.Node) int {
	if n.Type == html.TextNode {
		return len(strings.TrimSpace(n.Data))
	}
	total := 0
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		total += textLength(c)
	}
	return total
}

func innerText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
