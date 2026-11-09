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

## 8. Opaque refresh tokens with rotation and reuse detection

- **Decision:** refresh tokens are random opaque strings and only their SHA-256 hash is stored. Every refresh revokes the old token and issues a new pair in one transaction. Presenting a revoked token revokes all sessions of that user. Access tokens live 15 minutes.
- **Rejected:** JWT refresh tokens (revocation needs stored state anyway); single-use tokens without reuse detection (a stolen token could be used silently).
- **Trade-off:** detection is strict. Two concurrent refreshes with the same token, or replaying a token after logout, log the user out everywhere. Revoked and expired rows are kept because reuse detection needs them.

## 9. Sliding sessions, logout revokes only the refresh token

- **Decision:** the refresh token lasts 7 days and the window restarts on every refresh. Logout revokes the given refresh token and is idempotent.
- **Rejected:** an absolute session limit (extra setting and a forced login for active users); a denylist for access tokens (every service would need to check it).
- **Trade-off:** an active client stays signed in indefinitely, and an access token stays valid for up to 15 minutes after logout.

## 10. Signing key loaded from a file, no JWKS

- **Decision:** `auth` reads its private key from `JWT_PRIVATE_KEY_FILE` (a Docker secret in compose). Tokens carry no `kid`, and there is no JWKS endpoint.
- **Rejected:** a JWKS endpoint with key IDs (more moving parts than a stateless, quickly restarted system needs).
- **Trade-off:** rotating the key means replacing it and restarting the services. Access tokens signed with the old key stop working (at most 15 minutes of impact); refresh tokens are unaffected because they are not JWTs.

## 11. Account enumeration and brute force are accepted risks

- **Decision:** register answers 409 `EMAIL_TAKEN` for a known email. Login gives the same error for an unknown email and a wrong password, and compares against a dummy bcrypt hash so both take about the same time.
- **Rejected:** hiding existing emails at registration (needs email verification); rate limiting (out of scope for this project).
- **Trade-off:** registered emails can be discovered through the register endpoint, and passwords can be guessed without throttling. The timing equalization is best effort, not a guarantee.

## 12. First admin created from the environment

- **Decision:** at start, `auth` creates the account in `ADMIN_EMAIL` and `ADMIN_PASSWORD` with a single `INSERT ... ON CONFLICT DO NOTHING`. It never overwrites a password and never promotes an existing user. Register always creates role `user`.
- **Rejected:** a promotion endpoint (privilege escalation surface); a seed migration (password in the repository); a manual SQL step (not part of `docker compose up`).
- **Trade-off:** the admin password sits in the environment, and changing it later does not change an existing account. Invalid values stop the service from starting.

## 13. Auth is one package without a repository layer

- **Decision:** handler, service and request/response types live in `services/auth/internal/auth`. The sqlc `Queries` type is the data layer. The service depends on small interfaces declared next to it, so unit tests use fakes without a database.
- **Rejected:** separate model, service and repository packages (indirection that pays off only with more operations or several storage backends).
- **Trade-off:** the package grows with every feature. Split it when navigating it gets hard, not before.
  