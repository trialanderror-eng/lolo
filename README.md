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

## Development

```
go build ./...
go test ./...
go run ./cmd/rca-server
```

## License

Apache 2.0.
