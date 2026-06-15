package camerasnapshot_test

import (
	"encoding/json"
	"testing"

	camerasnapshot "github.com/emilejacobs/control-plane/internal/protocol/camerasnapshot"
)

func TestParseArgs(t *testing.T) {
	a, err := camerasnapshot.ParseArgs(json.RawMessage(`{"camera_id":"cam1","s3_key":"k","put_url":"u"}`))
	if err != nil {
		t.Fatalf("valid args: %v", err)
	}
	if a.CameraID != "cam1" || a.S3Key != "k" || a.PutURL != "u" {
		t.Errorf("parsed = %+v", a)
	}
}

func TestParseArgsRejects(t *testing.T) {
	cases := map[string]string{
		"empty camera_id": `{"camera_id":"","s3_key":"k","put_url":"u"}`,
		"missing s3_key":  `{"camera_id":"cam1","put_url":"u"}`,
		"missing put_url": `{"camera_id":"cam1","s3_key":"k"}`,
		"unknown field":   `{"camera_id":"cam1","s3_key":"k","put_url":"u","extra":1}`,
		"not an object":   `["nope"]`,
	}
	for name, raw := range cases {
		if _, err := camerasnapshot.ParseArgs(json.RawMessage(raw)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
