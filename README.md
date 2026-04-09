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
- `NOTIFICATION_SERVICE_URL` (dispatch uses this to call the notification service when sending WhatsApp driver instructions)
- `JWT_SECRET` (must match the auth service signing secret when tenant should be derived from bearer tokens)
- `CORS_ALLOWED_ORIGINS`
- `NATS_URL`, `NATS_STREAM` (use a JetStream-safe stream name such as `truckmoov_events`)

Dispatch persistence vars:
- `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, `POSTGRES_SSLMODE`

Tracking state/cache vars:
- `REDIS_ADDR`, `REDIS_DB`

Notification service auth vars:
- `JWT_SECRET`: must match the auth service signing secret so template-management endpoints can derive tenant scope from bearer tokens.

Notification WhatsApp vars (Meta Cloud API transport):
- `WHATSAPP_API_BASE_URL`: defaults to `https://graph.facebook.com/v18.0`
- `WHATSAPP_APP_ID`: Meta app id used for embedded signup token exchange
- `WHATSAPP_APP_SECRET`: Meta app secret used for embedded signup token exchange
- `WHATSAPP_ACCOUNT_ENCRYPTION_KEY`: 32-byte AES key encoded as 64 hex characters; used to encrypt tenant-specific long-lived WhatsApp access tokens at rest
- `WHATSAPP_PHONE_NUMBER_ID`: legacy fallback phone number id when no tenant WhatsApp account has been connected yet
- `WHATSAPP_BUSINESS_ACCOUNT_ID`: legacy fallback Business Account id when no tenant WhatsApp account has been connected yet
- `WHATSAPP_ACCESS_TOKEN`: legacy fallback permanent/system-user access token when no tenant WhatsApp account has been connected yet
- `WHATSAPP_WEBHOOK_VERIFY_TOKEN`: used for inbound webhook verification

Notification SMS fallback vars (Dotgo, mirrored from `fonu-api` v2):
- `SMS_SERVICE_URL`
- `SMS_SERVICE_USER_NAME`
- `SMS_SERVICE_PASSWORD`
- `SMS_SERVICE_SENDER_ID`

Migration note from `fonu-api`:
- Truckmoov uses `WHATSAPP_PHONE_NUMBER_ID` as the canonical env name.
- `WHATSAPP_BUSINESS_NUMBER_ID` and `WEBHOOK_VERIFY_TOKEN` are also accepted as temporary compatibility aliases during migration.

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

## Notification template endpoints
- `POST /notify/whatsapp/settings` connects a tenant WhatsApp account via Meta embedded signup. The request supplies the embedded-signup `code`, `phoneNumberId`, and `businessAccountId`. The service exchanges the code for a long-lived token, encrypts that token at rest, stores the tenant WhatsApp account in Postgres, and best-effort subscribes the app to the Business Account webhook.
- `GET /notify/whatsapp/settings` returns the currently connected tenant WhatsApp account.
- `DELETE /notify/whatsapp/settings/{phoneNumberId}` disconnects the currently connected tenant WhatsApp account.
- Once a tenant WhatsApp account has been connected, template creation/listing and WhatsApp message sends resolve phone number id, business account id, and access token dynamically from the stored tenant account instead of the legacy env fallback.
- `POST /notify/whatsapp/create-template` mirrors the Fonu v2 route shape but now accepts either the shorthand `{ "name", "header", "body" }` payload or a richer Meta-compatible payload with `templateType`, `language`, `category`, `allowCategoryChange`, `parameterFormat`, and full `components` for header media, body examples, and button definitions.
- Supported `templateType` values are `job_assignment` and `other`. `job_assignment` templates must contain placeholders `{{1}}` to `{{6}}` across their components so the minimum driver job instruction data can be populated at send time.
- `POST /notify/whatsapp/get-all-templates` mirrors the Fonu v2 route shape and returns the current template list from the configured Meta WhatsApp Business Account, including approval status such as `PENDING`, `APPROVED`, or `REJECTED`.
- `POST /notify/whatsapp/send-job-instruction` sends a WhatsApp template message to a driver number, but only when the selected template is approved and compatible with the `job_assignment` variable contract. The send payload must provide values for driver name, job reference, pickup location, delivery location, cargo description, and priority, and can optionally supply header media or button parameters when the chosen template requires them.
- When a driver replies to a job-instruction conversation with one of the dedicated status keywords `ENROUTE`, `ARRIVED`, `LOADED`, or `DELIVERED`, the WhatsApp webhook records the inbound message, reopens the 24-hour conversation window, and forwards a pending driver status request into dispatch. The keyword reply does not directly update the job status; dispatcher confirmation is still required.
- If Meta later reports that a sent WhatsApp job-instruction message failed delivery through the webhook `statuses[]` callback, notification automatically sends a one-time SMS fallback with the same core job instruction details through Dotgo. Repeated failed callbacks do not trigger repeated SMS sends.
- `POST /notify/whatsapp/send-other-template` sends an approved non-job-assignment WhatsApp template to a driver or other recipient. The payload supplies `variables` keyed by placeholder index (`"1"`, `"2"`, ...), and the backend rejects the request unless every variable configured on the approved template is present at send time. Header media and dynamic button parameters are also enforced when the template requires them.
- There is intentionally no delete endpoint here because Fonu v2 does not expose one, and template lifecycle should stay aligned with Meta Business Manager rather than a local catalog.
- Template approval remains asynchronous on the Meta side; the create endpoint submits the template request and returns the Meta response immediately.


## Notification API docs
- `GET http://localhost:3003/docs/notification` (preferred Swagger UI path)
- `GET http://localhost:3003/docs/notification/openapi.json` (preferred OpenAPI spec path)
- `GET http://localhost:3003/notify/docs` (legacy Swagger UI path)
- `GET http://localhost:3003/notify/docs/openapi.json` (legacy OpenAPI spec path)
- Kong unified docs URL: `GET http://localhost:8000/docs/notification`

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
- `POST /dispatch/jobs/{job_id}/send-whatsapp-instruction` send an approved job-assignment WhatsApp template to the selected driver number using the job data already stored on the dispatch record. The caller still supplies the driver number, driver display name, and template-specific media/button params. Returns the standard envelope with the updated job aggregate after the `driver_instruction_sent` timeline entry is recorded.
- `POST /dispatch/jobs/{job_id}/driver-status-requests` record a pending driver-reported status request from a recognized WhatsApp keyword reply. Supported keywords are `ENROUTE`, `ARRIVED`, `LOADED`, and `DELIVERED`. This does not update the job status yet; it only appends `driver_status_reported` to the job timeline and stores a pending confirmation request.
- `POST /dispatch/jobs/{job_id}/driver-status-requests/{request_id}/confirm` confirm a pending driver-reported status request. Only dispatcher-capable roles should call this; confirmation appends `driver_status_confirmed` and then performs the actual job status transition.
- `POST /dispatch/jobs/{job_id}/pod-attachments` add proof-of-delivery attachment.
- `POST /dispatch/jobs/{job_id}/notes` add dispatcher note.
- `POST /dispatch/jobs/status` legacy compatibility endpoint for direct status event publishing.

## Dispatch storage
- Dispatch jobs are stored in Postgres table `dispatch_jobs`.
- The repository keeps the full job aggregate in `payload JSONB` and surfaces indexed workflow columns (`status`, `priority`, `assigned_driver_id`, `assigned_vehicle_id`, timestamps) for relational querying.
- Existing Redis-backed dispatch job records are not auto-migrated; recreate or backfill them if you are switching an environment with live data.

## Event topics (examples)
- `tm.<tenant>.vehicle.location.updated`
- `tm.<tenant>.notification.send`
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
