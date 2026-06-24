package infra

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscloudwatch"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsevents"
	"github.com/aws/aws-cdk-go/awscdk/v2/awseventstargets"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsroute53"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsses"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssns"
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

	_, configSet := addSendingIdentity(stack)
	addEventRouting(stack, configSet)
	addReputationAlarms(stack)
	addSendIam(stack)

	return stack
}

// addSendingIdentity imports the SP-1 hosted zone, creates the configuration set, the SES
// domain identity (DKIM + custom MAIL FROM + feedback OFF), and the monitor-only DMARC record.
// Returns the imported zone and the config set (the config set is consumed by addEventRouting and
// by later send-IAM/alarm tasks; the zone is returned for completeness and future use).
func addSendingIdentity(stack awscdk.Stack) (awsroute53.IHostedZone, awsses.ConfigurationSet) {
	// Import the SP-1 hosted zone by attributes (pure reference — no AWS call, no lookup-role).
	zone := awsroute53.HostedZone_FromHostedZoneAttributes(stack, jsii.String("ImportedZone"),
		&awsroute53.HostedZoneAttributes{
			HostedZoneId: jsii.String(HostedZoneID),
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
		RecordName: jsii.String("_dmarc." + DomainName),
		Values:     jsii.Strings(DmarcValue),
	})

	return zone, configSet
}

// addEventRouting wires the config set's bounce/complaint events to EventBridge (default bus),
// then routes those events to an SNS topic via an EventBridge Rule.
func addEventRouting(stack awscdk.Stack, configSet awsses.ConfigurationSet) {
	// Send bounce/complaint events to EventBridge (default bus).
	defaultBus := awsevents.EventBus_FromEventBusName(stack, jsii.String("DefaultBus"),
		jsii.String("default"))
	configSet.AddEventDestination(jsii.String("ToEventBridge"),
		&awsses.ConfigurationSetEventDestinationOptions{
			ConfigurationSetEventDestinationName: jsii.String(EventDestinationName),
			Destination:                          awsses.EventDestination_EventBus(defaultBus),
			Enabled:                              jsii.Bool(true),
			Events: &[]awsses.EmailSendingEvent{
				awsses.EmailSendingEvent_BOUNCE,
				awsses.EmailSendingEvent_COMPLAINT,
			},
		})

	// SNS topic + Rule routing SES bounce/complaint events to the operator.
	topic := awssns.NewTopic(stack, jsii.String("BounceComplaintTopic"),
		&awssns.TopicProps{
			TopicName:   jsii.String(BounceTopicName),
			DisplayName: jsii.String("SES bounce and complaint notifications"),
		})
	awsevents.NewRule(stack, jsii.String("SesEventRule"), &awsevents.RuleProps{
		RuleName: jsii.String(BounceRuleName),
		EventBus: defaultBus,
		EventPattern: &awsevents.EventPattern{
			Source:     jsii.Strings("aws.ses"),
			DetailType: jsii.Strings("Email Bounce", "Email Complaint"),
		},
		Targets: &[]awsevents.IRuleTarget{
			awseventstargets.NewSnsTopic(topic, nil),
		},
	})
}

// addReputationAlarms creates CloudWatch alarms below SES's review thresholds. treatMissingData
// IGNORE because the AWS/SES metric does not exist until the first send (the simulator smoke
// turns it on) — without IGNORE the alarms would sit in INSUFFICIENT_DATA forever.
func addReputationAlarms(stack awscdk.Stack) {
	bounceMetric := awscloudwatch.NewMetric(&awscloudwatch.MetricProps{
		Namespace:  jsii.String("AWS/SES"),
		MetricName: jsii.String("Reputation.BounceRate"),
		Period:     awscdk.Duration_Minutes(jsii.Number(5)),
		Statistic:  jsii.String("Average"),
	})
	awscloudwatch.NewAlarm(stack, jsii.String("BounceRateAlarm"), &awscloudwatch.AlarmProps{
		AlarmName:          jsii.String("mail-bounce-rate"),
		Metric:             bounceMetric,
		Threshold:          jsii.Number(0.02),
		EvaluationPeriods:  jsii.Number(1),
		ComparisonOperator: awscloudwatch.ComparisonOperator_GREATER_THAN_OR_EQUAL_TO_THRESHOLD,
		TreatMissingData:   awscloudwatch.TreatMissingData_IGNORE,
	})

	complaintMetric := awscloudwatch.NewMetric(&awscloudwatch.MetricProps{
		Namespace:  jsii.String("AWS/SES"),
		MetricName: jsii.String("Reputation.ComplaintRate"),
		Period:     awscdk.Duration_Minutes(jsii.Number(5)),
		Statistic:  jsii.String("Average"),
	})
	awscloudwatch.NewAlarm(stack, jsii.String("ComplaintRateAlarm"), &awscloudwatch.AlarmProps{
		AlarmName:          jsii.String("mail-complaint-rate"),
		Metric:             complaintMetric,
		Threshold:          jsii.Number(0.0005),
		EvaluationPeriods:  jsii.Number(1),
		ComparisonOperator: awscloudwatch.ComparisonOperator_GREATER_THAN_OR_EQUAL_TO_THRESHOLD,
		TreatMissingData:   awscloudwatch.TreatMissingData_IGNORE,
	})
}

// addSendIam creates the scoped send capability: a mail-send policy (only ses:SendEmail/SendRawEmail,
// only from the verified identity, only as erick@) on a mail-sender-role assumable by any principal
// in the account (incl. SSO permission-set roles) — no long-lived keys.
func addSendIam(stack awscdk.Stack) {
	mailSendPolicy := awsiam.NewManagedPolicy(stack, jsii.String("MailSendPolicy"),
		&awsiam.ManagedPolicyProps{
			ManagedPolicyName: jsii.String(SendPolicyName),
			Statements: &[]awsiam.PolicyStatement{
				awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
					Effect:    awsiam.Effect_ALLOW,
					Actions:   jsii.Strings("ses:SendEmail", "ses:SendRawEmail"),
					Resources: jsii.Strings(IdentityArn),
					Conditions: &map[string]interface{}{
						"StringEquals": map[string]interface{}{"ses:FromAddress": FromAddress},
					},
				}),
			},
		})

	awsiam.NewRole(stack, jsii.String("MailSenderRole"), &awsiam.RoleProps{
		RoleName:        jsii.String(SenderRoleName),
		AssumedBy:       awsiam.NewAccountPrincipal(jsii.String(Account)),
		ManagedPolicies: &[]awsiam.IManagedPolicy{mailSendPolicy},
	})

	// SP-4 — mail-sender user: attaches the same mail-send policy directly for long-lived
	// access key usage by the TUI client. Key generated out-of-band by the human after deploy.
	awsiam.NewUser(stack, jsii.String("MailSenderUser"), &awsiam.UserProps{
		UserName:        jsii.String(SenderUserName),
		ManagedPolicies: &[]awsiam.IManagedPolicy{mailSendPolicy},
	})
}

// sp2Tags mirrors projectTags but marks Subproject=SP-2.
func sp2Tags() map[string]*string {
	return map[string]*string{
		"Project":    strptr("erickaldama-mail"),
		"Subproject": strptr("SP-2"),
		"ManagedBy":  strptr("CDK-Go"),
	}
}
