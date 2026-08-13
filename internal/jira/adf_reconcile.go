package jira

import (
	"encoding/json"
	"strings"

	"github.com/mikecsmith/ihj/internal/core"
	"github.com/mikecsmith/ihj/internal/document"
)

// descriptionADF builds the ADF payload for a description write. When the
// original native ADF is available it reconciles block-by-block, reusing
// untouched blocks verbatim; otherwise it falls back to a full render from the
// edited AST.
func descriptionADF(changes *core.Changes) map[string]any {
	if raw := rawADF(changes.DescriptionOriginal); len(raw) > 0 {
		if merged, err := reconcileADF(raw, changes.Description); err == nil {
			return merged
		}
	}
	return renderADFValue(changes.Description)
}

// rawADF coerces an opaque provider payload back to raw ADF bytes.
func rawADF(v any) []byte {
	switch b := v.(type) {
	case json.RawMessage:
		return b
	case []byte:
		return b
	default:
		return nil
	}
}

// reconcileADF builds the description ADF for a write while preserving
// untouched content verbatim. Each top-level block of the edited AST whose
// Markdown matches an original top-level block is emitted from the ORIGINAL
// ADF byte-for-byte, so rich constructs that Markdown cannot represent —
// tables (header rows), smart-links, expand/collapse sections, panels, text
// colour — survive edits made elsewhere in the description. Only blocks the
// user actually changed (or added) are re-serialized from the lossy Markdown
// round-trip; removed blocks are dropped.
//
// Matching is by rendered-Markdown equality, with a per-chunk FIFO queue so
// repeated identical blocks pair up in document order. Empty renderings are
// never matched (they would pair unrelated blocks).
func reconcileADF(originalRaw []byte, edited *document.Node) (map[string]any, error) {
	var origDoc adfNode
	if err := json.Unmarshal(originalRaw, &origDoc); err != nil {
		return nil, err
	}

	origBlocks := origDoc.Content
	byChunk := make(map[string][]int, len(origBlocks))
	for i, raw := range origBlocks {
		if chunk := blockMarkdownFromADF(raw); chunk != "" {
			byChunk[chunk] = append(byChunk[chunk], i)
		}
	}

	var children []*document.Node
	if edited != nil {
		children = edited.Children
	}

	content := make([]any, 0, len(children))
	for _, block := range children {
		chunk := blockMarkdownFromAST(block)
		if chunk != "" {
			if idxs := byChunk[chunk]; len(idxs) > 0 {
				idx := idxs[0]
				byChunk[chunk] = idxs[1:]
				var reused any
				if err := json.Unmarshal(origBlocks[idx], &reused); err == nil {
					content = append(content, reused) // verbatim original
					continue
				}
			}
		}
		if rendered := renderADFNode(block); rendered != nil {
			content = append(content, rendered)
		}
	}

	return map[string]any{
		"version": 1,
		"type":    "doc",
		"content": content,
	}, nil
}

// blockMarkdownFromADF renders a single raw ADF block to the Markdown chunk it
// would occupy in the editor buffer, so it can be matched against edited blocks.
func blockMarkdownFromADF(raw json.RawMessage) string {
	var n adfNode
	if err := json.Unmarshal(raw, &n); err != nil {
		return ""
	}
	node, err := convertADFNode(&n)
	if err != nil || node == nil {
		return ""
	}
	return blockMarkdownFromAST(node)
}

// blockMarkdownFromAST renders a single AST block to a trimmed, stabilized
// Markdown chunk used as the block-matching key. It renders the block, then
// re-parses and re-renders once: the original ADF side computes its chunk from
// a first render, while the edited side arrives via the editor (already one
// round-trip in), and some blocks — notably blockquotes wrapping tables or code
// — do not render identically on the first pass. Putting both sides through the
// same extra round-trip cancels that difference so unchanged blocks match.
func blockMarkdownFromAST(block *document.Node) string {
	if block == nil {
		return ""
	}
	md := document.RenderMarkdown(document.NewDoc(block))
	if reparsed, err := document.ParseMarkdownString(md); err == nil {
		md = document.RenderMarkdown(reparsed)
	}
	return strings.TrimSpace(md)
}
