# User Fixes PDF → Backend Fix Map

Source: `~/Documents/User Fixes.pdf` (22-page UI spec). Every issue below is mapped to its exact fix: which repo, which endpoint, and current status. Two backend repos are involved:

- **`truckmoov-services-ts`** (NestJS) — `fleet` app: fuel, financials, devices inventory, carriers/data-bundles catalog.
- **`truckmoov-services-go`** — `dispatch` service: jobs/shipments. `tracking` service: GPS device telemetry.

Status legend: ✅ EXISTS/FIXED (ready to use) · ⚠️ PARTIAL (works but missing a piece, noted) · ⛔ NOT BUILT (deferred, reason given).

---

## 1. Fuel Analytics — weekly diesel/petrol consumption chart

**PDF**: bar chart, diesel vs petrol liters by day, date-range picker ("Last 7 Days").

**Fix**: `truckmoov-services-ts` — `GET /fleet/fuel/metrics?period=daily|weekly|monthly&from=&to=`
File: `apps/fleet/src/fleet.controller.ts` (`fuelConsumption`) → `contexts/fleet/fuel/application/fuel-metrics.use-case.ts`
Response: array of `{ label, liters, totalCost, records, byFuelType: { diesel: {...}, petrol: {...} } }`.

Status: ✅ FIXED. Daily bucketing and `from`/`to` were added this session — previously only weekly/monthly existed with no date filter.

---

## 2. Fuel Efficiency — km/L per vehicle

**PDF**: per-vehicle km/L bars, red/green threshold coloring.

**Fix**: `truckmoov-services-ts` — `GET /fleet/fuel/efficiency`
File: `apps/fleet/src/fleet.controller.ts` (`fuelEfficiency`) → `fuel-metrics.use-case.ts`
Response: `{ vehicleId, kmPerLiter, belowThreshold, threshold }[]`.

Status: ✅ EXISTS. No changes needed.

---

## 3. Fuel Budget vs Actual

**PDF**: line chart, daily fuel spend vs budget.

**Fix**: `truckmoov-services-ts` — `GET /fleet/fuel/budget-vs-actual?from=&to=`
File: `apps/fleet/src/fleet.controller.ts`, `BudgetVsActualQueryDto` in `dto/fuel.dto.ts`

Status: ✅ EXISTS.

---

## 4. Fuel Budgets (set/list)

**PDF**: budget config per vehicle or fleet-wide.

**Fix**: `truckmoov-services-ts` — `GET /fleet/fuel/budgets`, `PUT /fleet/fuel/budgets/:vehicleId` (use `"all"` for fleet-wide).

Status: ✅ EXISTS.

---

## 5. Profitability Distribution (donut: Excellent/Good/Marginal/Loss-Making)

**PDF**: vehicle count by profitability tier, date range.

**Fix**: `truckmoov-services-ts` — `POST /fleet/financials/profitability` returns per-vehicle `tier`; tier counts now also rolled up into `GET /fleet/financials/summary`'s `tierDistribution` field.
Files: `apps/fleet/src/fleet-financials.use-case.ts` (`aggregate()`, `computeProfitability()`), `dto/fleet.schema.ts` (`TierDistributionSchema`).

Status: ✅ FIXED. `tierDistribution` and date-range (`from`/`to`) filtering added this session — previously no aggregate tier-count endpoint existed.

---

## 6. Cost Breakdown (Fuel/Maintenance/Wages/Insurance/Depreciation bar chart)

**PDF**: fleet-wide totals by category, date range.

**Fix**: `truckmoov-services-ts` — `GET /fleet/financials/summary?from=&to=` now includes `costBreakdown: { fuel, maintenance, wages, insurance, depreciation, other }` (fleet-wide sums).
File: `fleet-financials.use-case.ts` (`aggregate()`).

Status: ✅ FIXED. Previously `summary` only had totals/margin/score, no per-category breakdown.

---

## 7. Vehicle Profit & Loss table (search, status filter, range filters, score bar)

**PDF**: per-vehicle revenue/costs/profit/margin/status/score, searchable, filterable by status dropdown and margin%/revenue/net-profit/score ranges.

**Fix**: `truckmoov-services-ts` — `POST /fleet/financials/profitability`
New body fields: `search`, `marginFrom`/`marginTo`, `revenueFrom`/`revenueTo`, `netProfitFrom`/`netProfitTo`, `scoreFrom`/`scoreTo`, `from`/`to`.
New response fields per vehicle: `status` (= `tier`, same 4 values), `name` (composed `make + model`, falls back to plate number), `kmDriven` (odometer delta).
File: `dto/financials.dto.ts` (`ComputeProfitabilityDto`), `fleet-financials.use-case.ts`.

Status: ✅ FIXED. Search/range filters, `status`, `name`, `kmDriven` all added this session.

---

## 8. Statement Builder (title/company/period/prepared-by/notes/accent-color/section-toggles, export PDF/CSV/Excel)

**PDF**: modal to configure a financial statement's cover page, branding, and which sections to include, then export.

**Fix**: `truckmoov-services-ts` — `GET /fleet/financials/statement`, `GET /fleet/financials/statement/export?format=pdf|csv|xlsx`
New query params: `title`, `companyName`, `preparedBy`, `notes`, `accentColor`, `includeCoverPage`, `includeFleetSummary`, `includePerVehicleBreakdown`.
Files: `dto/financials.dto.ts` (`StatementQueryDto`), `financial-statement.renderer.ts` (`StatementOptions`, threaded through CSV/XLSX/PDF renderers).

Status: ⚠️ PARTIAL. Title/company/prepared-by/notes/section-toggles all wired for all 3 formats. `accentColor` is accepted by the API but **not applied** — the in-house PDF renderer (`utils/pdf.ts`) has no color support (text-only PDF primitives), and extending it for one cosmetic field wasn't worth it this pass. If accent color matters, `utils/pdf.ts` needs a color-fill primitive added first.

---

## 9. Configure Vehicle Financials (per-vehicle cost config)

**PDF**: select a vehicle, save cost parameters.

**Fix**: `truckmoov-services-ts` — `PUT /fleet/financials/configs/:vehicleId`
New fields: `fuelEstimateNgnPerMonth`, `maintenanceEstimateNgnPerMonth` (existing: `monthlyWagesNgn`, `monthlyInsuranceNgn`, `monthlyDepreciationNgn`, `monthlyOtherNgn`).
Behavior: fuel/maintenance cost in profitability calc uses **actual records** when they exist for the period; falls back to `estimate × periodMonths` only when no records exist (e.g. brand-new vehicle).
Files: `dto/financials.dto.ts`, migration `Migration20260701000000.ts` (adds the 2 columns), `fleet-financials.use-case.ts` (`computeProfitability` fallback logic).

Status: ✅ FIXED. Previously fuel/maintenance had zero configurability — always ₦0 for vehicles with no logged records.

---

## 10. Detailed Cost Analysis table (5-column export)

**PDF**: per-vehicle Fuel/Maintenance/Wages/Insurance/Depreciation/Total columns, exportable.

**Fix**: `truckmoov-services-ts` — `GET /fleet/financials/cost-breakdown/export`
CSV headers now: `vehicleId, plateNumber, fuelCost, maintenanceCost, wages, insurance, depreciation, totalCost` (5 cost categories split out; previously wages/insurance/depreciation were collapsed into one `fixedCosts` column).
File: `fleet-financials.use-case.ts` (`exportCostBreakdown`).

Status: ✅ FIXED.

---

## 11. Underperforming Assets ("Unknown Vehicle" bug, revenue/costs/km driven/job count)

**PDF**: list of marginal/loss-making vehicles with revenue, costs, km driven, job count.

**Fix**: same `POST /fleet/financials/profitability` response as item 7 — `name` field now populated (was missing, causing the "Unknown Vehicle" label in the screenshot) and `kmDriven` now populated (was entirely absent).

Status: ✅ FIXED. Both root causes of the screenshot's "Unknown Vehicle" bug addressed.

---

## 12. Devices list (search, manufacturer filter, last-seen filter, status filter, export)

**PDF**: device inventory table with filters and export.

**Fix**: `truckmoov-services-ts`
- `GET /fleet/devices?lastSeenBucket=5min|1hr|24hr|7day|never` — new filter, added this session.
- `GET /fleet/devices/export` — new CSV export endpoint, added this session (didn't exist before — devices had no export at all).
Files: `dto/vehicle.dto.ts` (`DeviceListQueryDto`), `fleet.controller.ts`, `list-devices.use-case.ts`, `mikro-device.repository.ts`.

Status: ✅ FIXED.

---

## 13. Device actions — View/Edit/Unassign/Update Firmware/Delete

**PDF**: dropdown menu per device row.

**Fix**: `truckmoov-services-ts`
- View: `GET /fleet/devices/:id` — existed.
- Edit: `PATCH /fleet/devices/:id` — existed, but **imei was editable, now fixed to read-only** (`UpdateDeviceDto` no longer inherits `imei`).
- Unassign: `DELETE /fleet/devices/:id/assignment` — existed.
- Update Firmware: `PATCH /fleet/devices/:id/firmware` — **new dedicated endpoint**, added this session (previously only reachable via the generic edit endpoint, no standalone action or activity-log entry).
- Delete: `DELETE /fleet/devices/:id` — existed.

Status: ✅ FIXED (firmware + imei read-only were the 2 gaps, both closed).

---

## 14. Bulk Import Devices (CSV template + upload)

**PDF**: download template, drag-and-drop upload.

**Fix**: `truckmoov-services-ts`
- Upload: `POST /fleet/devices/import` — existed.
- Template download: `GET /fleet/devices/import-template` — **new**, added this session (didn't exist before).

Status: ✅ FIXED.

---

## 15. Request Device Setup (manufacturer/model/tracker-type/SIM/carrier/plan/data-bundle/vehicle/location/date/use-case)

**PDF**: form to request device provisioning, with an SIM-required toggle exposing carrier/plan/data-bundle sub-fields sourced from a "Data Bundles catalog."

**Fix**: `truckmoov-services-ts`
- Request submission: `POST /fleet/device-setup-requests` — existed, covers every field in the form.
- **Data Bundles catalog** (new): `GET /fleet/carriers`, `GET /fleet/data-bundles?carrierId=` — built this session from scratch (didn't exist at all before; `carrierId`/`bundleId` were previously free-text strings with nothing to validate against).
Files: `contexts/fleet/carriers/*` (new bounded context), migration `Migration20260701000001.ts` (seeds MTN/Airtel/Glo/9mobile + sample bundles).

Status: ✅ FIXED. Catalog is global (not per-tenant), seeded with 4 Nigerian carriers.

---

## 16. My Device Requests (list + detail w/ timeline)

**PDF**: track submitted requests through review/provisioning/assignment.

**Fix**: `truckmoov-services-ts` — `GET /fleet/device-setup-requests`, `GET /fleet/device-setup-requests/:id`

Status: ✅ EXISTS. No changes needed — org name, requesting email, all SIM/carrier fields, timeline all already present.

---

## 17. Shipments list (Total/Pending/In-Progress/Completed/Total-Value counters, search, status filter)

**PDF**: dashboard counters + job cards.

**Fix**: `truckmoov-services-go` (dispatch)
- List: `GET /dispatch/jobs?status=&query=&driver_id=&vehicle_id=`
- Counters: `GET /dispatch/jobs/summary` → `{ total, by_status: {...}, total_value_ngn }`. `total_value_ngn` (sum of non-cancelled job amounts) **added this session** — the endpoint already existed but was missing this one field.
Files: `internal/dispatch/interfaces/http/handler.go`, `internal/dispatch/application/job_lifecycle.go` (`SummarizeJobs`).

Status: ✅ FIXED (only the total-value figure was missing; everything else already worked).

---

## 18. Create New Job (3-step wizard: locations + intermediate stops, cargo/pricing/deadline, review)

**PDF**: pickup/delivery with map preview and "Add intermediate stop," then cargo/weight/description/priority/amount/deadline, then review + create.

**Fix**: `truckmoov-services-go` (dispatch) — `POST /dispatch/jobs`, single call (not 3 separate requests — frontend assembles the 3-step form into one payload).
- `stops: [{address, contact_name, contact_phone, latitude, longitude, notes}]` — **new**, added this session for intermediate stops (previously only single pickup+dropoff existed, no way to represent extra stops at all).
- Priority "Medium" → the backend already normalizes `"medium"`/`""`/`"normal"` all to `normal` — **no frontend or backend change needed**, this was misreported as a gap in an earlier pass of this audit.
- Map preview / address→coordinates: **not a backend concern** — see item 21 below, this is a frontend-only Mapbox integration.

Status: ✅ FIXED (stops), ✅ ALREADY WORKED (priority mapping).

---

## 19. Job detail — Edit and Delete actions

**PDF**: `...` dropdown on a job card with View Details / Edit / Delete.

**Fix**: `truckmoov-services-go` (dispatch)
- Edit: `PATCH /dispatch/jobs/{id}` — **new**, added this session. Full-detail edit: `pickup`, `dropoff`, `stops`, `cargo_type`, `cargo_description`, `amount`, `deadline`, `load_weight_kg`, `load_volume_m3`. **Important for whoever builds this: this is full-replacement, not a merge-patch** — omitting a field (e.g. `amount`) resets it to 0/empty, it does not leave the existing value untouched. Always send the complete current job body plus your changes.
- Delete: `DELETE /dispatch/jobs/{id}` — **new**, added this session. Hard-deletes the row. The older `POST /dispatch/jobs/{id}/cancel` (soft-cancel, preserves the record with `status: cancelled`) still exists separately — **pick one of these two for the UI's red "Delete" button**; hard-delete has no audit trail, cancel does.
Files: `internal/dispatch/domain/job.go` (`UpdateDetails`), `internal/dispatch/application/job_lifecycle.go` (`UpdateDetails`, `DeleteJob`), `internal/dispatch/infrastructure/*job_repository.go` (`Delete` on all 3 backends: in-memory, Redis, Postgres).

Status: ✅ FIXED. Neither endpoint existed before this session.

---

## 20. Job detail — status progress bar, assigned driver/vehicle, activity timeline

**PDF**: `Pending → Assigned → Accepted → In Progress → Completed` progress bar, driver contact icons (WhatsApp/message/call), vehicle registration+make/model, activity log.

**Fix**: `truckmoov-services-go` (dispatch) — `GET /dispatch/jobs/{id}`, `GET /dispatch/jobs/{id}/timeline`

Status:
- Activity timeline: ✅ EXISTS, matches directly (empty array → "No activity recorded yet" naturally).
- Status progress bar: ⚠️ MISMATCH, **needs a frontend decision, not backend code**. Backend enum is `created → assigned → en-route → arrived → loaded → delivered` (+ `cancelled`), 6 states. PDF shows 5 UI stages. Recommended mapping (no backend change needed):

  | PDF stage | Backend status |
  |---|---|
  | Pending | `created` |
  | Assigned | `assigned` |
  | Accepted | `acknowledged_at` is set (this is a timestamp flag on the job, not a distinct status value) |
  | In Progress | `en-route`, `arrived`, OR `loaded` (collapse all 3 into this one UI stage) |
  | Completed | `delivered` |

- Vehicle registration/make/model: ⛔ NOT BUILT. Job response only has `assigned_vehicle_id` (a bare UUID FK), no vehicle details embedded. **Frontend must cross-reference this ID against the fleet service's `GET /fleet/vehicles/:id`** — no backend work planned to embed a vehicle snapshot in the job response unless requested.
- Driver contact info (name/phone/WhatsApp): ✅ EXISTS — `assigned_driver_id`, `assigned_driver_phone` on the job; WhatsApp/call/message affordances are frontend UI using `assigned_driver_phone`, nothing further needed backend-side.

---

## 21. Device Connectivity / Telemetry / Firmware tabs

**PDF**: 4 tabs on the device detail modal — Overview, Connectivity, Telemetry, Firmware.

**Fix**: `truckmoov-services-go` (tracking) — `GET /tracking/devices/{deviceId}/{tab}` where `tab` ∈ `overview | connectivity | telemetry | firmware`.
File: `internal/tracking/interfaces/http/handler.go`, `internal/tracking/application/device_telemetry_query.go`.

Status: ✅ ALL 4 TABS ALREADY EXIST, fully backed. **This was incorrectly reported as fully missing in an earlier pass of this audit** — that pass only checked the fleet (NestJS) repo and never looked in `truckmoov-services-go/internal/tracking`. No backend work needed here — this is purely a frontend wiring gap (nothing in `truckmoov-web` currently calls these routes).

Response shapes (all confirmed against actual Go structs):
- `overview`: `device_id, vehicle_id, imei, health, last_seen, signal_strength, battery_level, latitude, longitude, firmware_version, sim: {device_id, iccid, msisdn, carrier, plan, status, data_usage_mb, data_limit_mb, expiry_date}`
- `connectivity`: `device_id, health, last_contact, last_telemetry, seconds_since_contact, status_timeout_seconds, reporting_interval_seconds, sample_count_24h, heartbeat: {expected_interval_seconds, online_threshold_seconds, offline_threshold_seconds}, last_health_check: {at, result}`
- `telemetry`: `device_id, latest, history[], aggregates_24h: {sample_count, avg_interval_seconds, data_quality_pct, window_start, window_end}`
- `firmware`: `device_id, current_version, protocol, reporting_interval_seconds, history: [{version, first_seen, last_seen}]`

One caveat worth checking before wiring the UI: `signal_strength`/`battery_level` in `overview` are raw values from the vendor payload — confirm they're already normalized to 0–100% against the actual device ingestion adapters (PDF shows them as percentages).

---

## 22. Address geocoding for the "Create New Job" map preview

**PDF**: typing a pickup/delivery address drops a pin on a live map.

**Fix**: **frontend-only, `truckmoov-web`** — not a backend task. `mapbox-gl` is already a dependency there (`apps/main/package.json`, `apps/admin/package.json`), with a real component (`apps/main/src/components/maps/MapboxRouteMap.tsx`) already taking a `mapboxToken` prop. The standard pattern is to add `@mapbox/mapbox-gl-geocoder`, reuse the existing token, and do address search entirely client-side — addresses never need to touch either backend repo.

Status: ⛔ NOT BUILT, and **intentionally not a backend endpoint** — a self-hosted OSM geocoder (Nominatim) was considered and the infra was scoped (mirrors the existing Valhalla deployment in `truckmoov-platform`) but not implemented, since Mapbox is the vendor already in use for maps and doing it client-side is simpler and keeps address data off the backend entirely. `MapboxRouteMap.tsx` is also a WIP scaffold — route calculation (`getDirections`) is currently a no-op stub.

---

## Summary table

| # | PDF issue | Repo | Status |
|---|---|---|---|
| 1 | Fuel analytics (daily/date-range) | fleet (ts) | ✅ |
| 2 | Fuel efficiency | fleet (ts) | ✅ existed |
| 3 | Fuel budget vs actual | fleet (ts) | ✅ existed |
| 4 | Fuel budgets CRUD | fleet (ts) | ✅ existed |
| 5 | Profitability distribution | fleet (ts) | ✅ |
| 6 | Cost breakdown (fleet-wide) | fleet (ts) | ✅ |
| 7 | Vehicle P&L table + filters | fleet (ts) | ✅ |
| 8 | Statement builder | fleet (ts) | ⚠️ accent color not applied |
| 9 | Configure vehicle financials | fleet (ts) | ✅ |
| 10 | Detailed cost analysis export | fleet (ts) | ✅ |
| 11 | Underperforming assets (name/km) | fleet (ts) | ✅ |
| 12 | Devices list filters + export | fleet (ts) | ✅ |
| 13 | Device actions (edit/firmware) | fleet (ts) | ✅ |
| 14 | Bulk import + template | fleet (ts) | ✅ |
| 15 | Request device setup + catalog | fleet (ts) | ✅ |
| 16 | My device requests | fleet (ts) | ✅ existed |
| 17 | Shipments counters | dispatch (go) | ✅ |
| 18 | Create job (stops, priority) | dispatch (go) | ✅ |
| 19 | Job edit + delete | dispatch (go) | ✅ |
| 20 | Job detail (status/vehicle) | dispatch (go) | ⚠️ status mapping + vehicle snapshot need frontend decisions |
| 21 | Device tracking tabs | tracking (go) | ✅ existed, needs frontend wiring |
| 22 | Geocoding | truckmoov-web | ⛔ frontend task, not backend |
