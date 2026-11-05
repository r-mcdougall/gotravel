-- Creates one database and one owner role per service.
--
-- Runs once, only when the postgres data volume is empty
-- (docker-entrypoint-initdb.d). Tables are NOT created here:
-- each service owns its schema through its own migrations.
--
-- Passwords are read from the container environment with psql \getenv,
-- so no secrets live in this file.

\set ON_ERROR_STOP on

\getenv auth_password AUTH_DB_PASSWORD
\getenv catalog_password CATALOG_DB_PASSWORD
\getenv booking_password BOOKING_DB_PASSWORD
\getenv payment_password PAYMENT_DB_PASSWORD

-- auth
CREATE ROLE auth_svc LOGIN PASSWORD :'auth_password';
CREATE DATABASE auth OWNER auth_svc;
REVOKE CONNECT ON DATABASE auth FROM PUBLIC;

-- catalog
CREATE ROLE catalog_svc LOGIN PASSWORD :'catalog_password';
CREATE DATABASE catalog OWNER catalog_svc;
REVOKE CONNECT ON DATABASE catalog FROM PUBLIC;

-- booking
CREATE ROLE booking_svc LOGIN PASSWORD :'booking_password';
CREATE DATABASE booking OWNER booking_svc;
REVOKE CONNECT ON DATABASE booking FROM PUBLIC;

-- payment
CREATE ROLE payment_svc LOGIN PASSWORD :'payment_password';
CREATE DATABASE payment OWNER payment_svc;
REVOKE CONNECT ON DATABASE payment FROM PUBLIC;