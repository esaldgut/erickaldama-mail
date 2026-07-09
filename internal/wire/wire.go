// Package wire is the single place that builds clients from profiles/backend. No subcommand duplicates this.
package wire

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"

	"erickaldama-mail/internal/aiassist"
	"erickaldama-mail/internal/aiassist/claude"
	"erickaldama-mail/internal/aiassist/ollama"
	"erickaldama-mail/internal/awsconf"
	"erickaldama-mail/internal/mailbox"
)

// Reader builds a mailbox.Reader using the given AWS profile. If credentials are expired,
// the error includes a suggestion to run "aws sso login --profile <profile>".
func Reader(ctx context.Context, profile string) (*mailbox.Reader, error) {
	cfg, err := awsconf.Load(ctx, profile)
	if err != nil {
		return nil, fmt.Errorf("aws config (profile %s): %w — try: aws sso login --profile %s", profile, err, profile)
	}
	return mailbox.NewReader(dynamodb.NewFromConfig(cfg), s3.NewFromConfig(cfg)), nil
}

// Sender builds a mailbox.Sender using the given AWS profile. If credentials are expired,
// the error includes a suggestion to run "aws sso login --profile <profile>".
func Sender(ctx context.Context, profile string) (*mailbox.Sender, error) {
	cfg, err := awsconf.Load(ctx, profile)
	if err != nil {
		return nil, fmt.Errorf("aws config (profile %s): %w — try: aws sso login --profile %s", profile, err, profile)
	}
	return mailbox.NewSender(ses.NewFromConfig(cfg), sesv2.NewFromConfig(cfg)), nil
}

// Provider returns the LLM backend. Ollama (local, default) needs no consent. Claude crosses the network →
// print the opt-in warning to STDERR once before returning it (spec §7 "opt-in con aviso").
func Provider(backend, agentModel string) (aiassist.LLMProvider, error) {
	switch backend {
	case "", "ollama":
		return ollama.New(agentModel, ""), nil
	case "claude":
		fmt.Fprintln(os.Stderr, "⚠️  backend=claude: el cuerpo del correo cruzará a api.anthropic.com (no se entrena por default; ZDR recomendado). El texto pasa por redact.Redact antes de enviarse.")
		return claude.New(os.Getenv("ANTHROPIC_API_KEY")), nil
	}
	return nil, fmt.Errorf("backend desconocido %q (usa ollama|claude)", backend)
}
