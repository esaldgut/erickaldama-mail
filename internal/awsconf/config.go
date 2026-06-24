// Package awsconf loads segregated AWS configs (one per profile) and exposes the canonical
// resource identifiers the client reads/writes. Read path uses mail-client-read; send uses mail-sender.
package awsconf

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

const (
	Region        = "us-east-1"
	TableName     = "mail-index"
	BucketName    = "erickaldama-mail-raw"
	InboundPrefix = "inbound/"
)

// Load returns an aws.Config bound to a named shared-config profile, pinned to us-east-1.
func Load(ctx context.Context, profile string) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx,
		config.WithSharedConfigProfile(profile),
		config.WithRegion(Region),
	)
}
