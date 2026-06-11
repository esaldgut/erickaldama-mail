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
