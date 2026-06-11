// Package infra defines the AWS CDK Go stacks for the erickaldama.com email project.
package infra

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// NewFoundationStack creates the SP-1 foundation: hosted zone + IAM boundary.
// Fleshed out construct-by-construct in tasks 2–5.
func NewFoundationStack(scope constructs.Construct, id string, props *awscdk.StackProps) awscdk.Stack {
	stack := awscdk.NewStack(scope, jsii.String(id), props)
	return stack
}
