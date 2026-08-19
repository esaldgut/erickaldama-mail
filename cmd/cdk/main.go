package main

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/jsii-runtime-go"

	"erickaldama-mail/internal/infra"
)

func main() {
	defer jsii.Close()

	app := awscdk.NewApp(nil)

	// Env resolved ONCE from CDK_DEFAULT_ACCOUNT/CDK_DEFAULT_REGION (from the OIDC-assumed credentials in CI,
	// or --profile locally; placeholder offline). Single source of truth — no account literal in this file.
	// Injected to all 5 stacks via EnvStackProps (uniform threading; the 5 signatures are not homogeneous).
	e := infra.NewEnv()

	infra.NewFoundationStack(app, "FoundationStack", &infra.EnvStackProps{
		StackProps: &awscdk.StackProps{Env: cdkEnv(e)},
		Env:        e,
	})

	infra.NewSendingStack(app, "SendingStack", &infra.EnvStackProps{
		StackProps: &awscdk.StackProps{Env: cdkEnv(e)},
		Env:        e,
	})

	storage, rawBucket := infra.NewMailStorageStack(app, "MailStorageStack", &infra.EnvStackProps{
		StackProps: &awscdk.StackProps{Env: cdkEnv(e)},
		Env:        e,
	})
	_ = storage

	infra.NewReceivingStack(app, "ReceivingStack", &infra.EnvStackProps{
		StackProps: &awscdk.StackProps{Env: cdkEnv(e)},
		Env:        e,
	}, rawBucket)

	infra.NewCdStack(app, "CdStack", &infra.EnvStackProps{
		StackProps: &awscdk.StackProps{
			Env:                 cdkEnv(e),
			PermissionsBoundary: awscdk.PermissionsBoundary_FromName(jsii.String(infra.BoundaryManagedPolicyName)),
		},
		Env: e,
	})

	app.Synth(nil)
}

// cdkEnv builds the CDK deploy environment from the resolved Env (account/region come from
// CDK_DEFAULT_ACCOUNT/REGION at synth, not from a hardcoded literal — NDA/attack-surface reduction).
func cdkEnv(e infra.Env) *awscdk.Environment {
	return &awscdk.Environment{
		Account: jsii.String(e.Account),
		Region:  jsii.String(e.Region),
	}
}
