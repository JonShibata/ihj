package document

import "strings"

// Links walks the AST in document order and returns every external web URL
// it carries as a hyperlink: the href of each MarkLink on a text span. Only
// http/https URLs are returned so provider-internal media references (media
// IDs, signed attachment URLs) don't leak in. Order is preserved and
// duplicates are kept; callers dedupe if they need to. Returns nil for a
// nil node.
func Links(n *Node) []string {
	var out []string
	var walk func(*Node)
	walk = func(node *Node) {
		if node == nil {
			return
		}
		for _, m := range node.Marks {
			if m.Type == MarkLink && isWebURL(m.Attrs["href"]) {
				out = append(out, m.Attrs["href"])
			}
		}
		for _, c := range node.Children {
			walk(c)
		}
	}
	walk(n)
	return out
}

// isWebURL reports whether s is an http or https URL.
func isWebURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
