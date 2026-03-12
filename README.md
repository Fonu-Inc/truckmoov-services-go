# truckmoov-services-go

Golang monorepo containing:
- Notification service (`:3003`)
- Tracking service (`:3004`)
- Dispatch service (`:3005`)

## Quick start

```bash
cp .env.example .env
go run ./cmd/notification
# in another terminal
go run ./cmd/tracking
# in another terminal
go run ./cmd/dispatch
```

Run quality checks:
```bash
go test ./...
```

## CORS
- `CORS_ALLOWED_ORIGINS` accepts comma-separated origins.
- Default local value allows web + Kong dev hosts (`localhost` / `127.0.0.1` on `5173` and `8000`).

## Container Architectures
- Linux deployments can be either `amd64` or `arm64` (depends on your node pool).
- Local fallback on Apple Silicon when native arm images break:
```bash
make docker-build-local LOCAL_PLATFORM=linux/amd64 IMAGE_REPO=truckmoov IMAGE_TAG=dev
```
- CI/CD recommended path (publish one multi-arch tag):
```bash
make docker-buildx-push IMAGE_REPO=ghcr.io/<org>/truckmoov IMAGE_TAG=<tag> PUSH_PLATFORMS=linux/amd64,linux/arm64
```
- Runtime will pull the matching architecture image automatically from that multi-arch tag.

## GitHub Actions
- Workflow: `.github/workflows/services-go-cicd.yml`
- `pull_request`: runs formatting checks, `go vet`, `go test`, binary compilation, and Docker build validation when Go service files change.
- `push` to `main`: runs the same pipeline on mainline changes.
- `workflow_dispatch`: lets you trigger the pipeline manually.

Current scope is CI plus Docker image verification. A deploy stage can be added once this repo has its own Kubernetes manifests or another declared deployment target, similar to the TypeScript services repo.

## Health endpoints
- `GET http://localhost:3003/notify/health`
- `GET http://localhost:3004/tracking/health`
- `GET http://localhost:3005/dispatch/health`

## Dispatch API docs
- `GET http://localhost:3005/dispatch/docs` (Swagger UI)
- `GET http://localhost:3005/dispatch/docs/openapi.json` (OpenAPI spec)
- Kong unified docs URL: `GET http://localhost:8000/docs/dispatch`

## Dispatch lifecycle endpoints (Epic D)
- `POST /dispatch/jobs` create job (pickup/dropoff).
- `PATCH /dispatch/jobs/{job_id}/scheduling` set priority/time windows.
- `POST /dispatch/jobs/{job_id}/assign` assign/reassign driver + vehicle.
- `POST /dispatch/jobs/{job_id}/cancel` cancel with reason.
- `POST /dispatch/jobs/{job_id}/acknowledge` driver acknowledgement.
- `POST /dispatch/jobs/{job_id}/status` update status (`en-route|arrived|loaded|delivered`).
- `GET /dispatch/jobs/{job_id}` fetch job details.
- `GET /dispatch/jobs/{job_id}/timeline` fetch lifecycle history.
- `POST /dispatch/jobs/{job_id}/pod-attachments` add proof-of-delivery attachment.
- `POST /dispatch/jobs/{job_id}/notes` add dispatcher note.
- `POST /dispatch/jobs/status` legacy compatibility endpoint for direct status event publishing.

## Event topics (examples)
- `tm.<tenant>.vehicle.location.updated`
- `tm.<tenant>.job.status.changed`
- `tm.<tenant>.driver.job.instruction`
- `tm.<tenant>.alert.triggered`
- `tm.<tenant>.message.delivery.updated`

## Protobuf contracts
Source of truth is under `api/proto`:
- `tracking/v1/tracking.proto`
- `dispatch/v1/dispatch.proto`
- `common/v1/common.proto`

## DDD notes
- Bounded contexts: `dispatch`, `tracking`, `notification`
- DDD architecture guide: `docs/ddd-architecture.md`
- Implemented layered slices:
  - `internal/dispatch` (`job.lifecycle`, `job.status.changed`)
  - `internal/tracking` (`vehicle.location.updated`, `alert.triggered`)
  - `internal/notification` (`message.delivery.updated`, outbox+retry)
