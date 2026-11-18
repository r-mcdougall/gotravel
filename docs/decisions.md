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

## 14. Reference data in migrations, times stored in UTC

- **Decision:** airlines and airports (with their IANA time zones) are inserted by a migration. Flights store UTC instants, and the API shows each time with the offset of its own airport. A flight is unique per airline, flight number and departure time. Prices are whole rupiah.
- **Rejected:** admin endpoints for airlines and airports (the set is fixed for this project); local times without an offset (ambiguous across Indonesia, Singapore, Malaysia and Japan).
- **Trade-off:** adding an airport or airline needs a migration. A search date is the local date at the origin airport, so a "day" is not a UTC day.

## 15. Search reads one route-day, the rest happens in Go

- **Decision:** search loads all flights of a route on one local day with a single static query, then filters, sorts and paginates in Go. Ties are broken by departure time, then id, so pages never repeat or skip a flight.
- **Rejected:** dynamic SQL for every filter combination (awkward for sqlc, and no two combinations could share a cache entry).
- **Trade-off:** every cache miss reads the whole day, about 300 rows on the busiest seeded route. A route with thousands of flights a day would need paging in SQL.

## 16. Redis caches the rows of a route-day, and is optional

- **Decision:** the rows of a route-day are cached for 60 seconds (`SEARCH_CACHE_TTL`), keyed by route and local date, so every search of that day shares one entry. An admin create or update deletes the affected day after commit (both the old and the new day when the schedule moves), and so does every seat report from booking (decision 28). With `REDIS_URL` empty the cache is off, and on any Redis error search reads the database. Redis is never the source of truth.
- **Rejected:** caching whole responses per query (low hit rate); deleting before commit (a search in between could cache the old data again).
- **Trade-off:** a search that read the database just before an admin write or a seat report can store old data for up to one TTL, so prices and `available_seats` can be up to 60 seconds old. While Redis is down, every search logs a warning (not rate limited) and can wait up to the 100 ms client timeout.

## 17. Capacity is checked locally until booking exists

- **Decision:** every create and update writes a `flight.changed` event to the outbox in the same transaction (decision 3). Until `booking` exists, lowering capacity is checked against the catalog's own `available_seats`, which moves by the same amount as capacity, and fails with 409 `CAPACITY_BELOW_SOLD`.
- **Rejected:** calling a booking service that does not exist yet.
- **Trade-off:** this is a stand-in for the synchronous check in decision 4 and is replaced when booking lands. Until then `available_seats` only changes through catalog writes.

## 18. Services verify JWTs with a public key mounted as a Docker secret

- **Decision:** only `auth` holds the private key (decision 10). Every service that verifies tokens (`catalog` now, `booking` and `payment` later) gets just the public key as a Docker secret and reads it from `JWT_PUBLIC_KEY_FILE` at start. Signature, expiry and the `admin` role are checked locally, with no call to `auth` per request.
- **Rejected:** asking `auth` to validate every token (every request would depend on it); passing the key in an environment variable (visible in `docker inspect`).
- **Trade-off:** a token stays valid until it expires, even if the user is demoted or deleted (decision 9), and rotating the key means restarting every verifying service (decision 10).

## 19. Demo data is created through the admin API

- **Decision:** `make seed` signs in as the admin and creates flights through `POST /api/v1/admin/flights`, from a deterministic plan: 606 flights a day for 14 days from tomorrow, 300 a day on CGK to DPS for load tests. Flights that already exist (409 `FLIGHT_EXISTS`) are skipped, never updated, and the run stops after 10 failures.
- **Rejected:** SQL inserts (they skip validation, outbox events and cache invalidation); a seed migration (demo data in every environment).
- **Trade-off:** about 8,500 requests instead of one bulk insert, and a changed plan does not update flights that already exist; `make reset` gives a clean start.

## 20. The outbox relay polls

- **Decision:** every service that publishes events runs a relay goroutine. It polls the outbox every 500 ms and takes up to 100 rows with `FOR UPDATE SKIP LOCKED`; a full batch is followed immediately by the next one. Rows are marked published only after the broker confirms every message of the batch. Failures back off up to 30 seconds.
- **Rejected:** `LISTEN/NOTIFY` (notifications are lost while the relay is down, so polling is still needed, plus a dedicated connection); publishing right after commit with a fallback sweep (two paths for the same job); the Watermill forwarder (its own table schema and message format).
- **Trade-off:** an event can wait up to 500 ms. A crash between the broker confirm and the commit publishes the batch again, which decision 3 already requires consumers to handle.

## 21. One relay in the platform package, one outbox shape everywhere

- **Decision:** `internal/platform/outbox` serves every service and queries a table named `outbox` with fixed columns (`id`, `event_type`, `payload`, `created_at`, `published_at`) through pgx. The relay builds the envelope `{event_id, type, occurred_at, data}` from the row, so services store only the event data, inserted through sqlc in the same transaction as the change.
- **Rejected:** a relay per service with sqlc queries (three copies of the same code and tests).
- **Trade-off:** the table shape is a convention that sqlc does not check against each service's migration. A service that changes its outbox breaks the relay at runtime. The relay tests use the same DDL as the migrations.

## 22. RabbitMQ topology

- **Decision:** one durable topic exchange per producing service (`catalog.events`, `booking.events`, `payment.events`) with the event type as routing key, and one quorum queue per consumer and event (`<consumer>.<event>`, for example `booking.flight-changed`). Producers declare their exchange; consumers declare the exchange, queue and binding, so services can start in any order. All services share one RabbitMQ user and vhost. Services use `internal/platform/messaging` and never import the AMQP client.
- **Rejected:** classic queues (quorum queues are the durable default in RabbitMQ 4 and bring a delivery limit); one RabbitMQ user per service (per-exchange permissions are not worth it here, unlike the separate databases in decision 1).
- **Trade-off:** any service can publish to any exchange. Changing a queue's arguments makes its declaration fail with `PRECONDITION_FAILED` until the queue is deleted (`make reset` in development).

## 23. Failed messages end in a dead-letter queue

- **Decision:** consumers ack manually with a prefetch of 10. A handler returns `messaging.ErrPermanent` for a message that can never succeed, such as one that cannot be decoded, and it is rejected straight to `<queue>.dlq`. Any other error is requeued after one second until the quorum delivery limit (20) moves the message to the dead-letter queue. Lost connections are re-dialed with a backoff from 1 to 30 seconds; a message interrupted by shutdown is left unacknowledged and returns to the queue.
- **Rejected:** requeueing forever (a poison message would loop without end); dropping failed messages; the experimental automatic recovery of `amqp091-go`.
- **Trade-off:** a database outage longer than the retries moves messages to the dead-letter queue, and moving them back is manual through the management UI.

## 24. Messages without a queue are dropped

- **Decision:** messages are published with `mandatory=false`, so the broker confirms and discards a message that no queue is bound for. Consumers create their queues when they start.
- **Rejected:** `mandatory=true` with return handling (rows would wait in the outbox until a consumer exists, but an unread return channel can stall the client); a definitions file that creates every queue when the broker starts (queue arguments written in two places).
- **Trade-off:** a consumer that starts after events were published never receives them. In development, run `make reset && make seed` after adding a consumer; a real deployment would need a backfill.

## 25. Seat counters live on the booking inventory

- **Decision:** `flight_inventories` keeps `held_seats` and `sold_seats` next to the capacity copied from the catalog. A hold is one conditional `UPDATE` (not departed, enough free seats) in the same transaction that inserts the booking, its passengers and an outbox event. Cancelling releases the seats the same way.
- **Rejected:** counting seats with `SUM` over bookings (every hold would have to lock the flight row first); `SELECT ... FOR UPDATE` and a check in Go (two round trips for the same guarantee).
- **Trade-off:** every status change must update the counters in its own transaction. A capacity lowered by the catalog can leave a negative balance, which is reported as zero free seats.

## 26. Holds are short, small and not deduplicated

- **Decision:** a hold lasts `HOLD_TTL` (15 minutes by default) and takes 1 to 9 passengers, the same limit as search. A passenger is a full name and a birth date. The seat price is copied into the booking when the hold is made; the total is the seat price times the passengers and is not stored.
- **Rejected:** identity or passport numbers (not needed for a mock payment, and sensitive); idempotency keys and a limit on holds per user.
- **Trade-off:** a retried request creates a second hold, and one user can hold many seats. Both are released when the holds expire, in line with the accepted risks of decision 11.

## 27. Bookings are private to their owner and use UTC

- **Decision:** every booking route needs an access token. Reads and cancel filter by the token's subject, so another user's booking answers 404, not 403. Times are returned in UTC because booking does not keep airport time zones.
- **Rejected:** 403 for other users' bookings (it confirms that the id exists); adding airport time zones to `flight.changed`.
- **Trade-off:** clients convert to local time themselves, unlike catalog responses, which use each airport's offset.

## 28. Booking reports seat availability with a version

- **Decision:** every change to a flight's free seats in booking (a hold, a cancel, an expiry and every applied `flight.changed`) increments `availability_version` and records `flight.availability_changed` with `flight_id`, `availability_version` and the absolute `available_seats`, in the same transaction. The catalog applies a report only when its version is newer and then forgets the cached route-day. An admin capacity change still moves `available_seats` in the catalog at once (decision 17); the next report from booking overwrites it.
- **Rejected:** seat deltas (a lost or repeated message would skew the count for good); reporting only when the capacity changes (the upsert would have to compare old and new values).
- **Trade-off:** search trails a hold by the relay and consumer delay. A price-only change sends a report that changes nothing, and the expirer sends one report per flight per batch.

## 29. Payment reads the bill from booking with the caller's token

- **Decision:** `POST /api/v1/payments` takes only a `booking_id`. Payment reads the booking from booking's public API with the caller's own `Authorization` header (2-second timeout), so booking checks ownership, and charges `total_price` only when the booking is `HELD` and `expires_at` is still ahead. The payment and its `payment.succeeded` event are written in one transaction.
- **Rejected:** the amount in the request (a client could pay any amount); a copy of bookings in payment fed by events (a second consumer and table for one read); a service-to-service token (new auth machinery for one call).
- **Trade-off:** payments need booking to be up; while it is not, payment answers 503 `BOOKING_UNAVAILABLE` and charges nothing. The deadline check uses payment's clock, and the booking can still expire before the confirmation arrives (decision 31).

## 30. The client picks the mock outcome; one success per booking

- **Decision:** the request's `outcome` (`succeeded` by default, or `failed`) decides the result. Every attempt is stored; a failed one sends no event and can be retried. A partial unique index allows one `SUCCEEDED` payment per booking, so a retried or concurrent second success answers 409 `ALREADY_PAID`.
- **Rejected:** a random outcome (tests and demos would not be repeatable); magic card numbers (realism nothing else uses); idempotency keys (the unique index already prevents a double charge).
- **Trade-off:** anyone can choose a failed payment, which is the point of a mock. A payment reversed by decision 31 frees the index, but the booking is no longer payable by then.

## 31. Bookings are confirmed by event; late payments are reversed

- **Decision:** booking consumes `payment.succeeded` and, in one transaction, confirms a booking that is still `HELD`, stores the `payment_id` and moves its seats from held to sold. Free seats do not change, so no availability report is sent. The expirer and the confirmation lock the same row, so exactly one wins. A booking that is no longer held answers with `payment.rejected` (`booking_expired`, `booking_cancelled`, `booking_not_found` or `booking_paid_by_another_payment`), and payment marks that payment `REVERSED`. A repeated message for a booking confirmed by the same payment changes nothing.
- **Rejected:** payment calling booking to confirm synchronously (a charged payment would need its own retry when booking is down); refusing a confirmation after `expires_at` (a payment made in time would lose to delivery delay); a refund flow.
- **Trade-off:** a paid booking stays `HELD` for the relay and consumer delay, about a second. A rejected payment shows `SUCCEEDED` until the reversal arrives, and nobody is notified of the reversal.
  