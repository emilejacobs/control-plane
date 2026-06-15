package agentrollout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/emilejacobs/control-plane/internal/protocol/agentmanifest"
)

// S3ManifestSource reads signed release manifests from the agent-dist bucket
// at the key layout CI publishes (.github/workflows/agent-release.yml):
// agent/{version}/manifest.json.
type S3ManifestSource struct {
	client *s3.Client
	bucket string
}

func NewS3ManifestSource(client *s3.Client, bucket string) *S3ManifestSource {
	return &S3ManifestSource{client: client, bucket: bucket}
}

func (s *S3ManifestSource) Manifest(ctx context.Context, version string) (agentmanifest.Manifest, error) {
	key := "agent/" + version + "/manifest.json"
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var noKey *types.NoSuchKey
		if errors.As(err, &noKey) {
			return agentmanifest.Manifest{}, fmt.Errorf("%s: %w", version, ErrVersionNotFound)
		}
		return agentmanifest.Manifest{}, fmt.Errorf("get manifest %s: %w", key, err)
	}
	defer out.Body.Close()

	var m agentmanifest.Manifest
	if err := json.NewDecoder(out.Body).Decode(&m); err != nil {
		return agentmanifest.Manifest{}, fmt.Errorf("decode manifest %s: %w", key, err)
	}
	return m, nil
}

// ListVersions enumerates the published versions in the catalog by listing the
// agent/<version>/ "directories" in the dist bucket (the same key layout
// Manifest reads). It uses a delimited ListObjectsV2 so S3 returns one common
// prefix per version rather than every object. The "latest" alias prefix is
// excluded — it points at a release, it is not itself a selectable version.
func (s *S3ManifestSource) ListVersions(ctx context.Context) ([]string, error) {
	var versions []string
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket:    aws.String(s.bucket),
		Prefix:    aws.String("agent/"),
		Delimiter: aws.String("/"),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list agent versions: %w", err)
		}
		for _, cp := range page.CommonPrefixes {
			// cp.Prefix is like "agent/1.4.1/" — strip the prefix and trailing
			// slash to recover the bare version.
			v := strings.TrimSuffix(strings.TrimPrefix(aws.ToString(cp.Prefix), "agent/"), "/")
			if v == "" || v == "latest" {
				continue
			}
			versions = append(versions, v)
		}
	}
	return versions, nil
}

// S3Presigner is the real Presigner over the agent-dist bucket.
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
