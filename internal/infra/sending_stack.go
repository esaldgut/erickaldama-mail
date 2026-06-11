package infra

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// NewSendingStack creates the SP-2 sending identity: SES domain identity + DKIM + custom
// MAIL FROM + DMARC + configuration set (EventBridge) + reputation alarms + send IAM.
// Fleshed out construct-by-construct in tasks 2–5.
func NewSendingStack(scope constructs.Construct, id string, props *awscdk.StackProps) awscdk.Stack {
	stack := awscdk.NewStack(scope, jsii.String(id), props)

	// Tag every resource for attribution (mirror FoundationStack).
	for k, v := range sp2Tags() {
		awscdk.Tags_Of(stack).Add(jsii.String(k), v, nil)
	}

	return stack
}

// sp2Tags mirrors projectTags but marks Subproject=SP-2.
func sp2Tags() map[string]*string {
	return map[string]*string{
		"Project":    strptr("erickaldama-mail"),
		"Subproject": strptr("SP-2"),
		"ManagedBy":  strptr("CDK-Go"),
	}
}
