package jira

import (
	"strings"
	"testing"

	"github.com/mikecsmith/ihj/internal/document"
)

func TestParseADF_InlineCard_IssueKey(t *testing.T) {
	in := []byte(`{
		"version": 1,
		"type": "doc",
		"content": [{
			"type": "paragraph",
			"content": [
				{"type": "text", "text": "see "},
				{"type": "inlineCard", "attrs": {"url": "https://example.atlassian.net/browse/PROJ-123"}}
			]
		}]
	}`)

	node, err := parseADF(in)
	if err != nil {
		t.Fatalf("parseADF: %v", err)
	}

	para := node.Children[0]
	if got := len(para.Children); got != 2 {
		t.Fatalf("paragraph children = %d; want 2", got)
	}
	link := para.Children[1]
	if link.Type != document.NodeText {
		t.Fatalf("inlineCard converted to %v; want NodeText", link.Type)
	}
	if link.Text != "PROJ-123" {
		t.Errorf("text = %q; want %q", link.Text, "PROJ-123")
	}
	if len(link.Marks) != 1 || link.Marks[0].Type != document.MarkLink {
		t.Fatalf("marks = %+v; want one MarkLink", link.Marks)
	}
	if got := link.Marks[0].Attrs["href"]; got != "https://example.atlassian.net/browse/PROJ-123" {
		t.Errorf("href = %q; want browse URL", got)
	}
}

func TestParseADF_InlineCard_NonIssueURL(t *testing.T) {
	in := []byte(`{
		"version": 1,
		"type": "doc",
		"content": [{
			"type": "paragraph",
			"content": [
				{"type": "inlineCard", "attrs": {"url": "https://example.com/docs/page"}}
			]
		}]
	}`)

	node, err := parseADF(in)
	if err != nil {
		t.Fatalf("parseADF: %v", err)
	}
	link := node.Children[0].Children[0]
	if link.Text != "page" {
		t.Errorf("text = %q; want %q (last URL segment)", link.Text, "page")
	}
	if link.Marks[0].Attrs["href"] != "https://example.com/docs/page" {
		t.Errorf("href lost")
	}
}

func TestParseADF_BlockCard(t *testing.T) {
	in := []byte(`{
		"version": 1,
		"type": "doc",
		"content": [{
			"type": "blockCard",
			"attrs": {"url": "https://example.atlassian.net/browse/PROJ-7"}
		}]
	}`)

	node, err := parseADF(in)
	if err != nil {
		t.Fatalf("parseADF: %v", err)
	}
	if len(node.Children) != 1 {
		t.Fatalf("doc children = %d; want 1", len(node.Children))
	}
	para := node.Children[0]
	if para.Type != document.NodeParagraph {
		t.Fatalf("blockCard wrapped as %v; want NodeParagraph", para.Type)
	}
	link := para.Children[0]
	if link.Text != "PROJ-7" || len(link.Marks) != 1 || link.Marks[0].Type != document.MarkLink {
		t.Errorf("link = {%q, %+v}; want PROJ-7 + MarkLink", link.Text, link.Marks)
	}
}

func TestParseADF_Mention(t *testing.T) {
	in := []byte(`{
		"version": 1,
		"type": "doc",
		"content": [{
			"type": "paragraph",
			"content": [
				{"type": "mention", "attrs": {"id": "557058:abc", "text": "@Jane Doe"}}
			]
		}]
	}`)

	node, err := parseADF(in)
	if err != nil {
		t.Fatalf("parseADF: %v", err)
	}
	mention := node.Children[0].Children[0]
	if mention.Type != document.NodeText {
		t.Fatalf("mention type = %v; want NodeText", mention.Type)
	}
	if mention.Text != "@Jane Doe" {
		t.Errorf("text = %q; want %q", mention.Text, "@Jane Doe")
	}
}

func TestParseADF_Expand_PreservesTitleAndContent(t *testing.T) {
	in := []byte(`{
		"version": 1,
		"type": "doc",
		"content": [{
			"type": "expand",
			"attrs": {"title": "Original AI generated ticket content"},
			"content": [
				{"type": "heading", "attrs": {"level": 2}, "content": [{"type": "text", "text": "Summary"}]},
				{"type": "paragraph", "content": [{"type": "text", "text": "hidden body text"}]}
			]
		}]
	}`)

	node, err := parseADF(in)
	if err != nil {
		t.Fatalf("parseADF: %v", err)
	}
	if len(node.Children) != 1 {
		t.Fatalf("doc children = %d; want 1", len(node.Children))
	}
	callout := node.Children[0]
	if callout.Type != document.NodeBlockquote {
		t.Fatalf("expand rendered as %v; want NodeBlockquote", callout.Type)
	}

	md := document.RenderMarkdown(node)
	for _, want := range []string{"Original AI generated ticket content", "Summary", "hidden body text"} {
		if !strings.Contains(md, want) {
			t.Errorf("rendered markdown missing %q\n---\n%s", want, md)
		}
	}
}

func TestParseADF_Panel_PreservesContent(t *testing.T) {
	in := []byte(`{
		"version": 1,
		"type": "doc",
		"content": [{
			"type": "panel",
			"attrs": {"panelType": "warning"},
			"content": [{"type": "paragraph", "content": [{"type": "text", "text": "heads up"}]}]
		}]
	}`)

	node, err := parseADF(in)
	if err != nil {
		t.Fatalf("parseADF: %v", err)
	}
	if node.Children[0].Type != document.NodeBlockquote {
		t.Fatalf("panel rendered as %v; want NodeBlockquote", node.Children[0].Type)
	}
	md := document.RenderMarkdown(node)
	for _, want := range []string{"Warning", "heads up"} {
		if !strings.Contains(md, want) {
			t.Errorf("rendered markdown missing %q\n---\n%s", want, md)
		}
	}
}

// TestParseADF_ContainerNodesNeverDropText guards the whole class of bug that
// hid the "Original AI generated ticket content" expand: any block-container
// node type — recognised or not — must surface its inner text, never silently
// swallow it by wrapping block children in a paragraph.
func TestParseADF_ContainerNodesNeverDropText(t *testing.T) {
	// The distinctive text lives one block level deep inside each container.
	containers := []string{
		"expand", "nestedExpand", "panel",
		"taskList", "decisionList", "layoutSection", "layoutColumn",
		"someFutureUnknownNode",
	}
	for _, typ := range containers {
		t.Run(typ, func(t *testing.T) {
			in := []byte(`{
				"version": 1,
				"type": "doc",
				"content": [{
					"type": "` + typ + `",
					"content": [{"type": "paragraph", "content": [{"type": "text", "text": "MUSTSURVIVE"}]}]
				}]
			}`)
			node, err := parseADF(in)
			if err != nil {
				t.Fatalf("parseADF: %v", err)
			}
			if md := document.RenderMarkdown(node); !strings.Contains(md, "MUSTSURVIVE") {
				t.Errorf("%s dropped its block content\n---\n%s", typ, md)
			}
		})
	}
}

func TestParseADF_InlineCard_EmptyURLDropped(t *testing.T) {
	in := []byte(`{
		"version": 1,
		"type": "doc",
		"content": [{
			"type": "paragraph",
			"content": [
				{"type": "text", "text": "before"},
				{"type": "inlineCard", "attrs": {}},
				{"type": "text", "text": "after"}
			]
		}]
	}`)

	node, err := parseADF(in)
	if err != nil {
		t.Fatalf("parseADF: %v", err)
	}
	para := node.Children[0]
	if got := len(para.Children); got != 2 {
		t.Fatalf("paragraph children = %d; want 2 (empty inlineCard dropped)", got)
	}
}
