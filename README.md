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
make psql              # open a psql session as the superuser
```

The API gateway listens on <http://localhost:8080> (`GATEWAY_PORT` in `.env`).
The RabbitMQ management UI is at <http://localhost:15672>.
Run `make` to list all available targets.

## API gateway

Nginx is the only public entry point. Unknown paths return `404`, and every
response carries an `X-Request-ID` header, taken from the request or generated.