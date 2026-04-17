# lolo

RCA-only incident investigation. Read-only. Integrates with the tools you already run.

## What it does

Something fires an alert. lolo gets a webhook, fans out to a set of investigators (deploys, alerts, kubernetes, logs, ...), collects evidence, ranks hypotheses, and writes a report to Slack, Jira, GitHub, or stdout.

It does not take actions. It does not remediate. It investigates and explains.

## Interfaces

```go
type Investigator interface {
    Name() string
    Investigate(ctx context.Context, inc Incident) ([]Evidence, error)
}

type Ranker interface {
    Rank(ctx context.Context, inc Incident, ev []Evidence) ([]Hypothesis, error)
}

type Sink interface {
    Emit(ctx context.Context, inc Incident, hs []Hypothesis) error
}
```

Each piece is pluggable and independently testable.

## Flow

```
Trigger (webhook) ──► Incident ──► Investigators (parallel)
                                        │
                                   []Evidence
                                        │
                                      Ranker
                                        │
                                  []Hypothesis
                                        │
                                      Sinks
```

## Status

Pre-alpha. Interfaces stable. One stub investigator and a stdout sink so the wiring is exercised end-to-end. Real investigators land next.

## Running

```
go run ./cmd/rca-server                 # listens on :8080, or $LOLO_ADDR
```

Environment:

| Var                  | Purpose                                                            |
|----------------------|--------------------------------------------------------------------|
| `LOLO_ADDR`          | listen address (default `:8080`)                                   |
| `LOLO_WEBHOOK_TOKEN` | shared-secret bearer token enforced on `/webhook/*`. If unset, the server logs a warning at startup and accepts unauthenticated requests (dev only). |
| `LOLO_DASHBOARD_TOKEN` | password for HTTP basic auth on `/`, `/investigations/*`, `/api/*`. Username is ignored. If unset, dashboard accepts unauthenticated requests (dev only). |
| `LOLO_GITHUB_TOKEN`  | GitHub PAT — enables the `github.deploys` investigator             |
| `LOLO_GITHUB_REPOS`  | comma-separated `owner/name` list checked when the incident scope has none |
| `LOLO_K8S_NAMESPACES`| comma-separated namespaces checked when the incident scope has none. The `kubernetes` investigator uses in-cluster auth, falling back to `KUBECONFIG`/`~/.kube/config`. |
| `LOLO_SLACK_WEBHOOK_URL` | Slack Incoming Webhook URL. When set, every RCA report is posted there in addition to stdout. |
| `LOLO_PROMETHEUS_URL` | Prometheus API base URL (e.g. `http://prometheus:9090`). Required to enable the `prometheus` investigator. The host is intentionally NOT taken from the alert payload — that would be an SSRF vector. |
| `LOLO_PROMETHEUS_TOKEN` | Optional bearer token for Prometheus auth. |
| `LOLO_LOKI_URL` | Loki API base URL (e.g. `http://loki:3100`). Required to enable the `logs` investigator. Same SSRF defense as Prometheus — host never taken from the alert payload. |
| `LOLO_LOKI_TOKEN` | Optional bearer token for Loki auth (useful for Grafana Cloud / hosted Loki). |
| `LOLO_PUBLIC_URL` | External URL lolo is reachable at (e.g. `https://lolo.internal`). Used to make `memory` investigator Links absolute so they resolve from Slack/Jira. Relative when unset — dashboard still works. |
| `LOLO_STORAGE_PATH` | File path for the SQLite investigation store (e.g. `/data/lolo.db`). When unset, storage is an ephemeral in-memory ring buffer (investigations lost on restart). Required for the `memory` investigator to accumulate knowledge across restarts. |
| `LOLO_LLM_URL` | OpenAI-compatible endpoint (e.g. `http://ollama:11434`). When set, lolo sends the top 3 hypotheses + their evidence to this endpoint for a natural-language narrative that replaces the deterministic `Reasoning` text. **Use a local endpoint only** — never a hosted API. Incident data must not leave the operator's environment. |
| `LOLO_LLM_MODEL` | Model identifier (e.g. `llama3.1:8b`, `qwen2.5:7b`). Default `llama3.1:8b`. |
| `LOLO_LLM_API_KEY` | Optional bearer auth for the LLM endpoint. Most local backends ignore it; include for hosted gateways if you've configured one despite the warning above. |

Endpoints:

- `GET  /healthz`
- `POST /webhook/alertmanager` — accepts an Alertmanager webhook payload
- `POST /investigate` — ad-hoc investigation (no alert required). Body: `{"summary": "...", "window": "2h", "scope": {"services":[...], "namespaces":[...], "repos":[...]}}`. Same bearer auth as `/webhook/*`.
- `GET  /` — dashboard index (HTML)
- `GET  /investigations/{id}` — single investigation (HTML)
- `GET  /api/investigations` — JSON list
- `GET  /api/investigations/{id}` — JSON detail

Example:

```
curl -X POST http://localhost:8080/webhook/alertmanager \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $LOLO_WEBHOOK_TOKEN" \
  -d '{"commonLabels":{"alertname":"HighErrorRate","service":"payments"},
       "commonAnnotations":{"summary":"Error rate 5%"},
       "alerts":[{"startsAt":"2026-04-16T17:00:00Z","status":"firing"}]}'
```

## Deploying

### Container image

```
docker build -t ghcr.io/trialanderror-eng/lolo:0.1.0 .
docker push    ghcr.io/trialanderror-eng/lolo:0.1.0
```

The image is distroless-static, runs as `nonroot`, ~13MB.

### Helm

The chart in `deploy/helm/` ships a Deployment, Service, ServiceAccount, and a ClusterRole + ClusterRoleBinding so the `kubernetes` investigator can read pods + events cluster-wide.

Credentials are read from a Secret you create yourself; the chart never stores them:

```
kubectl create secret generic lolo \
  --from-literal=webhook-token=$(openssl rand -hex 32) \
  --from-literal=github-token=ghp_xxx \
  --from-literal=slack-webhook-url=https://hooks.slack.com/services/...

helm install lolo deploy/helm \
  --set image.tag=0.1.0 \
  --set config.k8s.namespaces='{prod,infra}' \
  --set config.github.repos='{acme/api,acme/web}'
```

For least-privilege, set `rbac.create=false` and bind a per-namespace `RoleBinding` against the same ClusterRole yourself.

## Development

```
go build ./...
go test ./...
```

## License

Apache 2.0.
