# StatusBoard

A public **status page and incident tracker** written in **Go** for this project: the kind of page companies publish at `status.<company>.com`. Visitors see whether each part of the service works; on-call engineers open and update incidents through a token-protected API.

It is designed to show how a cloud-native app should behave on Kubernetes:

| Part | Kubernetes object | Why |
|---|---|---|
| Go web app | **Deployment**, 2 pods (staging) / 3–10 with autoscaling (prod), spread across zones | Keeps no data, so copies are interchangeable |
| PostgreSQL 17 | **StatefulSet** of 3 pods (prod) built by the Zalando **postgres-operator**: 1 primary + 2 replicas, one per Availability Zone, synchronous streaming replication, automatic failover with Patroni | The only place data lives, so it is protected against pod, node and zone failures |
| Valkey (Redis-compatible) | Deployment, no disk | Cache: most page views never touch the database |
| Nightly `pg_dump` to S3 | **CronJob** with EKS Pod Identity, reading from a replica | Backups without stored AWS keys |
| Monthly data export | **CronJob**: one CSV per table + full dump + manifest, to a KMS-encrypted bucket | The data team downloads a snapshot for analysis (read-only access) |

Full guide: **`../docs/StatusBoard-App-Guide.docx`** (how it works, running locally, high availability and failover, the Helm chart, monitoring, backups and restore, monthly data exports, troubleshooting).

## Run it locally

```bash
# quickest: in-memory data, no other software needed
ADMIN_TOKEN=dev SEED_COMPONENTS="Website,API" go run .        # http://localhost:8080

# realistic: with PostgreSQL and Valkey
docker compose up --build                                      # admin token: local-admin-token
```

```bash
curl -X POST localhost:8080/api/v1/incidents -H "Authorization: Bearer local-admin-token" \
  -d '{"title":"Payments are slow","impact":"major","message":"We are investigating."}'
```

Metrics are on a separate port that is never exposed publicly: `http://localhost:9090/metrics`.

## Tests

```bash
go vet ./... && go test ./...
```

The Docker build runs the same checks, so a failing change can never become an image.

## API

| Method and path | Auth | Purpose |
|---|---|---|
| `GET /`, `GET /incidents/{id}` | - | Status page, incident page |
| `GET /api/v1/status` | - | Summary as JSON |
| `GET /api/v1/components`, `GET /api/v1/incidents[?all=true]` | - | Lists |
| `POST /api/v1/components` | Bearer token | Create a component |
| `PATCH /api/v1/components/{id}` | Bearer token | Change its status (`operational`, `degraded`, `partial_outage`, `major_outage`) |
| `POST /api/v1/incidents` | Bearer token | Open an incident (`impact`: `minor`, `major`, `critical`) |
| `POST /api/v1/incidents/{id}/updates` | Bearer token | Add an update (`investigating`, `identified`, `monitoring`, `resolved`) |
| `GET /healthz`, `GET /readyz` | - | Liveness (process) and readiness (needs PostgreSQL) |

## Layout

```text
main.go                 settings, wiring, graceful shutdown
internal/store/         data model, PostgreSQL (with embedded migrations), in-memory store for tests
internal/cache/         Valkey/Redis cache
internal/web/           pages, API, health checks, Prometheus metrics, tests
Dockerfile              Go build -> distroless, non-root
docker-compose.yml      app + PostgreSQL + Valkey
helm/statusboard/       Helm chart (Deployment, HPA, PDB, postgresql cluster, Valkey, backup + export CronJobs, Ingress, monitoring)
helm/environments/      staging.yaml, prod.yaml
```

## Deploy

The GitHub Actions workflow [`statusboard-app.yml`](../.github/workflows/statusboard-app.yml) tests, builds and scans the image once, deploys it to **staging**, smoke-tests it, waits for an approval, then deploys the **same image** to **production**. Infrastructure: [`../terraform`](../terraform).
