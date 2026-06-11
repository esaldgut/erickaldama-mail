// Package infra defines the AWS CDK Go stacks for the erickaldama.com email project.
package infra

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsroute53"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// NewFoundationStack creates the SP-1 foundation: hosted zone + IAM boundary.
// Fleshed out construct-by-construct in tasks 2–5.
func NewFoundationStack(scope constructs.Construct, id string, props *awscdk.StackProps) awscdk.Stack {
	stack := awscdk.NewStack(scope, jsii.String(id), props)

	// Public hosted zone for erickaldama.com. Route53 auto-creates NS+SOA.
	// CaaAmazon adds a CAA record restricting cert issuance to Amazon (ACM) —
	// safe because the stack is 100% AWS (ACM + SES).
	zone := awsroute53.NewPublicHostedZone(stack, jsii.String("ErickaldamaZone"),
		&awsroute53.PublicHostedZoneProps{
			ZoneName:  jsii.String(DomainName),
			CaaAmazon: jsii.Bool(true),
			Comment:   jsii.String("erickaldama.com email project foundation (SP-1)"),
		})

	// Export the 4 name servers (a list token — join, do NOT dereference in Go)
	// so the human can update the registrar via route53domains:UpdateDomainNameservers.
	awscdk.NewCfnOutput(stack, jsii.String("NameServers"), &awscdk.CfnOutputProps{
		Value:       awscdk.Fn_Join(jsii.String(","), zone.HostedZoneNameServers()),
		Description: jsii.String("Set these 4 NS at the registrar (route53domains update-domain-nameservers)"),
	})
	awscdk.NewCfnOutput(stack, jsii.String("HostedZoneId"), &awscdk.CfnOutputProps{
		Value: zone.HostedZoneId(),
	})

	return stack
}
