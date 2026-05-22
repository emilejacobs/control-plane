package bootstrap

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// secretsGetter is the slice of the Secrets Manager API the loader needs —
// narrowed to one method so tests can fake it.
type secretsGetter interface {
	GetSecretValue(
		ctx context.Context,
		in *secretsmanager.GetSecretValueInput,
		optFns ...func(*secretsmanager.Options),
	) (*secretsmanager.GetSecretValueOutput, error)
}

// SecretsManagerLoader loads the bootstrap key from an AWS Secrets Manager
// secret — the store of record per ADR-017.
type SecretsManagerLoader struct {
	client   secretsGetter
	secretID string
}

// NewSecretsManagerLoader returns a loader for the given secret id (e.g.
// "uknomi/cp/bootstrap-key").
func NewSecretsManagerLoader(client secretsGetter, secretID string) *SecretsManagerLoader {
	return &SecretsManagerLoader{client: client, secretID: secretID}
}

// Load fetches the secret's current string value.
func (l *SecretsManagerLoader) Load(ctx context.Context) (string, error) {
	out, err := l.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &l.secretID,
	})
	if err != nil {
		return "", fmt.Errorf("get secret %q: %w", l.secretID, err)
	}
	if out.SecretString == nil {
		return "", fmt.Errorf("secret %q has no string value", l.secretID)
	}
	return *out.SecretString, nil
}
