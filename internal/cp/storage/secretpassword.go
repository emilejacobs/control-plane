package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// SecretsManagerGetter is the subset of the Secrets Manager client the DB
// password fetcher needs (injected for testability).
type SecretsManagerGetter interface {
	GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// secretPasswordFetcher reads the `password` field out of the RDS-managed
// secret's JSON each time it is asked, so a rotation is reflected as soon as the
// cache in NewPool expires.
type secretPasswordFetcher struct {
	client SecretsManagerGetter
	arn    string
}

// NewSecretPasswordFetcher returns a PasswordFetcher backed by the RDS-managed
// master secret (the AWS-generated rds!db-… secret). secretARN is its full ARN.
func NewSecretPasswordFetcher(client SecretsManagerGetter, secretARN string) PasswordFetcher {
	return &secretPasswordFetcher{client: client, arn: secretARN}
}

func (f *secretPasswordFetcher) FetchPassword(ctx context.Context) (string, error) {
	out, err := f.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(f.arn)})
	if err != nil {
		return "", fmt.Errorf("get db secret: %w", err)
	}
	if out.SecretString == nil {
		return "", errors.New("db secret has no string value")
	}
	var v struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(*out.SecretString), &v); err != nil {
		return "", fmt.Errorf("parse db secret json: %w", err)
	}
	if v.Password == "" {
		return "", errors.New("db secret json missing password")
	}
	return v.Password, nil
}
