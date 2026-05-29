package captures

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func testPresigner(t *testing.T) *S3Presigner {
	t.Helper()
	client := s3.NewPresignClient(s3.New(s3.Options{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKIATEST", "secret", ""),
	}))
	return NewS3Presigner(client, "uknomi-cp-captures")
}

// TestS3PresignerGetURL — a presigned GET carries the bucket + key and a
// SigV4 signature with the requested expiry, and needs no live S3 (signing
// is local).
func TestS3PresignerGetURL(t *testing.T) {
	url, err := testPresigner(t).GetURL(context.Background(), "snapshots/dev/cam1/1.jpg", 5*time.Minute)
	if err != nil {
		t.Fatalf("GetURL: %v", err)
	}
	for _, want := range []string{"uknomi-cp-captures", "snapshots/dev/cam1/1.jpg", "X-Amz-Signature=", "X-Amz-Expires=300"} {
		if !strings.Contains(url, want) {
			t.Errorf("GetURL %q missing %q", url, want)
		}
	}
}

// TestS3PresignerPutURL — a presigned PUT carries the key, the content-type
// constraint, and a signature.
func TestS3PresignerPutURL(t *testing.T) {
	url, err := testPresigner(t).PutURL(context.Background(), "audio/dev/1.wav", "audio/wav", time.Minute)
	if err != nil {
		t.Fatalf("PutURL: %v", err)
	}
	for _, want := range []string{"uknomi-cp-captures", "audio/dev/1.wav", "X-Amz-Signature="} {
		if !strings.Contains(url, want) {
			t.Errorf("PutURL %q missing %q", url, want)
		}
	}
}
