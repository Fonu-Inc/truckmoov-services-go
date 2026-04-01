# truckmoov-services-go

Golang monorepo containing:
- Notification service (`:3003`)
- Tracking service (`:3004`)
- Dispatch service (`:3005`)

## Quick start

```bash
cp .env.example .env
make dev
```

Run quality checks:
```bash
go test ./...
```

## CORS
- `CORS_ALLOWED_ORIGINS` accepts comma-separated origins.
- Default local value allows web + Kong dev hosts (`localhost` / `127.0.0.1` on `5173` and `8000`).

## Environment variables

Core vars used by notification, tracking, and dispatch:
- `NOTIFICATION_PORT`, `TRACKING_PORT`, `DISPATCH_PORT`
- `DEFAULT_TENANT`
- `JWT_SECRET` (must match the auth service signing secret when tenant should be derived from bearer tokens)
- `CORS_ALLOWED_ORIGINS`
- `NATS_URL`, `NATS_STREAM` (use a JetStream-safe stream name such as `truckmoov_events`)

Dispatch persistence vars:
- `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, `POSTGRES_SSLMODE`

Tracking state/cache vars:
- `REDIS_ADDR`, `REDIS_DB`

Tracking- and ingest-specific vars:
- `TELEMETRY_MAX_ACCURACY_M`: discard obviously bad GPS samples above this accuracy threshold.
- `TELEMETRY_MAX_SPEED_KPH`: reject implausible speed samples.
- `TRACKING_HISTORY_TTL_SECONDS`: retention for historical location entries.
- `TRACKING_LATEST_TTL_SECONDS`: retention for latest-known vehicle position cache.
- `GPS_API_KEY`: reserved for external GPS vendor adapters when enabled.

Shared runtime protection:
- `RATE_LIMIT_RPS`, `RATE_LIMIT_BURST`

Local recommendation:
- keep dispatch Postgres vars aligned with the platform repo `POSTGRES_*`
- keep `REDIS_ADDR` aligned with the platform repo `REDIS_PORT` for tracking
- keep `NATS_URL` aligned with the platform repo `NATS_CLIENT_PORT`

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

Kubernetes manifests now live under `deploy/k8s/{notification,tracking,dispatch}` for the in-cluster service names used by the platform gateway.
Dispatch job state is now backed by Postgres instead of per-process memory, while tracking continues to use Redis for operational location state.

## Health endpoints
- `GET http://localhost:3003/notify/health`
- `GET http://localhost:3004/tracking/health`
- `GET http://localhost:3005/dispatch/health`

## Dispatch API docs
- `GET http://localhost:3005/docs/dispatch` (preferred Swagger UI path)
- `GET http://localhost:3005/docs/dispatch/openapi.json` (preferred OpenAPI spec path)
- `GET http://localhost:3005/dispatch/docs` (legacy Swagger UI path)
- `GET http://localhost:3005/dispatch/docs/openapi.json` (legacy OpenAPI spec path)
- Kong unified docs URL: `GET http://localhost:8000/docs/dispatch`

## Dispatch lifecycle endpoints (Epic D)
- `GET /dispatch/jobs` list jobs for the Dispatch Center. Supports `status`, `query`, `driver_id`, and `vehicle_id` filters. Tenant scope is derived from the bearer token. Returns the standard envelope: `{ "status": true, "message": "success", "data": [...] }`.
- `POST /dispatch/jobs` create job from the original Dispatch Center modal payload (`vehicleId`, optional `driverId`, `pickupLocation`, `deliveryLocation`, optional `cargoDescription`, `priority`, `notes`). Canonical priorities are `low|normal|high|urgent`; legacy `medium` and `critical` inputs are still accepted and normalized to `normal` and `urgent`. The older richer flat payload and nested pickup/dropoff payload are still accepted.
- `PATCH /dispatch/jobs/{job_id}/scheduling` set priority/time windows.
- `POST /dispatch/jobs/{job_id}/assign` assign/reassign driver + vehicle.
- `POST /dispatch/jobs/{job_id}/cancel` cancel with reason.
- `POST /dispatch/jobs/{job_id}/acknowledge` driver acknowledgement.
- `POST /dispatch/jobs/{job_id}/status` update status (`en-route|arrived|loaded|delivered`).
- `GET /dispatch/jobs/{job_id}` fetch job details. Returns the standard envelope: `{ "status": true, "message": "success", "data": { ... } }`.
- `GET /dispatch/jobs/{job_id}/timeline` fetch lifecycle history. Returns the standard envelope: `{ "status": true, "message": "success", "data": { "job_id": "...", "timeline": [...] } }`.
- `POST /dispatch/jobs/{job_id}/pod-attachments` add proof-of-delivery attachment.
- `POST /dispatch/jobs/{job_id}/notes` add dispatcher note.
- `POST /dispatch/jobs/status` legacy compatibility endpoint for direct status event publishing.

## Dispatch storage
- Dispatch jobs are stored in Postgres table `dispatch_jobs`.
- The repository keeps the full job aggregate in `payload JSONB` and surfaces indexed workflow columns (`status`, `priority`, `assigned_driver_id`, `assigned_vehicle_id`, timestamps) for relational querying.
- Existing Redis-backed dispatch job records are not auto-migrated; recreate or backfill them if you are switching an environment with live data.

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
