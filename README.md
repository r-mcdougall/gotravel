# GoTravel

A flight booking backend inspired by Traveloka, built as a set of Go microservices.

> **Status:** work in progress. This README grows as services land.

## Scope

Phase 1 covers one-way, direct flights:

- Registration and login with JWT, profile editing
- Flight search by origin, destination, date and passenger count, with filters, sorting and pagination
- Seat reservation with a temporary hold, mock payment, then `CONFIRMED` or `EXPIRED`
- Admin: flight and capacity management, booking analytics

Round trips, multi-leg flights, seat maps, refunds and real payment gateways are out of scope.

## Services

| Service   | Responsibility                                             |
|-----------|------------------------------------------------------------|
| `auth`    | Users, registration, login, profile, JWT issuance          |
| `catalog` | Flight data, search, admin inventory endpoints             |
| `booking` | Seat inventory, holds, booking lifecycle, admin analytics  |
| `payment` | Mock payments, reports payment results to booking          |

Each service owns its own PostgreSQL database; there are no cross-database joins.

## Tech stack

Go, chi, PostgreSQL 17 (pgx, sqlc, goose), Redis, RabbitMQ, Nginx,
Prometheus, Grafana, Loki, Docker Compose.

## Getting started

Requirements: Docker with Compose v2, and `make`.

```sh
cp .env.example .env   # adjust values if needed
make up                # start the stack
make seed              # create demo flights (see "Demo data")
make psql              # open a psql session as the superuser
```

The API gateway listens on <http://localhost:8080> (`GATEWAY_PORT` in `.env`).
The RabbitMQ management UI is at <http://localhost:15672>.
Grafana is at <http://localhost:3000> (login from `.env`) with Prometheus and
Loki already provisioned as datasources; Prometheus is at <http://localhost:9090>.
Run `make` to list all available targets.

## Demo data

`make seed` fills the catalog with flights through the gateway, signed in as the
admin account from `.env` (`ADMIN_EMAIL`, `ADMIN_PASSWORD`). It creates 14 days
of flights starting tomorrow: 606 a day, 8,484 in total, from 8 airlines on 12
routes. One busy route, CGK to DPS, has 300 flights a day for load testing.

Running it again is safe: flights that already exist are skipped, so a later run
only adds the days that have come into range. Existing flights are never
changed. To start from scratch, run `make reset` (it deletes all volumes) and
`make up` again.

Then search for flights (`date` is the local date at the origin airport; on
macOS use `date -v+1d +%F`):

```sh
curl "http://localhost:8080/api/v1/flights?origin=CGK&destination=DPS&date=$(date -d tomorrow +%F)&passengers=2&sort=price_asc"
```

For the other options, run `go run ./tools/seed -h` with `ADMIN_EMAIL` and
`ADMIN_PASSWORD` set.

## API gateway

Nginx is the only public entry point. Unknown paths return `404`, and every
response carries an `X-Request-ID` header, taken from the request or generated
and forwarded to the service.

| Public path prefix                         | Service   |
|--------------------------------------------|-----------|
| `/api/v1/auth`, `/api/v1/users`            | `auth`    |
| `/api/v1/flights`, `/api/v1/admin/flights` | `catalog` |

Service `/healthz` and `/metrics` are not exposed through the gateway.
