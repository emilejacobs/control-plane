package prconfig_test

import (
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/prconfig"
)

func TestValidate(t *testing.T) {
	valid := prconfig.Config{
		CameraID: "0",
		Region:   "us-az",
		Webhooks: []prconfig.Webhook{{Name: "prod", URL: "https://api-flask.uknomi.com/x", Enabled: true, Image: true}},
	}
	if err := prconfig.Validate(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cases := map[string]prconfig.Config{
		"empty region":      {CameraID: "0", Region: ""},
		"bad region format": {CameraID: "0", Region: "US AZ"},
		"empty camera_id":   {CameraID: "", Region: "us-az"},
		"webhook no name": {CameraID: "0", Region: "us-az",
			Webhooks: []prconfig.Webhook{{Name: "", URL: "https://x.com/y"}}},
		"webhook bad url": {CameraID: "0", Region: "us-az",
			Webhooks: []prconfig.Webhook{{Name: "prod", URL: "ftp://nope"}}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if err := prconfig.Validate(c); err == nil {
				t.Errorf("expected validation error for %s, got nil", name)
			}
		})
	}
}
