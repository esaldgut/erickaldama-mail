package infra

import (
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
	"github.com/aws/jsii-runtime-go"
)

func synthSending(t *testing.T) assertions.Template {
	t.Helper()
	app := awscdk.NewApp(nil)
	stack := NewSendingStack(app, "TestSending", nil)
	return assertions.Template_FromStack(stack, nil)
}

func TestEmailIdentity(t *testing.T) {
	template := synthSending(t)

	template.ResourceCountIs(jsii.String("AWS::SES::EmailIdentity"), jsii.Number(1))
	template.HasResourceProperties(jsii.String("AWS::SES::EmailIdentity"),
		assertions.Match_ObjectLike(&map[string]interface{}{
			"EmailIdentity":         "erickaldama.com",
			"DkimSigningAttributes": map[string]interface{}{"NextSigningKeyLength": "RSA_2048_BIT"},
			"MailFromAttributes": map[string]interface{}{
				"MailFromDomain":      "mail.erickaldama.com",
				"BehaviorOnMxFailure": "USE_DEFAULT_VALUE",
			},
			"FeedbackAttributes": map[string]interface{}{"EmailForwardingEnabled": false},
		}))

	template.ResourceCountIs(jsii.String("AWS::SES::ConfigurationSet"), jsii.Number(1))
	template.HasResourceProperties(jsii.String("AWS::SES::ConfigurationSet"),
		map[string]interface{}{"Name": "mail-config"})

	// 6 record sets: 3 DKIM CNAME (auto) + MAIL FROM MX (auto) + MAIL FROM SPF TXT (auto) + DMARC TXT (manual).
	template.ResourceCountIs(jsii.String("AWS::Route53::RecordSet"), jsii.Number(6))
	template.HasResourceProperties(jsii.String("AWS::Route53::RecordSet"),
		assertions.Match_ObjectLike(&map[string]interface{}{
			"Type": "TXT", "Name": "_dmarc.erickaldama.com.",
			"ResourceRecords": []interface{}{`"v=DMARC1; p=none; rua=mailto:esaldgut+dmarc@gmail.com"`},
		}))
}

func TestEventRouting(t *testing.T) {
	template := synthSending(t)

	// Event destination to EventBridge for bounce/complaint.
	template.ResourceCountIs(jsii.String("AWS::SES::ConfigurationSetEventDestination"), jsii.Number(1))
	template.HasResourceProperties(jsii.String("AWS::SES::ConfigurationSetEventDestination"),
		assertions.Match_ObjectLike(&map[string]interface{}{
			"EventDestination": assertions.Match_ObjectLike(&map[string]interface{}{
				"Enabled":                true,
				"MatchingEventTypes":     []interface{}{"bounce", "complaint"},
				"EventBridgeDestination": assertions.Match_ObjectLike(&map[string]interface{}{}),
			}),
		}))

	// SNS topic + an EventBridge Rule that matches SES events and targets it.
	template.ResourceCountIs(jsii.String("AWS::SNS::Topic"), jsii.Number(1))
	template.ResourceCountIs(jsii.String("AWS::Events::Rule"), jsii.Number(1))
	template.HasResourceProperties(jsii.String("AWS::Events::Rule"),
		assertions.Match_ObjectLike(&map[string]interface{}{
			"EventPattern": assertions.Match_ObjectLike(&map[string]interface{}{
				"source":      []interface{}{"aws.ses"},
				"detail-type": []interface{}{"Email Bounce", "Email Complaint"},
			}),
			"Targets": assertions.Match_AnyValue(),
		}))
}
