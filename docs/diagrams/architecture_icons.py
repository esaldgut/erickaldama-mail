#!/usr/bin/env python3
"""
erickaldama.com email system — architecture diagram with official AWS icons.

Diagram-as-code (mingrammer `diagrams`). Generates a PNG/SVG using the official
AWS Architecture Icons, for portfolio/diffusion use (LinkedIn, slides). The
GitHub-native version lives in ../architecture.md (Mermaid). Both are generated
from code; this one renders real AWS service icons.

Run:
    cd docs/diagrams && .venv/bin/python architecture_icons.py
        → erickaldama_email_architecture.png
Requires: graphviz (system) + `pip install diagrams`.

SP-4 (2026-06-24 LIVE): added Go terminal client cluster with dual AI backend
(Ollama local qwen3:32b / Claude API claude-opus-4-8) and two disjoint IAM users
(mail-client-read, mail-sender). PNG not regenerated automatically — run the
command above after activating the venv.

CD (2026-06-25 LIVE): added CI/CD cluster — GitHub Actions deploys the CDK-Go
stacks to AWS via OIDC (no long-lived keys) with manual prod approval. CdStack
(5th stack) provisions a native OIDC provider + two scoped roles: mail-cd-diff
(read-only) and mail-cd-deploy (environment:production-gated).

v0.7 / v0.7.1 (2026-08-06 LIVE): the AI agent's search_messages tool now routes
through the v0.5 SQLite+FTS5 cache (bm25-ranked, prefix match) with a live-DynamoDB
substring fallback that fires only on real infra failure (invariant AR-1). v0.7.1
hardened the tool Description (describe the search MECHANISM, ASCII-pure) so a local
LLM stops inventing Gmail-style from: operators — a purely declarative change (no
runtime path altered). Added the FTS5 cache node + backend edges below.

NDA parametrization (2026-08, PR #22): the AWS Account ID + ARNs no longer appear as
literals in the public repo. The account is resolved at synth from CDK_DEFAULT_ACCOUNT
(exported in cd.yml from sts get-caller-identity on the OIDC-assumed role) and injected
via NewEnv()/EnvStackProps into the 5 stacks; role-to-assume comes from GitHub secrets.
cdk synth stays byte-identical (verified: empty cdk diff in CI). Added the account-
resolution node + edges in the CI/CD cluster below.
"""

from diagrams import Diagram, Cluster, Edge
from diagrams.aws.engagement import SimpleEmailServiceSes
from diagrams.aws.storage import SimpleStorageServiceS3
from diagrams.aws.compute import Lambda
from diagrams.aws.database import Dynamodb
from diagrams.aws.integration import SQS, SimpleNotificationServiceSns
from diagrams.aws.network import Route53
from diagrams.aws.management import Cloudwatch
from diagrams.aws.security import IAM
from diagrams.aws.general import User
from diagrams.onprem.client import Client
from diagrams.onprem.compute import Server
from diagrams.generic.storage import Storage
from diagrams.onprem.ci import GithubActions
from diagrams.onprem.vcs import Github

graph_attr = {
    "fontsize": "16",
    "bgcolor": "white",
    "pad": "0.6",
    "splines": "spline",
}

with Diagram(
    "erickaldama.com — Email System (AWS SES · us-east-1 · CDK-Go)",
    filename="erickaldama_email_architecture",
    show=False,
    direction="LR",
    graph_attr=graph_attr,
):
    sender = User("External sender")
    operator = Client("Operator\n(nvim + tmux)")

    with Cluster("Route 53 — erickaldama.com"):
        dns = Route53("MX · DKIM ×3\nMAIL FROM · DMARC")

    with Cluster("RECEIVE  ·  us-east-1  ·  LIVE"):
        ses_in = SimpleEmailServiceSes("SES v1\nerickaldama-inbound\ncatch-all · TLS Require")
        s3 = SimpleStorageServiceS3("S3 erickaldama-mail-raw\nSSE-S3 · Block-All-Public")
        parse = Lambda("Lambda mail-receive\nGo · al2023 · arm64")
        index = Dynamodb("DynamoDB mail-index\non-demand")
        dlq = SQS("SQS mail-receive-dlq\nSSE")

    with Cluster("SEND"):
        ses_out = SimpleEmailServiceSes("SES v2\nSendEmail (SigV4)")

    with Cluster("Go terminal client · SP-4 · LIVE"):
        tui = Client("cmd/mail (CLI Cobra)\ncmd/mail-tui (TUI Bubble Tea\nVim-motions)")
        read_iam = IAM("mail-client-read\nQuery mail-index\nGetObject erickaldama-mail-raw")
        send_iam = IAM("mail-sender\nSendRawEmail scoped")
        ollama = Server("Ollama local\nqwen3:32b\n(default · on-device)")
        claude_api = Client("Claude API\nclaude-opus-4-8\n(opt-in · explicit warning)")
        fts_cache = Storage(
            "SQLite+FTS5 cache\nindex.sqlite (0600)\nsearch_messages tool\nAR-1 · substring fallback"
        )

    with Cluster("Observability"):
        cw = Cloudwatch("CloudWatch\nbounce/complaint/DLQ")
        sns = SimpleNotificationServiceSns("SNS → operator")

    with Cluster("CI/CD · GitHub Actions → OIDC · LIVE"):
        gh = Github("PR / push to main")
        gha = GithubActions("cd.yml\ndiff (PR) · deploy (main)")
        oidc = IAM("OIDC provider\ntoken.actions.\ngithubusercontent.com")
        diff_iam = IAM("mail-cd-diff\nread-only · lookup only")
        deploy_iam = IAM("mail-cd-deploy\nsub=environment:production")
        # NDA parametrization (PR #22): role ARNs come from GitHub secrets; the account is NOT a source
        # literal — it is resolved at synth from CDK_DEFAULT_ACCOUNT, exported from the OIDC-assumed role.
        acct_resolve = IAM(
            "Export CDK_DEFAULT_ACCOUNT\n(sts get-caller-identity on\nthe assumed role)\n→ Env/EnvStackProps\nno account literal in source"
        )

    # Receive flow
    sender >> Edge(label="MX") >> dns >> ses_in
    ses_in >> Edge(label="① store") >> s3
    ses_in >> Edge(label="② notify") >> parse
    parse >> Edge(label="read body") >> s3
    parse >> Edge(label="PutItem\n(idempotent)") >> index
    parse >> Edge(label="on-failure", style="dashed") >> dlq

    # Operator drives the TUI
    operator >> Edge(label="drives") >> tui

    # TUI uses IAM credentials — read path
    tui >> Edge(label="mail-client-read") >> read_iam
    read_iam >> Edge(label="① list/threads (Query)") >> index
    read_iam >> Edge(label="② open msg (GetObject)", style="dashed") >> s3

    # TUI uses IAM credentials — send path
    tui >> Edge(label="mail-sender") >> send_iam
    send_iam >> Edge(label="③ send (build MIME · SigV4)") >> ses_out
    ses_out >> Edge(label="DKIM/SPF/DMARC", style="dashed") >> dns
    ses_out >> Edge(label="to recipient") >> sender

    # AI dual-backend (read-only agent-loop, no send tool)
    tui >> Edge(label="ai subcommand\n(default · on-device)", style="dashed") >> ollama
    tui >> Edge(label="ai subcommand\n(opt-in · crosses net)", style="dashed") >> claude_api

    # AI agent search_messages tool (v0.7 · hardened Description in v0.7.1):
    # both LLM backends resolve the search tool through the FTS5 cache; the cache
    # falls back to a live-DynamoDB substring read only on real infra failure (AR-1).
    ollama >> Edge(label="search_messages\ntool-call", style="dashed") >> fts_cache
    claude_api >> Edge(label="search_messages\ntool-call", style="dashed") >> fts_cache
    fts_cache >> Edge(label="FTS5 bm25 · prefix match") >> index
    fts_cache >> Edge(label="substring fallback\n(AR-1 · infra error only)", style="dashed") >> read_iam

    # Observability
    ses_out >> Edge(label="events", style="dashed") >> cw
    dlq >> Edge(style="dashed") >> cw
    cw >> sns

    # CI/CD: GitHub Actions assumes scoped OIDC roles to diff (PR) and deploy (main, gated)
    gh >> Edge(label="PR / push") >> gha
    gha >> Edge(label="diff (read-only)") >> diff_iam
    gha >> Edge(label="deploy\n(approval gate)") >> deploy_iam
    diff_iam >> Edge(label="AssumeRoleWithWebIdentity", style="dashed") >> oidc
    deploy_iam >> Edge(label="AssumeRoleWithWebIdentity", style="dashed") >> oidc

    # NDA parametrization (PR #22): the account is resolved from the assumed role at synth, NOT a source
    # literal. role-to-assume comes from GitHub secrets; a cd.yml step exports CDK_DEFAULT_ACCOUNT from
    # `sts get-caller-identity`, which NewEnv()/EnvStackProps inject into the 5 stacks. cdk synth/deploy
    # then produce byte-identical templates (verified: empty cdk diff in CI) with no account literal in git.
    diff_iam >> Edge(label="sts get-caller-identity", style="dashed") >> acct_resolve
    deploy_iam >> Edge(label="sts get-caller-identity", style="dashed") >> acct_resolve
    acct_resolve >> Edge(label="CDK_DEFAULT_ACCOUNT\n→ byte-identical synth") >> gha
    deploy_iam >> Edge(label="cdk deploy → mutates", style="dashed") >> ses_in
