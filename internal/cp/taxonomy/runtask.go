package taxonomy

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ECSRunTaskAPI is the subset of *ecs.Client RunTaskInvoker uses. Lets
// tests avoid the full SDK surface; production wires *ecs.Client.
type ECSRunTaskAPI interface {
	RunTask(ctx context.Context, in *ecs.RunTaskInput, optFns ...func(*ecs.Options)) (*ecs.RunTaskOutput, error)
}

// RunTaskConfig describes the ECS task to launch. Cluster + task def
// ARN come from cp-api env (the Terraform-managed task def is the
// authority); subnets and security group are the same VPC fixtures
// the scheduled run uses (audit-mirror precedent).
type RunTaskConfig struct {
	Cluster          string
	TaskDefinition   string
	Subnets          []string
	SecurityGroups   []string
	AssignPublicIP   bool
	ContainerName    string
	DryRun           bool // for future extension; currently unused
}

// AWSRunTaskInvoker is the production adapter behind api.RunTaskInvoker.
// One RunTask call, then surface the resulting task ARN — failures
// bubble up so the cp-api handler can return 5xx and the operator
// can retry.
type AWSRunTaskInvoker struct {
	client ECSRunTaskAPI
	cfg    RunTaskConfig
}

// NewAWSRunTaskInvoker binds an ECS client and task config.
func NewAWSRunTaskInvoker(c ECSRunTaskAPI, cfg RunTaskConfig) *AWSRunTaskInvoker {
	return &AWSRunTaskInvoker{client: c, cfg: cfg}
}

// Run invokes ecs:RunTask. The returned ARN is the task instance the
// caller surfaces to the operator (Settings page renders "Sync started").
func (a *AWSRunTaskInvoker) Run(ctx context.Context) (string, error) {
	out, err := a.client.RunTask(ctx, &ecs.RunTaskInput{
		Cluster:        aws.String(a.cfg.Cluster),
		TaskDefinition: aws.String(a.cfg.TaskDefinition),
		LaunchType:     types.LaunchTypeFargate,
		NetworkConfiguration: &types.NetworkConfiguration{
			AwsvpcConfiguration: &types.AwsVpcConfiguration{
				Subnets:        a.cfg.Subnets,
				SecurityGroups: a.cfg.SecurityGroups,
				AssignPublicIp: assignPublicIP(a.cfg.AssignPublicIP),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("ecs RunTask: %w", err)
	}
	if len(out.Failures) > 0 {
		f := out.Failures[0]
		return "", fmt.Errorf("ecs RunTask failure: %s (%s)",
			aws.ToString(f.Reason), aws.ToString(f.Detail))
	}
	if len(out.Tasks) == 0 {
		return "", fmt.Errorf("ecs RunTask returned no tasks")
	}
	return aws.ToString(out.Tasks[0].TaskArn), nil
}

func assignPublicIP(b bool) types.AssignPublicIp {
	if b {
		return types.AssignPublicIpEnabled
	}
	return types.AssignPublicIpDisabled
}
