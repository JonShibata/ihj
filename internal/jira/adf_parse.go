package jira

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/mikecsmith/ihj/internal/document"
)

// issueKeyRE matches a Jira issue key (project key + numeric id) anywhere
// in a string. Used to recover a readable label from inlineCard URLs that
// point at /browse/PROJ-123.
var issueKeyRE = regexp.MustCompile(`\b[A-Z][A-Z0-9_]+-\d+\b`)

// adfNode is the raw JSON shape that Jira's Atlassian Document Format uses.
// We parse into this throwaway struct, then convert to our own AST.
type adfNode struct {
	Type    string            `json:"type"`
	Text    string            `json:"text,omitempty"`
	Marks   []adfMark         `json:"marks,omitempty"`
	Attrs   map[string]any    `json:"attrs,omitempty"`
	Content []json.RawMessage `json:"content,omitempty"`
}

type adfMark struct {
	Type  string            `json:"type"`
	Attrs map[string]string `json:"attrs,omitempty"`
}

// parseADF converts a Jira ADF JSON blob into the internal AST.
// Accepts raw bytes.
func parseADF(data []byte) (*document.Node, error) {
	var raw adfNode
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("adf: invalid json: %w", err)
	}
	return convertADFNode(&raw)
}

func convertADFNode(raw *adfNode) (*document.Node, error) {
	children, err := convertADFChildren(raw.Content)
	if err != nil {
		return nil, err
	}

	switch raw.Type {
	case "doc":
		return document.NewDoc(children...), nil

	case "paragraph":
		return document.NewParagraph(children...), nil

	case "heading":
		level := adfAttrInt(raw.Attrs, "level", 2)
		return document.NewHeading(level, children...), nil

	case "text":
		marks := convertADFMarks(raw.Marks)
		return document.NewStyledText(raw.Text, marks...), nil

	case "hardBreak":
		return document.NewHardBreak(), nil

	case "bulletList":
		return document.NewBulletList(children...), nil

	case "orderedList":
		return document.NewOrderedList(children...), nil

	case "listItem":
		item := document.NewListItem(children...)
		// Detect checkbox text prefix that we injected during ADF rendering
		// (ADF has no native checkbox). Restore it as AST CheckState and
		// strip the text prefix so it doesn't double up on round-trip.
		extractCheckState(item)
		return item, nil

	case "codeBlock":
		lang := adfAttrString(raw.Attrs, "language")
		node := document.NewCodeBlock(lang, "")
		node.Children = children // ADF provides children directly, not wrapped text.
		return node, nil

	case "blockquote":
		return document.NewBlockquote(children...), nil

	case "rule":
		return document.NewRule(), nil

	case "table":
		return document.NewTable(children...), nil

	case "tableRow":
		return document.NewTableRow(children...), nil

	case "tableHeader":
		node := document.NewTableHeader(children...)
		node.ColSpan = max(1, adfAttrInt(raw.Attrs, "colspan", 1))
		node.RowSpan = max(1, adfAttrInt(raw.Attrs, "rowspan", 1))
		return node, nil

	case "tableCell":
		node := document.NewTableCell(children...)
		node.ColSpan = max(1, adfAttrInt(raw.Attrs, "colspan", 1))
		node.RowSpan = max(1, adfAttrInt(raw.Attrs, "rowspan", 1))
		return node, nil

	case "inlineCard":
		// Smart-link: how Jira's editor stores a pasted URL or auto-linked
		// issue key (PROJ-123). No display text in the source — we recover
		// the issue key from the URL when possible, else show the URL.
		text := inlineCardLabel(adfAttrString(raw.Attrs, "url"))
		if text == "" {
			return nil, nil
		}
		return document.NewStyledText(text, document.Link(adfAttrString(raw.Attrs, "url"))), nil

	case "blockCard":
		// Block-level smart-link. Wrap the inlineCard equivalent in a
		// paragraph so it slots in among other block children.
		text := inlineCardLabel(adfAttrString(raw.Attrs, "url"))
		if text == "" {
			return nil, nil
		}
		return document.NewParagraph(
			document.NewStyledText(text, document.Link(adfAttrString(raw.Attrs, "url"))),
		), nil

	case "mention":
		// User mention. attrs.text already includes the leading "@".
		text := adfAttrString(raw.Attrs, "text")
		if text == "" {
			text = "@" + adfAttrString(raw.Attrs, "id")
		}
		return document.NewStyledText(text), nil

	case "mediaSingle", "media", "mediaInline":
		node := document.NewMedia(
			adfAttrString(raw.Attrs, "type"),
			adfAttrString(raw.Attrs, "url"),
			adfAttrString(raw.Attrs, "alt"),
		)
		node.Children = children
		return node, nil

	case "expand", "nestedExpand":
		// A collapsible section. Terminals can't collapse, so surface the
		// title and its content as a callout rather than hiding (dropping) it.
		title := adfAttrString(raw.Attrs, "title")
		if title == "" {
			title = "Details"
		}
		return newCallout("▸ "+title, children), nil

	case "panel":
		// Info/note/warning/success/error callout. Render as a blockquote,
		// labelled by panel type when known.
		return newCallout(panelLabel(adfAttrString(raw.Attrs, "panelType")), children), nil

	default:
		// Unknown node types: preserve children so content isn't silently
		// lost. Block-level children can't be wrapped in a paragraph — the
		// Markdown renderer emits only inline nodes from a paragraph and would
		// drop them (this is the bug that hid ADF `expand` sections). Route
		// block content into a blockquote callout instead.
		if len(children) == 0 {
			return nil, nil
		}
		if containsBlock(children) {
			return newCallout("", children), nil
		}
		return document.NewParagraph(children...), nil
	}
}

// newCallout wraps block content in a blockquote, optionally prefixed with a
// bold title line. Used for ADF container nodes a terminal cannot render as
// truly collapsible or side-panelled (expand, nestedExpand, panel, and unknown
// block containers): the content is set off as a callout rather than dropped.
// An empty title yields a plain quoted callout.
func newCallout(title string, children []*document.Node) *document.Node {
	kids := make([]*document.Node, 0, len(children)+1)
	if title != "" {
		kids = append(kids, document.NewParagraph(document.NewStyledText(title, document.Bold())))
	}
	kids = append(kids, children...)
	return document.NewBlockquote(kids...)
}

// panelLabel maps an ADF panel's panelType to a human label for the callout
// title. An unknown or empty panelType yields no label (a plain quoted
// callout).
func panelLabel(panelType string) string {
	switch panelType {
	case "info":
		return "Info"
	case "note":
		return "Note"
	case "success":
		return "Success"
	case "warning":
		return "Warning"
	case "error":
		return "Error"
	default:
		return ""
	}
}

// containsBlock reports whether any node is block-level (anything other than
// inline text or a hard break). Block children cannot live inside a paragraph:
// the Markdown renderer emits only inline nodes from a paragraph, so wrapping
// block content in a paragraph silently drops it. Callers route such content
// into a blockquote instead.
func containsBlock(nodes []*document.Node) bool {
	for _, n := range nodes {
		if n.Type != document.NodeText && n.Type != document.NodeHardBreak {
			return true
		}
	}
	return false
}

func convertADFChildren(raw []json.RawMessage) ([]*document.Node, error) {
	var children []*document.Node
	for _, r := range raw {
		var child adfNode
		if err := json.Unmarshal(r, &child); err != nil {
			continue
		}
		node, err := convertADFNode(&child)
		if err != nil {
			continue
		}
		if node != nil {
			children = append(children, node)
		}
	}
	return children, nil
}

func convertADFMarks(raw []adfMark) []document.Mark {
	if len(raw) == 0 {
		return nil
	}
	marks := make([]document.Mark, 0, len(raw))
	for _, m := range raw {
		mark, ok := convertADFMark(m)
		if ok {
			marks = append(marks, mark)
		}
	}
	return marks
}

func convertADFMark(m adfMark) (document.Mark, bool) {
	switch m.Type {
	case "strong":
		return document.Bold(), true
	case "em":
		return document.Italic(), true
	case "code":
		return document.Code(), true
	case "strike":
		return document.Strike(), true
	case "underline":
		return document.Underline(), true
	case "link":
		href := ""
		if m.Attrs != nil {
			href = m.Attrs["href"]
		}
		return document.Link(href), true
	case "textColor":
		color := ""
		if m.Attrs != nil {
			color = m.Attrs["color"]
		}
		return document.TextColor(color), true
	case "subsup":
		if m.Attrs != nil && m.Attrs["type"] == "sub" {
			return document.Mark{Type: document.MarkSubscript}, true
		}
		return document.Mark{Type: document.MarkSuperscript}, true
	default:
		return document.Mark{}, false
	}
}

func adfAttrString(attrs map[string]any, key string) string {
	if attrs == nil {
		return ""
	}
	v, ok := attrs[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func adfAttrInt(attrs map[string]any, key string, fallback int) int {
	if attrs == nil {
		return fallback
	}
	v, ok := attrs[key]
	if !ok {
		return fallback
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return fallback
	}
}

// inlineCardLabel produces a display label for a smart-link URL. When the
// URL is a Jira /browse/<KEY> link we return just the key; otherwise we
// fall back to the URL's last path segment, then the full URL. Empty in,
// empty out (callers drop the node).
func inlineCardLabel(url string) string {
	if url == "" {
		return ""
	}
	if k := issueKeyRE.FindString(url); k != "" {
		return k
	}
	if base := path.Base(url); base != "" && base != "/" && base != "." {
		return base
	}
	return url
}

// extractCheckState detects a "[ ] " or "[x] " text prefix in a listItem's
// first paragraph and promotes it to the ListItem's CheckState field. This
// reverses the ADF rendering workaround (ADF has no native checkbox support).
func extractCheckState(item *document.Node) {
	if len(item.Children) == 0 {
		return
	}
	para := item.Children[0]
	if para.Type != document.NodeParagraph || len(para.Children) == 0 {
		return
	}
	first := para.Children[0]
	if first.Type != document.NodeText {
		return
	}
	switch {
	case strings.HasPrefix(first.Text, "[ ] "):
		checked := false
		item.CheckState = &checked
		first.Text = strings.TrimPrefix(first.Text, "[ ] ")
		if first.Text == "" {
			para.Children = para.Children[1:]
		}
	case strings.HasPrefix(first.Text, "[x] "):
		checked := true
		item.CheckState = &checked
		first.Text = strings.TrimPrefix(first.Text, "[x] ")
		if first.Text == "" {
			para.Children = para.Children[1:]
		}
	}
}
