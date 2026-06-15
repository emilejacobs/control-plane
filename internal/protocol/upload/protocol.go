// Package upload holds the wire types for the device→S3 captures pipeline
// (issue #8, ADR-030 § 7). The agent can't presign S3 URLs (it only holds an
// mTLS IoT identity), so uploading an artifact is a three-message handshake:
//
//	upload.request   agent → CP   "I have a <kind> artifact of <size>/<type>"
//	upload.url       CP   → agent  a CP-minted s3_key + a 5-min presigned PUT URL
//	upload.complete  agent → CP    "the PUT succeeded for <s3_key>" → CP indexes it
//
// The agent→CP messages ride the cmd-result channel and the CP→agent reply
// rides the cmd channel, so the flow reuses the existing IoT/SQS plumbing
// (no new topic or queue). Both halves depend on this package so they can't
// drift on the wire shape.
package upload

import (
	"fmt"
	"path"
)

// Command/result Type strings carried in the envelope.
const (
	TypeRequest  = "upload.request"
	TypeURL      = "upload.url"
	TypeComplete = "upload.complete"
)

// Capture kinds. Mirror the device_captures.kind CHECK constraint.
const (
	KindSnapshot   = "snapshot"
	KindAudio      = "audio"
	KindTranscript = "transcript"
)

// prefixForKind maps a kind to its top-level S3 prefix (the captures bucket's
// lifecycle rules key off these).
var prefixForKind = map[string]string{
	KindSnapshot:   "snapshots",
	KindAudio:      "audio",
	KindTranscript: "transcripts",
}

// extForContentType maps the content types the producers emit to a file
// extension. Unknown types fall back to .bin so a key is always well-formed.
var extForContentType = map[string]string{
	"image/jpeg":  ".jpg",
	"image/png":   ".png",
	"audio/wav":   ".wav",
	"audio/x-wav": ".wav",
	"text/plain":  ".txt",
}

// Request is the agent→CP ask for a presigned upload URL. SizeBytes is
// advisory (the presigned PUT does not enforce it); the authoritative size is
// reported in Complete after the PUT.
type Request struct {
	Kind        string         `json:"kind"`
	ContentType string         `json:"content_type"`
	SizeBytes   int64          `json:"size_bytes"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// Validate guards the agent's ask before CP mints a key + signs a URL.
func (r Request) Validate() error {
	if _, ok := prefixForKind[r.Kind]; !ok {
		return fmt.Errorf("upload: unknown kind %q", r.Kind)
	}
	if r.ContentType == "" {
		return fmt.Errorf("upload: empty content_type")
	}
	if r.SizeBytes <= 0 {
		return fmt.Errorf("upload: size_bytes must be positive, got %d", r.SizeBytes)
	}
	return nil
}

// URL is the CP→agent grant: the key CP assigned and a short-lived presigned
// PUT URL the agent uploads to. CorrelationID echoes the originating
// upload.request so the agent's uploader can match a grant to the in-flight
// upload — the dispatcher hands a handler only the command args, not the
// envelope, so the id must travel inside the grant body.
type URL struct {
	CorrelationID string `json:"correlation_id"`
	S3Key         string `json:"s3_key"`
	PutURL        string `json:"put_url"`
}

// Complete is the agent→CP confirmation that the PUT landed; CP indexes a
// device_captures row from it.
type Complete struct {
	Kind        string         `json:"kind"`
	S3Key       string         `json:"s3_key"`
	ContentType string         `json:"content_type"`
	SizeBytes   int64          `json:"size_bytes"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// S3Key mints the CP-controlled object key: <prefix>/<deviceID>/<id><ext>. The
// agent never chooses its own key, so a compromised agent can't overwrite
// another device's captures. id is a server-minted unique token (e.g. a uuid).
func S3Key(kind, deviceID, id, contentType string) (string, error) {
	prefix, ok := prefixForKind[kind]
	if !ok {
		return "", fmt.Errorf("upload: unknown kind %q", kind)
	}
	ext := extForContentType[contentType]
	if ext == "" {
		ext = ".bin"
	}
	return path.Join(prefix, deviceID, id+ext), nil
}
