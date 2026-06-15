package upload_test

import (
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/upload"
)

func TestValidateRequestAcceptsEachKind(t *testing.T) {
	for _, kind := range []string{upload.KindSnapshot, upload.KindAudio, upload.KindTranscript} {
		req := upload.Request{Kind: kind, ContentType: "image/jpeg", SizeBytes: 10}
		if err := req.Validate(); err != nil {
			t.Errorf("kind %q should validate: %v", kind, err)
		}
	}
}

func TestValidateRequestRejectsBadInput(t *testing.T) {
	cases := map[string]upload.Request{
		"unknown kind":      {Kind: "video", ContentType: "video/mp4", SizeBytes: 1},
		"empty kind":        {Kind: "", ContentType: "image/jpeg", SizeBytes: 1},
		"empty content type": {Kind: upload.KindSnapshot, ContentType: "", SizeBytes: 1},
		"zero size":         {Kind: upload.KindSnapshot, ContentType: "image/jpeg", SizeBytes: 0},
		"negative size":     {Kind: upload.KindSnapshot, ContentType: "image/jpeg", SizeBytes: -5},
	}
	for name, req := range cases {
		if err := req.Validate(); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
}

// S3Key mints a CP-controlled key under the kind's prefix; the agent never
// chooses where its bytes land. The extension is derived from the content type
// so downloads carry a sensible name.
func TestS3Key(t *testing.T) {
	cases := []struct {
		kind, contentType, want string
	}{
		{upload.KindSnapshot, "image/jpeg", "snapshots/dev-1/cap-9.jpg"},
		{upload.KindAudio, "audio/wav", "audio/dev-1/cap-9.wav"},
		{upload.KindTranscript, "text/plain", "transcripts/dev-1/cap-9.txt"},
		{upload.KindSnapshot, "application/octet-stream", "snapshots/dev-1/cap-9.bin"},
	}
	for _, c := range cases {
		got, err := upload.S3Key(c.kind, "dev-1", "cap-9", c.contentType)
		if err != nil {
			t.Fatalf("S3Key(%q,%q): %v", c.kind, c.contentType, err)
		}
		if got != c.want {
			t.Errorf("S3Key(%q,%q) = %q, want %q", c.kind, c.contentType, got, c.want)
		}
	}
}

func TestS3KeyRejectsUnknownKind(t *testing.T) {
	if _, err := upload.S3Key("video", "dev-1", "cap-9", "video/mp4"); err == nil {
		t.Error("S3Key should reject an unknown kind")
	}
}
