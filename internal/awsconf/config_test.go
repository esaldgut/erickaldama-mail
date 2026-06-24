package awsconf

import "testing"

func TestConstants(t *testing.T) {
	if Region != "us-east-1" || TableName != "mail-index" || BucketName != "erickaldama-mail-raw" || InboundPrefix != "inbound/" {
		t.Fatalf("constants drifted: %s %s %s %s", Region, TableName, BucketName, InboundPrefix)
	}
}
