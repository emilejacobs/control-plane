package cameras_test

import (
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

func TestValidateCamera(t *testing.T) {
	cases := []struct {
		name     string
		label    string
		rtspURL  string
		wantCode string // empty = expect success
	}{
		{"happy", "Drive-thru", "rtsp://user:pass@10.0.0.42:554/stream", ""},
		{"rtsps scheme accepted", "x", "rtsps://host:8322/s", ""},
		{"empty label", "", "rtsp://x", cameras.CodeBadLabel},
		{"whitespace-only label", "   \t", "rtsp://x", cameras.CodeBadLabel},
		{"http scheme rejected", "x", "http://10.0.0.42/", cameras.CodeBadRtspURL},
		{"no scheme rejected", "x", "10.0.0.42/", cameras.CodeBadRtspURL},
		{"empty url rejected", "x", "", cameras.CodeBadRtspURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cameras.ValidateCamera(tc.label, tc.rtspURL)
			if tc.wantCode == "" {
				if err != nil {
					t.Errorf("got error %v, want success", err)
				}
				return
			}
			v, ok := cameras.AsValidation(err)
			if !ok {
				t.Fatalf("got error %v (type %T), want *ValidationError", err, err)
			}
			if v.Code != tc.wantCode {
				t.Errorf("code: got %q, want %q", v.Code, tc.wantCode)
			}
		})
	}
}

// AsValidation returns false for non-ValidationError errors so
// callers can distinguish "validation rejected" from "transient
// failure" cleanly.
func TestAsValidationFalseForOtherErrors(t *testing.T) {
	if _, ok := cameras.AsValidation(errors.New("oops")); ok {
		t.Error("AsValidation should be false for a non-ValidationError")
	}
	if _, ok := cameras.AsValidation(nil); ok {
		t.Error("AsValidation should be false for nil")
	}
}
