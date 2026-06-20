package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type fakeSM struct {
	out *secretsmanager.GetSecretValueOutput
	err error
	gotID string
}

func (f *fakeSM) GetSecretValue(_ context.Context, in *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	f.gotID = aws.ToString(in.SecretId)
	return f.out, f.err
}

func TestSecretPasswordFetcherParsesPassword(t *testing.T) {
	sm := &fakeSM{out: &secretsmanager.GetSecretValueOutput{
		SecretString: aws.String(`{"username":"uknomi_admin","password":"s3cr3t-rotated"}`),
	}}
	f := NewSecretPasswordFetcher(sm, "arn:aws:secretsmanager:us-east-1:1:secret:rds!db-x")
	pw, err := f.FetchPassword(context.Background())
	if err != nil {
		t.Fatalf("FetchPassword: %v", err)
	}
	if pw != "s3cr3t-rotated" {
		t.Errorf("password = %q, want s3cr3t-rotated", pw)
	}
	if sm.gotID != "arn:aws:secretsmanager:us-east-1:1:secret:rds!db-x" {
		t.Errorf("requested SecretId = %q", sm.gotID)
	}
}

func TestSecretPasswordFetcherErrors(t *testing.T) {
	cases := []struct {
		name string
		out  *secretsmanager.GetSecretValueOutput
		err  error
	}{
		{"api error", nil, errors.New("access denied")},
		{"no string", &secretsmanager.GetSecretValueOutput{}, nil},
		{"bad json", &secretsmanager.GetSecretValueOutput{SecretString: aws.String("not json")}, nil},
		{"missing password", &secretsmanager.GetSecretValueOutput{SecretString: aws.String(`{"username":"u"}`)}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewSecretPasswordFetcher(&fakeSM{out: tc.out, err: tc.err}, "arn")
			if _, err := f.FetchPassword(context.Background()); err == nil {
				t.Error("expected an error")
			}
		})
	}
}
