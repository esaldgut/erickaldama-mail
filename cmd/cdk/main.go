package main

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/jsii-runtime-go"

	"erickaldama-mail/internal/infra"
)

func main() {
	defer jsii.Close()

	app := awscdk.NewApp(nil)

	infra.NewFoundationStack(app, "FoundationStack", &awscdk.StackProps{
		Env: env(),
	})

	infra.NewSendingStack(app, "SendingStack", &awscdk.StackProps{
		Env: env(),
	})

	app.Synth(nil)
}

// env pins the target account and region (SP-1 deploys only to ErickSA us-east-1).
func env() *awscdk.Environment {
	return &awscdk.Environment{
		Account: jsii.String("367707589526"),
		Region:  jsii.String("us-east-1"),
	}
}
