package tui

import (
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mikecsmith/ihj/internal/core"
)

// attachKind is a coarse classification of an attachment used to pick a viewer.
type attachKind int

const (
	attachOther attachKind = iota
	attachImage
	attachVideo
	attachAudio
)

// attachmentKind classifies an attachment by its MIME type, falling back to the
// filename extension when the MIME type is missing or generic (Jira sometimes
// reports application/octet-stream for uploads).
func attachmentKind(mime, filename string) attachKind {
	mime = strings.ToLower(mime)
	switch {
	case strings.HasPrefix(mime, "image/"):
		return attachImage
	case strings.HasPrefix(mime, "video/"):
		return attachVideo
	case strings.HasPrefix(mime, "audio/"):
		return attachAudio
	}

	switch strings.ToLower(filepath.Ext(filename)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".tiff", ".tif", ".svg", ".avif":
		return attachImage
	case ".mp4", ".mov", ".mkv", ".webm", ".avi", ".m4v", ".wmv", ".flv", ".mpeg", ".mpg":
		return attachVideo
	case ".mp3", ".wav", ".flac", ".ogg", ".oga", ".m4a", ".aac", ".opus":
		return attachAudio
	}
	return attachOther
}

// osOpenCommand returns the platform's "open with the default app" command
// template. Used for attachment types ihj has no in-terminal viewer for
// (PDFs, archives, office docs, …).
func osOpenCommand() string {
	if runtime.GOOS == "darwin" {
		return "open {path}"
	}
	return "xdg-open {path}"
}

// defaultAttachmentViewer returns the built-in viewer command template for an
// attachment when the workspace hasn't configured attachment_view_command.
// Images render inline via kitty's icat, video/audio play in-terminal via mpv,
// and everything else is handed to the OS opener. {path} is substituted with
// the downloaded tempfile by buildViewProcess.
func defaultAttachmentViewer(a core.Attachment) string {
	switch attachmentKind(a.MIMEType, a.Filename) {
	case attachImage:
		return "kitten icat --hold {path}"
	case attachVideo, attachAudio:
		return "mpv --vo=kitty {path}"
	default:
		return osOpenCommand()
	}
}
