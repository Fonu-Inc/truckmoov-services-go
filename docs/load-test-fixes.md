# Load-test fixes (1k VU signup → vehicles → GPS)

What failed under k6, what we changed, and why. Covers rate limits, capacity, Redis write/read paths, and Timescale.

## Results that drove this

- Public/full runs exited 99 with ~48–57% failures, dial timeouts, and p98 over 100ms.
- Root causes stacked: per-IP rate limits on shared gateway IPs, undersized pods, Redis dial-per-command on every write, and synchronous Timescale inserts on the live ingest path.

---

## 1. Tracking per-IP rate limit (ingest)

| | |
|---|---|
| **Bad** | `WithRateLimit` default ~100 RPS/IP applied to all tracking routes, including GPS ingest. Device/gateway traffic shares a few source IPs, so one bucket capped the whole fleet. |
| **Fixed** | Bypass rate limit for GPS ingest paths in `pkg/http/rate_limit.go` (`e32176f`). Gitops also sets `RATE_LIMIT_RPS=0` / `BURST=0` on tracking (`cab464d`). |
| **Why** | Live map ingest is legitimate high-rate traffic from few IPs; IP buckets starve it. Abuse control belongs elsewhere (auth, device identity, gateway). |

## 2. Onboarding / fleet / platform capacity

| | |
|---|---|
| **Bad** | Auth onboarding ~2 RPS / 120 burst; tracking 2×100m CPU; redis/kong/auth/fleet undersized → dial timeouts under 1k VUs. |
| **Fixed** | Gitops `cab464d`: onboarding 50/500; fleet RATE_LIMIT 500/1000; tracking HPA 3–12; CPU/memory bumps for tracking, auth, fleet, redis, kong. |
| **Why** | Failures were connection/timeouts, not app logic. Raising ceilings and adding HPA stops pods from being CPU-throttled while k6 opens thousands of concurrent dials. |

## 3. Redis write path — dial per command

| | |
|---|---|
| **Bad** | Custom `pkg/redis.Store` dialed TCP + AUTH + SELECT on **every** command, then closed the socket. Each GPS fix did SetEX + SAdd (+ history EVAL) = multiple new connections. |
| **Fixed** | Idle connection pool (32) with AUTH once per dial; connections reused across commands. |
| **Why** | At 1k concurrent writers, dial/AUTH dominated latency and exhausted Redis/file-descriptor capacity. Reuse turns N dials into N commands on warm sockets. |

## 4. Redis write — latest location SetEX + SAdd

| | |
|---|---|
| **Bad** | `RedisLatestLocationRepository.Save` did SetEX then SAdd as two round trips (and two dials before the pool fix). |
| **Fixed** | Pipeline both commands on one connection / one RTT. |
| **Why** | Every ingest must update snapshot + tenant index. Pipelining halves wait time on that critical path. |

## 5. Redis write — location history

| | |
|---|---|
| **Bad** | Sync `PushCapped` EVAL on every ingest after the pool fix still added a Redis RTT to p98 (~460ms med remaining). |
| **Fixed** | Fire-and-forget goroutine for history append (errors still logged). |
| **Why** | Live map uses latest snapshot + NATS; ring history can lag a beat without failing the GPS 200. |

## 6. Redis read — fleet latest locations list

| | |
|---|---|
| **Bad** | `List(tenant)` did SMEMBERS then **one GET per vehicle** (N+1). Under load that was N round trips (and N dials). |
| **Fixed** | SMEMBERS + single `MGET` for all keys. |
| **Why** | Read path for map/list must scale with fleet size; one MGET keeps latency flat vs vehicle count. |

## 7. Timescale write — sync insert on ingest

| | |
|---|---|
| **Bad** | Ingest waited up to 500ms for `SaveSample` INSERT even though comments said Timescale is best-effort. Slow/locked DB delayed every GPS 200. |
| **Fixed** | `AsyncTelemetryStore` wraps Timescale: fire-and-forget with 128 in-flight cap; drop samples under backpressure (live path already in Redis+NATS). |
| **Why** | Source of truth for live tracking is Redis+NATS. Blocking HTTP on history DB violates that design and fails p98. |

## 8. Timescale schema / query shape

| Path | Bad | Fixed | Why |
|---|---|---|---|
| Hypertable chunks | Default chunk interval (often 7d) means recent device queries scan large chunks under heavy insert. | `create_hypertable(..., chunk_time_interval => INTERVAL '1 day')`. | Smaller chunks → less IO for `ORDER BY observed_at DESC LIMIT n` and history windows. |
| Cold data | Uncompressed history grows WAL/disk; aggregates scan fat rows. | Compression segmentby `tenant_id,device_id`, orderby `observed_at DESC`, policy after 7 days. | Device-detail history/aggregates read compressed cold data; hot day stays writable. |
| Indexes | Already `(tenant_id, device_id, observed_at DESC)` and firmware index. | Kept; queries already match. | Latest / history / aggregates filter tenant+device+time — index-only friendly. |
| Reads | `LatestSample`, `SampleHistory`, `Aggregates`, `FirmwareHistory` already use the covering predicates. | No SQL rewrite needed beyond chunk/compress. | Fix was write decoupling + storage layout, not broken SELECT text. |

## 9. Timescale pod + Postgres tunables

| | |
|---|---|
| **Bad** | 250m CPU / 512Mi–1Gi, stock Postgres defaults — WAL fsync and tiny `shared_buffers` under continuous INSERT. |
| **Fixed** | Requests 500m/1Gi, limits 2 CPU / 2Gi; `shared_buffers=256MB`, larger WAL, `synchronous_commit=off`, `checkpoint_completion_target=0.9`. Tracking `DB_MAX_OPEN_CONNS=25`. |
| **Why** | Telemetry is best-effort: losing the last few unflushed commits on crash is acceptable; fsync-on-every-commit is not. More buffer + connections match async writer concurrency. |

## 10. Fleet vehicle queries (plate / list)

| | |
|---|---|
| **Bad** | Suspected missing indexes; k6 also sent `?limit=` which fleet rejects with 400. |
| **Fixed** | Indexes already present: `(tenantId, plateNumber)`, `(tenantId, status)`, etc. on `fleet_vehicles`. k6 lists `/fleet/vehicles` without unknown query keys. |
| **Why** | Create-by-plate and tenant lists were not the 1k-VU failure mode once auth/fleet rate limits and resources were raised. |

## 11. k6 client / auth mistakes (test harness)

| | |
|---|---|
| **Bad** | SUPER_ADMIN + portal=admin → fleet 404; login probes tripped IP block; `vehicles?limit=` → 400. |
| **Fixed** | Use tenant `ENTERPRISE_ADMIN` / `FLEET_ADMIN` token; rotate JWTs; drop invalid query params. |
| **Why** | Harness bugs looked like API outages. |

## 12. NATS publish logging (post-fix ingest still ~460ms med)

| | |
|---|---|
| **Bad** | Every JetStream publish logged at **Info** (`event_published`). At ~1k GPS RPS that alone saturates slog/CPU on tracking pods. |
| **Fixed** | Demote to `Debug`. |
| **Why** | Hot-path success logs are observability debt under flood; failures still return errors to the caller. |

## 13. Device assign 0% under k6

| | |
|---|---|
| **Bad** | k6 `PATCH /fleet/devices/:id/assignment` after create — API requires `verified_online`/`assignable` lifecycle; new devices are `registered` → always 400. |
| **Fixed** | Seed via `PATCH /fleet/vehicles/:id/gps-device` (IMEI attach) instead of assignment. |
| **Why** | Assignment lifecycle is product-correct; load seed only needs IMEI→vehicle for tracking identity. |

## 14. Capacity round 2 (after ~30% fail / fleet create 2% pass)

| | |
|---|---|
| **Bad** | Tracking 3×250m, fleet/auth 2×250m, redis 250m, fleet RL 500, onboarding 50/500, road-match on — still CPU-bound and signup/create timeouts. |
| **Fixed** | Tracking min 6 / 500m–2CPU; fleet min 4 / 500m–2CPU, RL 2000/4000; auth min 3 / 500m–2CPU, onboarding 200/2000; redis 500m–2CPU; `TRACKING_ROAD_MATCH_ENABLED=false`. |
| **Why** | Round-1 bumps stopped dial timeouts; remaining miss is sustained CPU + fleet/auth write contention + Valhalla/NATS side work. |

## 15. Latest measured deltas (for context)

| Run | Fail rate | Write avg | Ingest med |
|---|---|---|---|
| Pre-fix | ~48–57% + dial timeouts | ~1.7s | p98 miss / timeouts |
| After round 1 (full) | ~30% | ~883ms | ~460ms |
| Target | low | — | p98 &lt; 100ms |

Round 2 code/gitops above aims at the leftover; re-measure after deploy.

---

## Commit map

| Repo | Commit | Scope |
|---|---|---|
| `truckmoov-services-go` | `e32176f` | Ingest rate-limit bypass |
| `truckmoov-gitops` | `cab464d` | Capacity round 1 |
| `truckmoov-services-go` | `64c0fab` | Redis pool/pipeline/MGET, async Timescale, chunks |
| `truckmoov-gitops` | `4f70ec3` | Timescale resources/tunables |
| (this change) | pending | NATS log demote, async history, k6 gps attach, capacity round 2 |

Do not push until explicitly asked.
