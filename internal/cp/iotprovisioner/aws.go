package iotprovisioner

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	"github.com/aws/aws-sdk-go-v2/service/iot/types"
)

// AWS is the production Provisioner. It mints a per-device thing + cert in
// AWS IoT Core, attaches the supplied policy to the cert, and returns the
// PEM material to the caller. The policy itself is expected to already
// exist (created by Terraform per the Phase 0 / infra setup).
type AWS struct {
	client     *iot.Client
	policyName string
}

func NewAWS(client *iot.Client, policyName string) *AWS {
	return &AWS{client: client, policyName: policyName}
}

func (a *AWS) ProvisionDevice(ctx context.Context, deviceID string) (DeviceCert, error) {
	thingOut, err := a.client.CreateThing(ctx, &iot.CreateThingInput{
		ThingName: aws.String(deviceID),
	})
	if err != nil {
		return DeviceCert{}, fmt.Errorf("create thing: %w", err)
	}

	keys, err := a.client.CreateKeysAndCertificate(ctx, &iot.CreateKeysAndCertificateInput{
		SetAsActive: true,
	})
	if err != nil {
		_, _ = a.client.DeleteThing(ctx, &iot.DeleteThingInput{ThingName: aws.String(deviceID)})
		return DeviceCert{}, fmt.Errorf("create keys+cert: %w", err)
	}
	certARN := aws.ToString(keys.CertificateArn)

	if _, err := a.client.AttachThingPrincipal(ctx, &iot.AttachThingPrincipalInput{
		ThingName: aws.String(deviceID),
		Principal: aws.String(certARN),
	}); err != nil {
		return DeviceCert{}, fmt.Errorf("attach thing principal: %w", err)
	}

	if _, err := a.client.AttachPolicy(ctx, &iot.AttachPolicyInput{
		PolicyName: aws.String(a.policyName),
		Target:     aws.String(certARN),
	}); err != nil {
		return DeviceCert{}, fmt.Errorf("attach policy: %w", err)
	}

	expiresAt, err := certNotAfter(aws.ToString(keys.CertificatePem))
	if err != nil {
		return DeviceCert{}, fmt.Errorf("parse cert expiry: %w", err)
	}

	return DeviceCert{
		ThingARN:   aws.ToString(thingOut.ThingArn),
		CertARN:    certARN,
		CertPEM:    aws.ToString(keys.CertificatePem),
		PrivKeyPEM: aws.ToString(keys.KeyPair.PrivateKey),
		ExpiresAt:  expiresAt,
	}, nil
}

func (a *AWS) Revoke(ctx context.Context, certARN string) error {
	certID := certIDFromARN(certARN)
	if certID == "" {
		return fmt.Errorf("invalid cert ARN: %q", certARN)
	}
	_, err := a.client.UpdateCertificate(ctx, &iot.UpdateCertificateInput{
		CertificateId: aws.String(certID),
		NewStatus:     types.CertificateStatusInactive,
	})
	if err != nil {
		return fmt.Errorf("update certificate inactive: %w", err)
	}
	return nil
}

// certIDFromARN extracts the cert ID from arn:aws:iot:region:account:cert/<id>.
func certIDFromARN(arn string) string {
	idx := strings.LastIndex(arn, "/")
	if idx < 0 || idx == len(arn)-1 {
		return ""
	}
	return arn[idx+1:]
}

func certNotAfter(certPEM string) (expires time.Time, _ error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return time.Time{}, fmt.Errorf("no PEM block found")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, fmt.Errorf("x509.ParseCertificate: %w", err)
	}
	return parsed.NotAfter, nil
}
