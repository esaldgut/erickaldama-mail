#!/usr/bin/env python3
"""
erickaldama.com email system — architecture diagram with official AWS icons.

Diagram-as-code (mingrammer `diagrams`). Generates a PNG/SVG using the official
AWS Architecture Icons, for portfolio/diffusion use (LinkedIn, slides). The
GitHub-native version lives in ../architecture.md (Mermaid). Both are generated
from code; this one renders real AWS service icons.

Run:
    python3 architecture_icons.py          # → erickaldama_email_architecture.png
Requires: graphviz (system) + `pip install diagrams`.
"""

from diagrams import Diagram, Cluster, Edge
from diagrams.aws.engagement import SimpleEmailServiceSes
from diagrams.aws.storage import SimpleStorageServiceS3
from diagrams.aws.compute import Lambda
from diagrams.aws.database import Dynamodb
from diagrams.aws.integration import SQS, SimpleNotificationServiceSns
from diagrams.aws.network import Route53
from diagrams.aws.management import Cloudwatch
from diagrams.aws.general import User
from diagrams.onprem.client import Client

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
    tui = Client("Go TUI client\n(read + send)")

    with Cluster("Route 53 — erickaldama.com"):
        dns = Route53("MX · DKIM ×3\nMAIL FROM · DMARC")

    with Cluster("RECEIVE  ·  us-east-1"):
        ses_in = SimpleEmailServiceSes("SES\nreceipt rule set")
        s3 = SimpleStorageServiceS3("S3\nraw MIME (SSE-S3)")
        parse = Lambda("Lambda\nparse (async)")
        index = Dynamodb("DynamoDB\nmail-index")
        dlq = SQS("SQS DLQ")

    with Cluster("SEND"):
        ses_out = SimpleEmailServiceSes("SES v2\nSendEmail (SigV4)")

    with Cluster("Observability"):
        cw = Cloudwatch("CloudWatch\nbounce/complaint/DLQ")
        sns = SimpleNotificationServiceSns("SNS → operator")

    # Receive flow
    sender >> Edge(label="MX") >> dns >> ses_in
    ses_in >> Edge(label="① store") >> s3
    ses_in >> Edge(label="② notify") >> parse
    parse >> Edge(label="read body") >> s3
    parse >> Edge(label="PutItem\n(idempotent)") >> index
    parse >> Edge(label="on-failure", style="dashed") >> dlq

    # Operator drives the TUI
    operator >> Edge(label="drives") >> tui

    # TUI READS mail (no IMAP — own client over the backend)
    tui >> Edge(label="① list/threads (Query)") >> index
    tui >> Edge(label="② open msg (GetObject)", style="dashed") >> s3

    # TUI SENDS mail
    tui >> Edge(label="③ send (build MIME)") >> ses_out
    ses_out >> Edge(label="DKIM/SPF/DMARC", style="dashed") >> dns
    ses_out >> Edge(label="to recipient") >> sender

    # Observability
    ses_out >> Edge(label="events", style="dashed") >> cw
    dlq >> Edge(style="dashed") >> cw
    cw >> sns
