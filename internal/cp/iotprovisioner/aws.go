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

// iotClient is the subset of the AWS IoT SDK ProvisionDevice + Revoke use,
// surfaced as an interface so unit tests can inject failures at each step
// without paying for a moto round-trip. *iot.Client satisfies it.
type iotClient interface {
	CreateThing(ctx context.Context, in *iot.CreateThingInput, opts ...func(*iot.Options)) (*iot.CreateThingOutput, error)
	CreateKeysAndCertificate(ctx context.Context, in *iot.CreateKeysAndCertificateInput, opts ...func(*iot.Options)) (*iot.CreateKeysAndCertificateOutput, error)
	AttachThingPrincipal(ctx context.Context, in *iot.AttachThingPrincipalInput, opts ...func(*iot.Options)) (*iot.AttachThingPrincipalOutput, error)
	AttachPolicy(ctx context.Context, in *iot.AttachPolicyInput, opts ...func(*iot.Options)) (*iot.AttachPolicyOutput, error)
	DeleteThing(ctx context.Context, in *iot.DeleteThingInput, opts ...func(*iot.Options)) (*iot.DeleteThingOutput, error)
	DetachThingPrincipal(ctx context.Context, in *iot.DetachThingPrincipalInput, opts ...func(*iot.Options)) (*iot.DetachThingPrincipalOutput, error)
	UpdateCertificate(ctx context.Context, in *iot.UpdateCertificateInput, opts ...func(*iot.Options)) (*iot.UpdateCertificateOutput, error)
	DeleteCertificate(ctx context.Context, in *iot.DeleteCertificateInput, opts ...func(*iot.Options)) (*iot.DeleteCertificateOutput, error)
}

// AWS is the production Provisioner. It mints a per-device thing + cert in
// AWS IoT Core, attaches the supplied policy to the cert, and returns the
// PEM material to the caller. The policy itself is expected to already
// exist (created by Terraform per the Phase 0 / infra setup).
type AWS struct {
	client     iotClient
	policyName string
}

func NewAWS(client *iot.Client, policyName string) *AWS {
	return &AWS{client: client, policyName: policyName}
}

// newAWSWithClient is the unit-test seam — it accepts the iotClient
// interface so the fake in aws_rollback_test.go can drive failure paths.
func newAWSWithClient(client iotClient, policyName string) *AWS {
	return &AWS{client: client, policyName: policyName}
}

func (a *AWS) ProvisionDevice(ctx context.Context, deviceID string) (DeviceCert, error) {
	thingOut, err := a.client.CreateThing(ctx, &iot.CreateThingInput{
		ThingName: aws.String(deviceID),
	})
	if err != nil {
		return DeviceCert{}, fmt.Errorf("create thing: %w", err)
	}

	// rollback is a LIFO stack of cleanup actions, appended after each
	// step succeeds. If a later step fails, the stack runs in reverse to
	// undo the partial provisioning — this is what keeps IoT Core clean
	// after AttachThingPrincipal or AttachPolicy 4xx/5xx (the bug that
	// produced the Wave 0 orphan things).
	var rollback []func()
	committed := false
	defer func() {
		if committed {
			return
		}
		for i := len(rollback) - 1; i >= 0; i-- {
			rollback[i]()
		}
	}()
	rollback = append(rollback, func() {
		_, _ = a.client.DeleteThing(ctx, &iot.DeleteThingInput{ThingName: aws.String(deviceID)})
	})

	keys, err := a.client.CreateKeysAndCertificate(ctx, &iot.CreateKeysAndCertificateInput{
		SetAsActive: true,
	})
	if err != nil {
		return DeviceCert{}, fmt.Errorf("create keys+cert: %w", err)
	}
	certARN := aws.ToString(keys.CertificateArn)
	certID := aws.ToString(keys.CertificateId)
	rollback = append(rollback, func() {
		_, _ = a.client.UpdateCertificate(ctx, &iot.UpdateCertificateInput{
			CertificateId: aws.String(certID),
			NewStatus:     types.CertificateStatusInactive,
		})
		_, _ = a.client.DeleteCertificate(ctx, &iot.DeleteCertificateInput{
			CertificateId: aws.String(certID),
			ForceDelete:   true,
		})
	})

	if _, err := a.client.AttachThingPrincipal(ctx, &iot.AttachThingPrincipalInput{
		ThingName: aws.String(deviceID),
		Principal: aws.String(certARN),
	}); err != nil {
		return DeviceCert{}, fmt.Errorf("attach thing principal: %w", err)
	}
	rollback = append(rollback, func() {
		_, _ = a.client.DetachThingPrincipal(ctx, &iot.DetachThingPrincipalInput{
			ThingName: aws.String(deviceID),
			Principal: aws.String(certARN),
		})
	})

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

	committed = true
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
