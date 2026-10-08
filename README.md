# StatusBoard on Amazon EKS

A complete, self-contained project: a **Go status page and incident tracker**, the **Terraform** that builds its AWS platform, and the **GitHub Actions** pipelines that test, build and deploy it to **staging and production**.

This folder is designed to be its **own GitHub repository**: GitHub only runs workflows from `.github/workflows/` at the root of a repository, and every path in the workflows is relative to this folder.

![DevOps architecture](architecture/statusboard-devops-architecture.png)

## What is inside

```text
statusboard/                      <- repository root
├── app/                          the Go application, Dockerfile, docker-compose and Helm chart
│   ├── helm/statusboard/         one chart: app, PostgreSQL cluster, Valkey, backup + export jobs, monitoring
│   └── helm/environments/        staging.yaml, prod.yaml
├── terraform/                    VPC, EKS, ECR, add-ons, monitoring, postgres-operator,
│                                 staging + prod namespaces, backup and data-export buckets
├── .github/
│   ├── workflows/
│   │   ├── statusboard-infra.yml Terraform: Checkov, plan, approval, apply / destroy
│   │   └── statusboard-app.yml   test, build, scan, push, staging, approval, production
│   └── actions/statusboard-deploy/   the deploy steps shared by staging and production
├── architecture/                 4 draw.io diagrams (.png to view, .drawio to edit)
└── docs/
    ├── StatusBoard-App-Guide.docx            how the app, chart, HA database, backups and exports work
    └── StatusBoard-EKS-Deployment-Guide.docx step-by-step deployment, costs, teardown, troubleshooting
```

| Part | Highlights |
|---|---|
| Application | Stateless Go pods (2 in staging, 3–10 autoscaled in prod) spread over 3 Availability Zones; Valkey cache; Prometheus metrics on a private port |
| Database | PostgreSQL 17 run by the Zalando **postgres-operator**: 3 pods (1 primary, 2 synchronous-capable replicas) in 3 zones, automatic failover with Patroni |
| Data protection | Nightly `pg_dump` to S3; monthly CSV + dump export to a KMS-encrypted bucket that a read-only data team group can download |
| Delivery | GitHub OIDC (no AWS keys), one image built and scanned once, deployed to staging, smoke-tested, approved, then promoted to production |
| Monitoring | kube-prometheus-stack, a ServiceMonitor, 12 alerts and a Grafana dashboard with an environment switch |

## Diagrams

| Diagram | What it shows |
|---|---|
| [`statusboard-devops-architecture`](architecture/statusboard-devops-architecture.png) | GitHub Actions, AWS services, the VPC across 3 AZs, the EKS cluster with prod, staging and monitoring, the database members, and the backup and export buckets |
| [`statusboard-system-architecture`](architecture/statusboard-system-architecture.png) | The platform as layers: users, edge, application, data and backups, observability |
| [`statusboard-app-data-flow`](architecture/statusboard-app-data-flow.png) | Reading the page (cache first) and updating an incident (write, then clear the cache) |
| [`statusboard-cicd-pipeline`](architecture/statusboard-cicd-pipeline.png) | Both workflows, from `git push` to production |

## Quick start

```bash
# 0. push this folder as its own repository
cd statusboard
git init && git add . && git commit -m "StatusBoard on EKS"
git branch -M main
git remote add origin https://github.com/<you>/statusboard.git
git push -u origin main

# 1. change the state bucket name, domain, github_repository and admin ARN (see the deployment guide)
cd terraform && ./create-remote-state.sh          # prints the role ARN for GitHub

# 2. in GitHub: secrets AWS_ROLE_ARN and GRAFANA_ADMIN_PASSWORD, variable DOMAIN_NAME,
#    environments "staging" (no rules), "production" and "infrastructure" (required reviewers)

# 3. Actions: run "StatusBoard Infrastructure" (apply), then "StatusBoard Application"
```

Run the app on your laptop: `cd app && docker compose up --build`, then open <http://localhost:8080>.

The full walkthrough, with explanations of every step, is in [`docs/StatusBoard-EKS-Deployment-Guide.docx`](docs/StatusBoard-EKS-Deployment-Guide.docx).

> Running the platform costs roughly USD 12 per day. Destroy it after each study session (workflow action `destroy`, then `terraform/destroy-remote-state.sh`).
