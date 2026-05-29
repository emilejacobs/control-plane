// Package captures owns the device→S3 binary-artifact pipeline's CP-side
// helpers (issue #8): pre-signed S3 URLs for the agent's upload PUT and the
// dashboard's download GET. The bytes never transit cp-api — it only signs
// short-lived URLs the agent and browser use directly against S3.
package captures

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Presigner mints short-lived S3 URLs. The handlers + cmd flow depend on this
// interface; tests use a fake, production uses S3Presigner.
type Presigner interface {
	// GetURL signs a download URL for an existing object.
	GetURL(ctx context.Context, key string, expiry time.Duration) (string, error)
	// PutURL signs an upload URL that pins the Content-Type the agent must send.
	PutURL(ctx context.Context, key, contentType string, expiry time.Duration) (string, error)
}

// S3Presigner is the real Presigner over the captures bucket.
type S3Presigner struct {
	client *s3.PresignClient
	bucket string
}

func NewS3Presigner(client *s3.PresignClient, bucket string) *S3Presigner {
	return &S3Presigner{client: client, bucket: bucket}
}

func (p *S3Presigner) GetURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	req, err := p.client.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(p.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("presign get %s: %w", key, err)
	}
	return req.URL, nil
}

func (p *S3Presigner) PutURL(ctx context.Context, key, contentType string, expiry time.Duration) (string, error) {
	req, err := p.client.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(p.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("presign put %s: %w", key, err)
	}
	return req.URL, nil
}
