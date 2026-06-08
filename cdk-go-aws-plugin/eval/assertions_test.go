package eval

import "testing"

func TestAssertSESIdentityOutput(t *testing.T) {
	good := `
		identity := awsses.NewEmailIdentity(stack, jsii.String("Identity"), &awsses.EmailIdentityProps{ ... })
		dkimHost := *identity.DkimDnsTokenName1() // derived from SigningHostedZone, not hardcoded
		identity.AddMailFromAttributes(...)
	`
	res := AssertSESIdentity(good)
	if !res.Pass {
		t.Fatalf("good output should pass, failures: %v", res.Failures)
	}
	bad := `domain := "erickaldama.com"; dkim := "token.dkim.amazonses.com"; run("cdk deploy")`
	res = AssertSESIdentity(bad)
	if res.Pass {
		t.Fatalf("bad output should fail (hardcoded dkim + cdk deploy)")
	}
}

func TestAssertS3BucketOutput(t *testing.T) {
	good := `b := awss3.NewBucket(stack, jsii.String("Raw"), &awss3.BucketProps{ Encryption: awss3.BucketEncryption_S3_MANAGED })`
	if res := AssertS3Bucket(good); !res.Pass {
		t.Fatalf("good s3 output should pass: %v", res.Failures)
	}
	bad := `b := awss3.NewBucket(stack, jsii.String("Raw"), &awss3.BucketProps{ PublicReadAccess: jsii.Bool(true) }); run("cdk deploy")`
	if res := AssertS3Bucket(bad); res.Pass {
		t.Fatalf("public+deploy s3 output should fail")
	}
}
