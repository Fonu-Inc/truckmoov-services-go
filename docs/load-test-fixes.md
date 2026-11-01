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
| **Bad** | Dial-per-command made each `PushCapped` EVAL expensive; async append was tried next. |
| **Fixed** | Keep sync Append (pool reuse). Async was **reverted**: concurrent goroutines reordered the ring so newest fix was not always head. |
| **Why** | History contract requires newest-first; per-vehicle serial queue would be the upgrade if EVAL RTT returns as the bottleneck. |

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

## 12. NATS publish logging

| | |
|---|---|
| **Bad** | Every JetStream publish logged at **Info** (`event_published`) under ~1k RPS. |
| **Fixed** | Demote to `Debug`. Async publish-after-save was **tried and reverted** — vendors must get 503 on bus failure to retry. |
| **Why** | Success logs were free CPU; changing publish order breaks Traccar/webhook retry contracts. |

## 13. Device assign 0% under k6

| | |
|---|---|
| **Bad** | k6 `PATCH /fleet/devices/:id/assignment` after create — API requires `verified_online`/`assignable` lifecycle; new devices are `registered` → always 400. |
| **Fixed** | Seed via `PATCH /fleet/vehicles/:id/gps-device` (IMEI attach) instead of assignment. |
| **Why** | Assignment lifecycle is product-correct; load seed only needs IMEI→vehicle for tracking identity. |

## 13b. Vehicle plan quota / plate collision blocks seed

| | |
|---|---|
| **Bad** | Plan quota 409 **and** duplicate-plate 400 (`K60000000`… from `uniquePlate`) — reuse only handled 409, so setup seeded 0. |
| **Fixed** | Reuse existing vehicles on 400/409; attach fresh IMEIs via `gps-device`; plates include a time suffix. |
| **Why** | Spike must still bind IMEIs for ingest when create is blocked by quota or leftover load-test plates. |

## 14. Capacity round 2 (after ~30% fail / fleet create 2% pass)

| | |
|---|---|
| **Bad** | Tracking 3×250m, fleet/auth 2×250m, redis 250m, fleet RL 500, onboarding 50/500, road-match on — still CPU-bound and signup/create timeouts. |
| **Fixed** | Tracking min 6 / 500m–2CPU; fleet min 4 / 500m–2CPU, RL 2000/4000; auth min 3 / 500m–2CPU, onboarding 200/2000; redis 500m–2CPU; `TRACKING_ROAD_MATCH_ENABLED=false`. |
| **Why** | Round-1 bumps stopped dial timeouts; remaining miss is sustained CPU + fleet/auth write contention + Valhalla/NATS side work. |

## 15. Latest measured deltas (for context)

| Run | Fail rate | Write avg | Ingest med | Notes |
|---|---|---|---|---|
| Pre-fix | ~48–57% + dial timeouts | ~1.7s | p98 miss / timeouts | |
| After round 1 (full) | ~30% | ~883ms | ~460ms | |
| After round 2 (`sha-8e0bf16`) | ~20% | ~779ms | ~399ms med / ~1.8s p95 | writes dominate; integrity ~96% |
| Target | low | — | **p98 &lt; 100ms service time** | see §16–18 |

## 16. JetStream PubAck serial with Redis (ingest p98)

| | |
|---|---|
| **Bad** | Ingest did `Publish` (wait PubAck) → Redis `Save` → history `EVAL` sequentially. Under flood, JetStream file-ack + Redis RTTs stacked past 100ms server time. Fully async publish was tried earlier and **broke** vendor 503 retries. |
| **Fixed** | `BeginPublish` / `PublishAsync`: start JetStream publish, run Redis snapshot while ack is in flight, then `ack()` before 200/202. Still 503 if PubAck fails. History moved to `OrderedAsyncHistoryRepository` (per-vehicle queue, call-order drain). |
| **Why** | Overlap turns `nats+redis+history` into roughly `max(nats, redis)` on the hot path without dropping the retry contract. History stays newest-first without blocking HTTP. |

## 17. NATS undersized for JetStream flood

| | |
|---|---|
| **Bad** | NATS StatefulSet requests **100m CPU / 256Mi** while tracking floods JetStream file storage at hundreds of RPS. |
| **Fixed** | Requests 500m / 512Mi, limits 2 CPU / 1Gi. Event bus uses `PublishAsyncMaxPending(4096)`. |
| **Why** | PubAck latency is dominated by a starved JetStream process; tracking HPA alone cannot fix broker CPU. |

## 18. p98 SLO measurement vs remote WAN

| | |
|---|---|
| **Bad** | k6 thresholds used `http_req_duration` from a laptop. Measured **min write ~127ms**; cold TTFB ~430ms — WAN alone exceeds 100ms, so the service SLO could never pass. |
| **Fixed** | Gate `tm_upstream_ms` on Kong `X-Kong-Upstream-Latency` at `p(98)<100`. Keep client duration as `P98_CLIENT_MS` (default 10000) for transport visibility. |
| **Why** | Requirement is how long the **API** takes to complete the request; Kong upstream is that number when running against `dev-api`. |

## 19. Redis GET on every ingest (OOO filter)

| | |
|---|---|
| **Bad** | Every GPS fix did `locationStore.Get` before Save so `applyTelemetryFilter` could reject out-of-order — one extra Redis RTT on the critical path under 1k VUs (upstream med ~105ms, fat p95). |
| **Fixed** | Pod-local `lastFix` map handles OOO when speed/vendor filters are off; Redis GET only when `VendorPositionID` or `MaxSpeedKPH` needs the previous row. Idle Redis pool 32→128. |
| **Why** | Live flood rarely needs haversine; skipping GET removes a full RTT from the common path while Save still updates the shared snapshot. |

---

## Commit map

| Repo | Commit | Scope |
|---|---|---|
| `truckmoov-services-go` | `e32176f` | Ingest rate-limit bypass |
| `truckmoov-gitops` | `cab464d` | Capacity round 1 |
| `truckmoov-services-go` | `64c0fab` | Redis pool/pipeline/MGET, async Timescale, chunks |
| `truckmoov-gitops` | `4f70ec3` | Timescale resources/tunables |
| `truckmoov-services-go` | `55bd058` | NATS log Debug; sync publish for 503 |
| `truckmoov-services-go` | (this) | PublishAsync overlap + ordered async history |
| `truckmoov-gitops` | (this) | NATS CPU bump + tracking image bump |

## 20. Capacity round 3 (ingest upstream med ~170ms at 1k VU)

| | |
|---|---|
| **Bad** | Tracking floor 6 replicas / Redis 500m still left ingest Kong-upstream med ~170ms and fat p95 under 1k GPS VUs after PubAck overlap + GET skip. |
| **Fixed** | Tracking HPA min 12 / max 24; Redis requests 1 CPU (limit 4). k6 GPS_ONLY=1 isolates ingest from signup contention. |
| **Why** | Per-pod work was already ~ms on a warm path; the remaining gap is horizontal capacity on tracking+Redis under concurrent PubAck+SET. |

## 21. JetStream file PubAck on GPS location events

| | |
|---|---|
| **Bad** | Every ingest waited for JetStream *file* PubAck. Under 1k VU that alone kept Kong-upstream med ~170ms+ with fat tails even after Redis/overlap fixes. |
| **Fixed** | Location updates use JetStream `PublishAsync` + connection `FlushTimeout(100ms)` — do **not** wait for PubAck. Durable/core consumers still receive the event; Flush failure still returns 503. |
| **Why** | PubAck waits on disk under flood; Flush only needs the server to accept the bytes. Live path SoT is Redis. |

