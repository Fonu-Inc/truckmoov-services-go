# DDD / Clean Architecture (services-go)

## Bounded contexts
- `dispatch`: job assignment/state transitions.
- `tracking`: location ingest/live tracking.
- `notification`: message delivery lifecycle.

## Layering convention
Per bounded context:
- `internal/<context>/domain`: entities/domain events/value objects/invariants.
- `internal/<context>/application`: use-cases and ports.
- `internal/<context>/infrastructure`: adapters for event bus, DB, providers.
- `internal/<context>/interfaces`: HTTP/gRPC handlers.

`cmd/<service>` remains composition root only.

## Implemented vertical slices
`dispatch/job.status.changed` follows DDD layering:
- domain event: `internal/dispatch/domain/job_status_changed.go`
- use-case: `internal/dispatch/application/publish_job_status_changed.go`
- event bus adapter: `internal/dispatch/infrastructure/job_status_event_publisher.go`
- HTTP adapter: `internal/dispatch/interfaces/http/handler.go`
- composition root wiring: `cmd/dispatch/main.go`

`tracking/location+alert` follows DDD layering:
- domain events: `internal/tracking/domain/*`
- use-cases: `internal/tracking/application/*`
- adapters: `internal/tracking/infrastructure/*`
- HTTP adapter: `internal/tracking/interfaces/http/handler.go`
- composition root wiring: `cmd/tracking/main.go`

`notification/outbox+retry` follows DDD layering:
- domain model/events: `internal/notification/domain/*`
- use-cases: `internal/notification/application/*`
- adapters: `internal/notification/infrastructure/*`
- HTTP adapter: `internal/notification/interfaces/http/handler.go`
- composition root wiring: `cmd/notification/main.go`

Public API remains:
- `POST /dispatch/jobs/status`
- `GET /dispatch/health`
- `GET /dispatch/eta`

## Next DDD slices
1. Replace in-memory repositories with Redis/Postgres-backed adapters for durability.
2. Add application-level tests for use-cases and contract tests for adapters.
3. Introduce domain value objects for IDs/statuses to centralize validation.
