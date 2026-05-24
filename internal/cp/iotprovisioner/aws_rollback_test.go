package iotprovisioner

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	"github.com/aws/aws-sdk-go-v2/service/iot/types"
)

// fakeIoT is the unit-test stand-in for the AWS IoT client. Each method
// either returns the corresponding *Err to simulate a failure, or records
// the call (in calls) and returns a benign output. The integration test
// against moto exercises the success path end-to-end; this fake is the
// only way to drive the failure-and-rollback paths without paying for a
// flaky moto error-injection setup.
type fakeIoT struct {
	// failure injection
	createThingErr              error
	createKeysAndCertificateErr error
	attachThingPrincipalErr     error
	attachPolicyErr             error

	// ordered call log — assertions look at this to confirm rollback ran
	calls []string
}

func (f *fakeIoT) CreateThing(_ context.Context, in *iot.CreateThingInput, _ ...func(*iot.Options)) (*iot.CreateThingOutput, error) {
	f.calls = append(f.calls, "CreateThing:"+aws.ToString(in.ThingName))
	if f.createThingErr != nil {
		return nil, f.createThingErr
	}
	return &iot.CreateThingOutput{
		ThingName: in.ThingName,
		ThingArn:  aws.String("arn:aws:iot:us-east-1:000:thing/" + aws.ToString(in.ThingName)),
	}, nil
}

func (f *fakeIoT) CreateKeysAndCertificate(_ context.Context, _ *iot.CreateKeysAndCertificateInput, _ ...func(*iot.Options)) (*iot.CreateKeysAndCertificateOutput, error) {
	f.calls = append(f.calls, "CreateKeysAndCertificate")
	if f.createKeysAndCertificateErr != nil {
		return nil, f.createKeysAndCertificateErr
	}
	return &iot.CreateKeysAndCertificateOutput{
		CertificateArn: aws.String("arn:aws:iot:us-east-1:000:cert/abc123"),
		CertificateId:  aws.String("abc123"),
		CertificatePem: aws.String("-----BEGIN CERTIFICATE-----\nfake-not-parseable\n-----END CERTIFICATE-----\n"),
		KeyPair: &types.KeyPair{
			PrivateKey: aws.String("-----BEGIN PRIVATE KEY-----\nfake\n-----END PRIVATE KEY-----\n"),
		},
	}, nil
}

func (f *fakeIoT) AttachThingPrincipal(_ context.Context, in *iot.AttachThingPrincipalInput, _ ...func(*iot.Options)) (*iot.AttachThingPrincipalOutput, error) {
	f.calls = append(f.calls, "AttachThingPrincipal:"+aws.ToString(in.ThingName))
	if f.attachThingPrincipalErr != nil {
		return nil, f.attachThingPrincipalErr
	}
	return &iot.AttachThingPrincipalOutput{}, nil
}

func (f *fakeIoT) AttachPolicy(_ context.Context, in *iot.AttachPolicyInput, _ ...func(*iot.Options)) (*iot.AttachPolicyOutput, error) {
	f.calls = append(f.calls, "AttachPolicy:"+aws.ToString(in.PolicyName))
	if f.attachPolicyErr != nil {
		return nil, f.attachPolicyErr
	}
	return &iot.AttachPolicyOutput{}, nil
}

func (f *fakeIoT) DeleteThing(_ context.Context, in *iot.DeleteThingInput, _ ...func(*iot.Options)) (*iot.DeleteThingOutput, error) {
	f.calls = append(f.calls, "DeleteThing:"+aws.ToString(in.ThingName))
	return &iot.DeleteThingOutput{}, nil
}

func (f *fakeIoT) DetachThingPrincipal(_ context.Context, in *iot.DetachThingPrincipalInput, _ ...func(*iot.Options)) (*iot.DetachThingPrincipalOutput, error) {
	f.calls = append(f.calls, "DetachThingPrincipal:"+aws.ToString(in.ThingName))
	return &iot.DetachThingPrincipalOutput{}, nil
}

func (f *fakeIoT) UpdateCertificate(_ context.Context, in *iot.UpdateCertificateInput, _ ...func(*iot.Options)) (*iot.UpdateCertificateOutput, error) {
	f.calls = append(f.calls, "UpdateCertificate:"+aws.ToString(in.CertificateId)+":"+string(in.NewStatus))
	return &iot.UpdateCertificateOutput{}, nil
}

func (f *fakeIoT) DeleteCertificate(_ context.Context, in *iot.DeleteCertificateInput, _ ...func(*iot.Options)) (*iot.DeleteCertificateOutput, error) {
	f.calls = append(f.calls, "DeleteCertificate:"+aws.ToString(in.CertificateId))
	return &iot.DeleteCertificateOutput{}, nil
}


// TestProvisionRollsBackWhenAttachThingPrincipalFails covers the failure
// mode that produced the first generation of Wave 0 orphan things: the
// cert was minted but failed to bind to the thing. After this fix, the
// IoT side returns to clean slate — no thing, no cert.
func TestProvisionRollsBackWhenAttachThingPrincipalFails(t *testing.T) {
	fake := &fakeIoT{
		attachThingPrincipalErr: errors.New("simulated AttachThingPrincipal failure"),
	}
	p := newAWSWithClient(fake, "UknomiAgentPolicy")

	_, err := p.ProvisionDevice(context.Background(), "test-device-1")
	if err == nil {
		t.Fatal("ProvisionDevice: got nil, want error")
	}

	// Forward steps were attempted, then rollback fired.
	wantCalls := []string{
		"CreateThing:test-device-1",
		"CreateKeysAndCertificate",
		"AttachThingPrincipal:test-device-1",
		// rollback: revoke + delete cert, delete thing
		"UpdateCertificate:abc123:INACTIVE",
		"DeleteCertificate:abc123",
		"DeleteThing:test-device-1",
	}
	if len(fake.calls) != len(wantCalls) {
		t.Fatalf("call sequence: got %v, want %v", fake.calls, wantCalls)
	}
	for i, want := range wantCalls {
		if fake.calls[i] != want {
			t.Errorf("call[%d]: got %q, want %q", i, fake.calls[i], want)
		}
	}
}

// TestProvisionRollsBackWhenAttachPolicyFails covers the exact failure
// mode that produced the three Wave 0 orphan things on the bench Mac:
// CreateThing + cert + principal-attach succeeded, AttachPolicy 403'd.
// After this fix, that 403 leaves zero residue in IoT Core.
func TestProvisionRollsBackWhenAttachPolicyFails(t *testing.T) {
	fake := &fakeIoT{
		attachPolicyErr: errors.New("simulated AttachPolicy failure"),
	}
	p := newAWSWithClient(fake, "UknomiAgentPolicy")

	_, err := p.ProvisionDevice(context.Background(), "test-device-2")
	if err == nil {
		t.Fatal("ProvisionDevice: got nil, want error")
	}

	wantCalls := []string{
		"CreateThing:test-device-2",
		"CreateKeysAndCertificate",
		"AttachThingPrincipal:test-device-2",
		"AttachPolicy:UknomiAgentPolicy",
		// rollback: detach principal, revoke + delete cert, delete thing
		"DetachThingPrincipal:test-device-2",
		"UpdateCertificate:abc123:INACTIVE",
		"DeleteCertificate:abc123",
		"DeleteThing:test-device-2",
	}
	if len(fake.calls) != len(wantCalls) {
		t.Fatalf("call sequence: got %v, want %v", fake.calls, wantCalls)
	}
	for i, want := range wantCalls {
		if fake.calls[i] != want {
			t.Errorf("call[%d]: got %q, want %q", i, fake.calls[i], want)
		}
	}
}

// TestProvisionRollsBackWhenCreateKeysAndCertificateFails covers the
// pre-existing rollback (thing-only) — verified via the call trace so a
// future refactor cannot quietly drop it.
func TestProvisionRollsBackWhenCreateKeysAndCertificateFails(t *testing.T) {
	fake := &fakeIoT{
		createKeysAndCertificateErr: errors.New("simulated CreateKeys failure"),
	}
	p := newAWSWithClient(fake, "UknomiAgentPolicy")

	_, err := p.ProvisionDevice(context.Background(), "test-device-3")
	if err == nil {
		t.Fatal("ProvisionDevice: got nil, want error")
	}

	wantCalls := []string{
		"CreateThing:test-device-3",
		"CreateKeysAndCertificate",
		"DeleteThing:test-device-3",
	}
	if len(fake.calls) != len(wantCalls) {
		t.Fatalf("call sequence: got %v, want %v", fake.calls, wantCalls)
	}
	for i, want := range wantCalls {
		if fake.calls[i] != want {
			t.Errorf("call[%d]: got %q, want %q", i, fake.calls[i], want)
		}
	}
}

// guard: fakeIoT must satisfy iotClient
var _ iotClient = (*fakeIoT)(nil)
