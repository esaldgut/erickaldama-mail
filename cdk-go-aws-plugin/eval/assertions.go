package eval

import (
	"regexp"
	"strings"
)

type Result struct {
	Pass     bool
	Failures []string
}

// norm collapses all whitespace runs to a single space (whitespace-resilient assertions).
func norm(s string) string { return strings.Join(strings.Fields(s), " ") }

func contains(hay, needle string) bool { return strings.Contains(norm(hay), norm(needle)) }
func matches(hay, pattern string) bool { return regexp.MustCompile(pattern).MatchString(hay) }

// AssertSESIdentity checks the generated CDK-Go for the SES-identity golden prompt.
func AssertSESIdentity(out string) Result {
	var f []string
	if !contains(out, "awsses.NewEmailIdentity") {
		f = append(f, "missing awsses.NewEmailIdentity")
	}
	if !matches(out, `jsii\.String\(`) {
		f = append(f, "missing jsii.String usage")
	}
	if contains(out, "dkim.amazonses.com") {
		f = append(f, "trap#2: hardcoded dkim suffix")
	}
	if matches(out, `cdk\s+deploy`) {
		f = append(f, "negative: contains cdk deploy")
	}
	if matches(out, `\b\d{12}\b`) {
		f = append(f, "negative: hardcoded 12-digit account id")
	}
	return Result{Pass: len(f) == 0, Failures: f}
}

// AssertS3Bucket checks the generated CDK-Go for the s3-bucket golden prompt.
func AssertS3Bucket(out string) Result {
	var f []string
	if !contains(out, "awss3.NewBucket") {
		f = append(f, "missing awss3.NewBucket")
	}
	if !matches(out, `jsii\.String\(`) {
		f = append(f, "missing jsii usage")
	}
	if !matches(out, `(?i)BUCKET_OWNER_ENFORCED|S3_MANAGED|SSE`) {
		f = append(f, "missing SSE/encryption config")
	}
	if matches(out, `(?i)PublicReadAccess:\s*jsii\.Bool\(true\)`) {
		f = append(f, "negative: bucket is public")
	}
	if matches(out, `cdk\s+deploy`) {
		f = append(f, "negative: contains cdk deploy")
	}
	return Result{Pass: len(f) == 0, Failures: f}
}
