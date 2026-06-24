package infra

// Canonical identifiers for the erickaldama.com email project (SP-1 foundation).
const (
	Account = "367707589526"
	Region  = "us-east-1"

	DomainName = "erickaldama.com"

	// IAM resource names (stable; referenced by the bootstrap and post-deploy gate).
	ReadonlyManagedPolicyName = "mail-readonly-managed"
	ReadonlyUserName          = "mail-readonly"
	// BoundaryManagedPolicyName names the permissions boundary. It is NOT a stack resource —
	// it is a bootstrap artifact (iam/erickaldama-boundary.json) that must pre-exist for
	// `cdk bootstrap --custom-permissions-boundary`. Kept here as the canonical name.
	BoundaryManagedPolicyName = "erickaldama-boundary"

	// SP-2 — sending identity.
	MailFromDomain  = "mail.erickaldama.com"
	FromAddress     = "erick@erickaldama.com"
	ConfigSetName   = "mail-config"
	SendPolicyName  = "mail-send"
	SenderRoleName  = "mail-sender-role"
	BounceTopicName = "mail-bounce-complaint"
	// Event-routing resource names (SES config-set EventBridge destination + the bounce/complaint Rule).
	EventDestinationName = "mail-config-eventbridge"
	BounceRuleName       = "mail-ses-bounce-complaint"
	// IdentityArn is the SES identity ARN used to scope the send policy.
	IdentityArn = "arn:aws:ses:us-east-1:367707589526:identity/erickaldama.com"
	// HostedZoneID is the SP-1 hosted zone for erickaldama.com (CfnOutput of FoundationStack).
	// Imported by SP-2+ stacks via HostedZone_FromHostedZoneAttributes.
	HostedZoneID = "Z023932911KA6S98A6ZRW"
	// DmarcValue is the monitor-only DMARC record (no rua yet). A cross-domain rua to Gmail is
	// NON-FUNCTIONAL: per RFC 7489 §7.1 the report receiver's domain must publish an authorization
	// record (<policydomain>._report._dmarc.<receiver>), and live DNS confirms gmail.com publishes
	// none (per-domain or wildcard) — so senders would not deliver reports there. rua is added in
	// SP-3 pointing at a same-domain mailbox (dmarc-reports@erickaldama.com), which needs no
	// cross-domain authorization. Verified 2026-06-11 via dig + RFC 7489.
	DmarcValue = "v=DMARC1; p=none; rua=mailto:dmarc-reports@erickaldama.com"

	// SP-3 — receive pipeline.
	RawBucketName       = "erickaldama-mail-raw"
	MailIndexTableName  = "mail-index"
	ReceiveFunctionName = "mail-receive"
	ReceiveLambdaRole   = "mail-receive-lambda-role"
	ReceiptRuleSetName  = "erickaldama-inbound"
	ReceiptRuleName     = "store-and-index"
	ReceiveDlqName      = "mail-receive-dlq"
	InboundObjectPrefix = "inbound/" // SES appends messageId verbatim; trailing slash required.
	InboundMxHost       = "inbound-smtp.us-east-1.amazonaws.com"
	DmarcReportsAddress = "dmarc-reports@erickaldama.com"
	OperatorEmail       = "esaldgut@gmail.com" // publishable / benign
	// ReceiptRuleArn is the ARN used to scope the SES S3 bucket policy and Lambda invoke permission.
	ReceiptRuleArn = "arn:aws:ses:us-east-1:367707589526:receipt-rule-set/erickaldama-inbound:receipt-rule/store-and-index"

	// SP-4 — client principals (long-lived access keys generated out-of-band; never in CDK/git).
	ClientReadUserName = "mail-client-read" // dynamodb:Query/GetItem on mail-index + s3:GetObject on inbound/*
	SenderUserName     = "mail-sender"      // attaches mail-send policy directly (SendRawEmail)
)

// projectTags are applied to the stack so every resource is attributable.
func projectTags() map[string]*string {
	return map[string]*string{
		"Project":    strptr("erickaldama-mail"),
		"Subproject": strptr("SP-1"),
		"ManagedBy":  strptr("CDK-Go"),
	}
}

func strptr(s string) *string { return &s }
