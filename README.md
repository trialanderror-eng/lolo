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
| `LOLO_GITHUB_TOKEN`  | GitHub PAT — enables the `github.deploys` investigator             |
| `LOLO_GITHUB_REPOS`  | comma-separated `owner/name` list checked when the incident scope has none |

Endpoints:

- `GET  /healthz`
- `POST /webhook/alertmanager` — accepts an Alertmanager webhook payload

Example:

```
curl -X POST http://localhost:8080/webhook/alertmanager \
  -H "Content-Type: application/json" \
  -d '{"commonLabels":{"alertname":"HighErrorRate","service":"payments"},
       "commonAnnotations":{"summary":"Error rate 5%"},
       "alerts":[{"startsAt":"2026-04-16T17:00:00Z","status":"firing"}]}'
```

## Development

```
go build ./...
go test ./...
```

## License

Apache 2.0.
