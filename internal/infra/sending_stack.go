package infra

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsroute53"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsses"
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

	// Import the SP-1 hosted zone by attributes (pure reference — no AWS call, no lookup-role).
	zone := awsroute53.HostedZone_FromHostedZoneAttributes(stack, jsii.String("ImportedZone"),
		&awsroute53.HostedZoneAttributes{
			HostedZoneId: jsii.String("Z023932911KA6S98A6ZRW"),
			ZoneName:     jsii.String(DomainName),
		})

	// Configuration set (publishes Reputation.* metrics for the alarms). Event destination in Task 3.
	configSet := awsses.NewConfigurationSet(stack, jsii.String("ConfigurationSet"),
		&awsses.ConfigurationSetProps{
			ConfigurationSetName: jsii.String(ConfigSetName),
			ReputationMetrics:    jsii.Bool(true),
		})

	// Domain identity: Easy DKIM (auto CNAMEs) + custom MAIL FROM (auto MX + SPF TXT).
	// FeedbackForwarding OFF (event destination captures bounce/complaint — avoid duplicate emails).
	// Identity_PublicHostedZone(zone) makes CDK auto-publish the 5 records into the imported zone.
	awsses.NewEmailIdentity(stack, jsii.String("SendingIdentity"),
		&awsses.EmailIdentityProps{
			Identity:                    awsses.Identity_PublicHostedZone(zone),
			DkimIdentity:                awsses.DkimIdentity_EasyDkim(awsses.EasyDkimSigningKeyLength_RSA_2048_BIT),
			MailFromDomain:              jsii.String(MailFromDomain),
			MailFromBehaviorOnMxFailure: awsses.MailFromBehaviorOnMxFailure_USE_DEFAULT_VALUE,
			ConfigurationSet:            configSet,
			FeedbackForwarding:          jsii.Bool(false),
		})

	// DMARC (monitor-only) — the construct does NOT publish DMARC; SPF is already auto-published, do NOT add it.
	awsroute53.NewTxtRecord(stack, jsii.String("Dmarc"), &awsroute53.TxtRecordProps{
		Zone:       zone,
		RecordName: jsii.String("_dmarc.erickaldama.com"),
		Values:     jsii.Strings(DmarcValue),
	})

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
