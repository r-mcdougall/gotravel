// Flight search under load, through the gateway. Run with make loadtest.
// SCENARIO=steady (default): ramp to RATE requests per second in 1 minute, then hold it for HOLD.
// SCENARIO=stress: climb STEPS (requests per second), 30 s to reach each step and 1 min on it,
// to find where latency or errors break; read each step's latency from the Grafana dashboard.
import http from 'k6/http';
import { check } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const SCENARIO = __ENV.SCENARIO || 'steady';
const RATE = Number(__ENV.RATE || 100);
const HOLD = __ENV.HOLD || '3m';
const STEPS = (__ENV.STEPS || '100,250,500,750,1000').split(',').map(Number);

// make seed covers the 14 days from tomorrow; days 1 to 13 ahead in Jakarta stay inside it
// whichever time zone the seed counted "tomorrow" in.
const SEARCH_DAYS = 13;

// Most searches are plain; a few narrow or sort the day, as a real client would.
const REFINEMENTS = ['', '', '', '', '', '', '', '&airlines=GA,JT', '&max_price=1500000', '&sort=price_asc'];

const scenarios = {
    steady: {
        executor: 'ramping-arrival-rate',
        startRate: 10,
        timeUnit: '1s',
        preAllocatedVUs: 50,
        maxVUs: 300,
        stages: [
            { target: RATE, duration: '1m' },
            { target: RATE, duration: HOLD },
        ],
    },
    stress: {
        executor: 'ramping-arrival-rate',
        startRate: 10,
        timeUnit: '1s',
        preAllocatedVUs: 200,
        maxVUs: 1000,
        stages: STEPS.flatMap((target) => [
            { target, duration: '30s' },
            { target, duration: '1m' },
        ]),
    },
};

if (!scenarios[SCENARIO]) {
    throw new Error(`unknown SCENARIO ${SCENARIO}, use steady or stress`);
}

export const options = {
    scenarios: { [SCENARIO]: scenarios[SCENARIO] },
    // A failed threshold marks the run as failed but does not stop it, so a stress run climbs every step.
    thresholds: {
        http_req_duration: ['p(95)<500'],
        http_req_failed: ['rate<0.01'],
        checks: ['rate>0.99'],
        // Iterations k6 could not start because every VU was busy: the target rate was not really offered.
        dropped_iterations: ['count==0'],
    },
    summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

function pick(values) {
    return values[Math.floor(Math.random() * values.length)];
}

// searchDate returns a local date in Jakarta, where CGK is, between 1 and SEARCH_DAYS days ahead.
function searchDate() {
    const jakarta = new Date(Date.now() + 7 * 60 * 60 * 1000);
    jakarta.setUTCDate(jakarta.getUTCDate() + 1 + Math.floor(Math.random() * SEARCH_DAYS));
    return jakarta.toISOString().slice(0, 10);
}

export default function () {
    const refinement = pick(REFINEMENTS);
    const passengers = 1 + Math.floor(Math.random() * 3);
    const url = `${BASE_URL}/api/v1/flights?origin=CGK&destination=DPS&date=${searchDate()}&passengers=${passengers}${refinement}`;

    const res = http.get(url, { tags: { name: 'search' } });

    check(res, {
        'status is 200': (r) => r.status === 200,
        // A plain search must find the seeded flights; an empty day means the seed is missing or stale.
        'plain search finds flights': (r) => refinement !== '' || (r.status === 200 && r.json('meta.total') > 0),
    });
}
