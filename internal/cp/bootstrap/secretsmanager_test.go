package bootstrap

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// fakeSecrets is a stand-in for the Secrets Manager client. It records the
// secret id requested and returns a preset value.
type fakeSecrets struct {
	value string
	gotID string
}

func (f *fakeSecrets) GetSecretValue(
	_ context.Context,
	in *secretsmanager.GetSecretValueInput,
	_ ...func(*secretsmanager.Options),
) (*secretsmanager.GetSecretValueOutput, error) {
	f.gotID = *in.SecretId
	return &secretsmanager.GetSecretValueOutput{SecretString: &f.value}, nil
}

func TestSecretsManagerLoaderReturnsSecretString(t *testing.T) {
	sm := &fakeSecrets{value: "the-bootstrap-key"}
	loader := NewSecretsManagerLoader(sm, "uknomi/cp/bootstrap-key")

	got, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != "the-bootstrap-key" {
		t.Errorf("Load: got %q want %q", got, "the-bootstrap-key")
	}
	if sm.gotID != "uknomi/cp/bootstrap-key" {
		t.Errorf("requested secret id %q want %q", sm.gotID, "uknomi/cp/bootstrap-key")
	}
}
