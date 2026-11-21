// Concurrent holds on one flight, through the gateway. Run with LOADTEST=holds make loadtest.
// VUS virtual users each try HOLDS_PER_VU holds of 1 to 3 passengers at once on a new flight with
// CAPACITY seats. The seats held must never exceed the capacity, and no hold may fail with 5xx.
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const CAPACITY = Number(__ENV.CAPACITY || 100);
const VUS = Number(__ENV.VUS || 200);
const HOLDS_PER_VU = Number(__ENV.HOLDS_PER_VU || 5);
const MAX_PASSENGERS = 3;

const heldSeats = new Counter('held_seats');
const JSON_HEADERS = { 'Content-Type': 'application/json' };

export const options = {
    scenarios: {
        holds: {
            executor: 'per-vu-iterations',
            vus: VUS,
            iterations: HOLDS_PER_VU,
            maxDuration: '2m',
        },
    },
    thresholds: {
        // Never more seats than the flight has, and at most MAX_PASSENGERS - 1 left over: a request for
        // 3 passengers is refused when only 2 seats remain.
        held_seats: [`count<=${CAPACITY}`, `count>=${CAPACITY - (MAX_PASSENGERS - 1)}`],
        // Holds answer 201 or 409 NOT_ENOUGH_SEATS; anything else is a failure.
        checks: ['rate==1'],
        'http_req_failed{name:hold}': ['rate==0'],
    },
    summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

function post(path, body, token, params = {}) {
    const headers = token ? { ...JSON_HEADERS, Authorization: `Bearer ${token}` } : JSON_HEADERS;
    return http.post(`${BASE_URL}${path}`, body === null ? null : JSON.stringify(body), { headers, ...params });
}

function login(email, password) {
    const res = post('/api/v1/auth/login', { email, password });
    if (res.status !== 200) {
        throw new Error(`login ${email}: ${res.status} ${res.body}`);
    }
    return res.json('access_token');
}

function passengers(count) {
    return Array.from({ length: count }, (_, i) => ({ full_name: `Passenger ${i + 1}`, birth_date: '1990-01-01' }));
}

// tomorrow returns tomorrow's date in Jakarta, where CGK is.
function tomorrow() {
    const jakarta = new Date(Date.now() + 7 * 60 * 60 * 1000);
    jakarta.setUTCDate(jakarta.getUTCDate() + 1);
    return jakarta.toISOString().slice(0, 10);
}

function createFlight(adminToken) {
    const date = tomorrow();
    const minute = String(Math.floor(Math.random() * 60)).padStart(2, '0');
    const res = post(
        '/api/v1/admin/flights',
        {
            flight_number: `GA${9000 + Math.floor(Math.random() * 1000)}`,
            airline_code: 'GA',
            origin_code: 'CGK',
            destination_code: 'DPS',
            departure_at: `${date}T09:${minute}:00+07:00`,
            arrival_at: `${date}T12:${minute}:00+08:00`,
            price: 1000000,
            capacity: CAPACITY,
        },
        adminToken,
    );
    if (res.status !== 201) {
        throw new Error(`create flight: ${res.status} ${res.body}`);
    }
    return res.json('id');
}

// waitForInventory holds one seat until booking knows the flight from catalog's event, then cancels
// that hold so the test starts with every seat free.
function waitForInventory(flightId, token) {
    for (let attempt = 0; attempt < 50; attempt++) {
        const res = post('/api/v1/bookings', { flight_id: flightId, passengers: passengers(1) }, token, {
            responseCallback: http.expectedStatuses(201, 404),
        });
        if (res.status === 201) {
            const cancel = post(`/api/v1/bookings/${res.json('id')}/cancel`, null, token);
            if (cancel.status !== 200) {
                throw new Error(`cancel the probe hold: ${cancel.status} ${cancel.body}`);
            }
            return;
        }
        if (res.status !== 404) {
            throw new Error(`probe hold: ${res.status} ${res.body}`);
        }
        sleep(0.2);
    }
    throw new Error('booking did not learn about the new flight within 10 seconds');
}

export function setup() {
    if (!__ENV.ADMIN_EMAIL || !__ENV.ADMIN_PASSWORD) {
        throw new Error('ADMIN_EMAIL and ADMIN_PASSWORD must be set');
    }
    const adminToken = login(__ENV.ADMIN_EMAIL, __ENV.ADMIN_PASSWORD);

    const email = `loadtest-${Date.now()}@example.com`;
    const password = 'correct horse battery';
    const register = post('/api/v1/auth/register', { email, password, full_name: 'Load Test' });
    if (register.status !== 201) {
        throw new Error(`register: ${register.status} ${register.body}`);
    }
    const token = login(email, password);

    const flightId = createFlight(adminToken);
    waitForInventory(flightId, token);
    console.log(`flight ${flightId} with ${CAPACITY} seats, ${VUS} VUs x ${HOLDS_PER_VU} holds`);
    return { flightId, token };
}

export default function ({ flightId, token }) {
    const count = 1 + Math.floor(Math.random() * MAX_PASSENGERS);
    const res = post('/api/v1/bookings', { flight_id: flightId, passengers: passengers(count) }, token, {
        tags: { name: 'hold' },
        responseCallback: http.expectedStatuses(201, 409),
    });

    const ok = check(res, {
        'hold is 201 or 409 NOT_ENOUGH_SEATS': (r) =>
            r.status === 201 || (r.status === 409 && r.json('error.code') === 'NOT_ENOUGH_SEATS'),
    });
    if (ok && res.status === 201) {
        heldSeats.add(count);
    }
}

// teardown prints what catalog shows once booking's availability events have arrived, to compare
// with held_seats and with booking's own tables.
export function teardown({ flightId }) {
    sleep(3);
    const res = http.get(`${BASE_URL}/api/v1/flights/${flightId}`);
    console.log(`catalog: flight ${flightId} has available_seats=${res.json('available_seats')}`);
}
