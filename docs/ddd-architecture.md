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
`dispatch/job.lifecycle` follows DDD layering:
- domain aggregate: `internal/dispatch/domain/job.go`
- use-case: `internal/dispatch/application/job_lifecycle.go`
- adapters:
  - repository: `internal/dispatch/infrastructure/job_repository.go`, `internal/dispatch/infrastructure/postgres_job_repository.go`
  - notifier: `internal/dispatch/infrastructure/driver_instruction_notifier.go`
- HTTP adapter: `internal/dispatch/interfaces/http/handler.go`
- composition root wiring: `cmd/dispatch/main.go`

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
- `GET /dispatch/health`
- `GET /dispatch/jobs`
- `POST /dispatch/jobs`
- `PATCH /dispatch/jobs/{job_id}/scheduling`
- `POST /dispatch/jobs/{job_id}/assign`
- `POST /dispatch/jobs/{job_id}/cancel`
- `POST /dispatch/jobs/{job_id}/acknowledge`
- `POST /dispatch/jobs/{job_id}/status`
- `GET /dispatch/jobs/{job_id}`
- `GET /dispatch/jobs/{job_id}/timeline`
- `POST /dispatch/jobs/{job_id}/pod-attachments`
- `POST /dispatch/jobs/{job_id}/notes`
- `POST /dispatch/jobs/status` (legacy compatibility endpoint)
- `GET /dispatch/docs`
- `GET /dispatch/docs/openapi.json`

## Next DDD slices
1. Add notification consumer for `tm.<tenant>.driver.job.instruction` to deliver WhatsApp messages.
2. Introduce domain value objects for IDs/statuses to centralize validation.
3. Add a reporting/query projection if dispatch analytics outgrow the current aggregate-per-row storage model.
