package links

import (
	"html"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Markdown traverses actual CommonMark links, excluding code and image ASTs.
// An image inside an outer link contributes the outer link, as in Rex.
func Markdown(source []byte) []Item {
	root := goldmark.New().Parser().Parse(text.NewReader(source))
	var links []Item
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if _, ok := n.(*ast.Image); ok {
			return ast.WalkSkipChildren, nil
		}
		var href, title string
		switch node := n.(type) {
		case *ast.Link:
			href = string(util.UnescapePunctuations(node.Destination))
			title = string(node.Text(source))
		case *ast.AutoLink:
			href = string(node.URL(source))
			title = string(node.Label(source))
		default:
			return ast.WalkContinue, nil
		}
		line := 1
		for parent := n.Parent(); parent != nil; parent = parent.Parent() {
			if parent.Type() == ast.TypeBlock && parent.Lines().Len() > 0 {
				line += strings.Count(string(source[:parent.Lines().At(0).Start]), "\n")
				break
			}
		}
		href = html.UnescapeString(href)
		title = html.UnescapeString(title)
		links = append(links, Item{Target: href, Title: title, Line: line})
		return ast.WalkSkipChildren, nil
	})
	return links
}
