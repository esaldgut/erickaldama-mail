package infra

import (
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
	"github.com/aws/jsii-runtime-go"
)

// synth builds the stack and returns the assertion Template. No AWS calls.
func synth(t *testing.T) assertions.Template {
	t.Helper()
	app := awscdk.NewApp(nil)
	stack := NewFoundationStack(app, "TestStack", nil)
	return assertions.Template_FromStack(stack, nil)
}

func TestHostedZone(t *testing.T) {
	template := synth(t)

	// Exactly one public hosted zone, named with the trailing dot Route53 adds.
	template.ResourceCountIs(jsii.String("AWS::Route53::HostedZone"), jsii.Number(1))
	template.HasResourceProperties(jsii.String("AWS::Route53::HostedZone"), map[string]interface{}{
		"Name": "erickaldama.com.",
	})

	// CAA record restricting issuance to Amazon (ACM) — foundation hardening.
	template.ResourceCountIs(jsii.String("AWS::Route53::RecordSet"), jsii.Number(1))
	template.HasResourceProperties(jsii.String("AWS::Route53::RecordSet"), map[string]interface{}{
		"Type": "CAA",
	})
}
