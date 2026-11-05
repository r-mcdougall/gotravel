# Decisions

## 1. One PostgreSQL instance, one database per service

- **Decision:** a single instance with a separate database and role per service.
- **Rejected:** four instances (too heavy for this project); one shared database (couples services through the schema).
- **Trade-off:** the instance is a shared point of failure. No cross-database joins, so data from other services is fetched over HTTP or snapshotted.

## 2. RabbitMQ for events, REST for requests

- **Decision:** payment results, seat release and availability updates go through RabbitMQ. Everything that needs an immediate answer (hold, search, auth) stays synchronous.
- **Rejected:** REST everywhere (a booking outage would lose payment results and seat releases); Kafka (more than this system needs).
- **Trade-off:** search data becomes eventually consistent, and there is one more component to run.

## 3. Transactional outbox

- **Decision:** events are written to an outbox table in the same transaction as the data change, then published by a relay worker.
- **Rejected:** publishing directly after commit (a crash in between loses the event).
- **Trade-off:** delivery is at-least-once, so every consumer must be idempotent.

## 4. Booking owns seat inventory

- **Decision:** `booking` keeps the seat counts, updated from `catalog` events, so a hold is a single local transaction.
- **Rejected:** `catalog` owns seats and `booking` reserves over HTTP (one hold would touch two databases and need compensation).
- **Trade-off:** capacity changes reach `booking` asynchronously and cannot be rejected. Lowering capacity below the seats already sold is therefore validated with a synchronous call from `catalog` to `booking`.

## 5. JWT signed with EdDSA

- **Decision:** `auth` signs with an Ed25519 private key; other services only hold the public key.
- **Rejected:** HS256 (every service would share the signing secret); RS256 (works, but larger keys and more settings to get wrong).
- **Trade-off:** less universal support than RS256. If third parties ever verify tokens, switch to RS256.

## 6. One Go module with a shared platform package

- **Decision:** a single `go.mod`, one Dockerfile and one CI pipeline. `internal/platform` holds infrastructure only, never domain logic.
- **Rejected:** one module per service (too much overhead for one developer).
- **Trade-off:** services share one dependency set. The infrastructure-only rule keeps shared code from coupling them.

## 7. Nginx as the only public entry point

- **Decision:** public routes are an explicit allowlist; service health and metrics endpoints are not exposed.
- **Rejected:** exposing each service directly.
- **Trade-off:** each new service needs a route added.
