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
	// IdentityArn is the SES identity ARN used to scope the send policy.
	IdentityArn = "arn:aws:ses:us-east-1:367707589526:identity/erickaldama.com"
	// HostedZoneID is the SP-1 hosted zone for erickaldama.com (CfnOutput of FoundationStack).
	// Imported by SP-2+ stacks via HostedZone_FromHostedZoneAttributes.
	HostedZoneID = "Z023932911KA6S98A6ZRW"
	// DmarcValue is the monitor-only DMARC record. rua points to a Gmail +label so reports
	// are collected from day 1 (mailbox at erickaldama.com does not exist until SP-3).
	DmarcValue = "v=DMARC1; p=none; rua=mailto:esaldgut+dmarc@gmail.com"
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
