package tui

import (
	"testing"

	"github.com/mikecsmith/ihj/internal/core"
)

func TestDefaultAttachmentViewer(t *testing.T) {
	tests := []struct {
		name string
		att  core.Attachment
		want string
	}{
		{
			name: "mp4 by mime plays with mpv",
			att:  core.Attachment{Filename: "clip.mp4", MIMEType: "video/mp4"},
			want: "mpv --vo=kitty {path}",
		},
		{
			name: "mp4 by extension when mime is generic",
			att:  core.Attachment{Filename: "clip.mp4", MIMEType: "application/octet-stream"},
			want: "mpv --vo=kitty {path}",
		},
		{
			name: "audio plays with mpv",
			att:  core.Attachment{Filename: "note.mp3", MIMEType: "audio/mpeg"},
			want: "mpv --vo=kitty {path}",
		},
		{
			name: "png renders with kitten icat",
			att:  core.Attachment{Filename: "shot.png", MIMEType: "image/png"},
			want: "kitten icat --hold {path}",
		},
		{
			name: "image by extension when mime missing",
			att:  core.Attachment{Filename: "shot.JPG"},
			want: "kitten icat --hold {path}",
		},
		{
			name: "pdf falls back to the OS opener",
			att:  core.Attachment{Filename: "spec.pdf", MIMEType: "application/pdf"},
			want: osOpenCommand(),
		},
		{
			name: "unknown type falls back to the OS opener",
			att:  core.Attachment{Filename: "data.bin", MIMEType: ""},
			want: osOpenCommand(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultAttachmentViewer(tc.att); got != tc.want {
				t.Errorf("defaultAttachmentViewer(%+v) = %q, want %q", tc.att, got, tc.want)
			}
		})
	}
}

func TestAttachmentKind_MIMEBeatsExtension(t *testing.T) {
	// A real video whose filename lacks a useful extension is still detected
	// from its MIME type.
	if got := attachmentKind("video/webm", "recording"); got != attachVideo {
		t.Errorf("attachmentKind(video/webm) = %v, want attachVideo", got)
	}
	// And a misleading MIME type is overridden by nothing — MIME wins first.
	if got := attachmentKind("image/png", "thing.mp4"); got != attachImage {
		t.Errorf("attachmentKind(image/png, thing.mp4) = %v, want attachImage", got)
	}
}
