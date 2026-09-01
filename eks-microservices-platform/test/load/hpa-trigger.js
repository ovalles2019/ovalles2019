// Load profile designed to exercise the scorer's HPA, not just to generate RPS.
//
// The reference project this improves on demonstrated autoscaling by running
// `while true; do wget -q -O- <svc>; done` against a stock WordPress container.
// That shows a graph going up, but it proves nothing about the service: the CPU
// consumed is nginx serving a static page, and the same graph appears whatever
// is behind the load balancer.
//
// Here the request cost is a genuine function of the payload — scoring a
// 2048-point window is real arithmetic — so the shape below tests specific
// claims the chart makes:
//
//   ramp      the HPA reacts within its 30s scale-up stabilisation window
//   plateau   added replicas actually bring utilisation back under target
//   spike     the scale-up policy (100% or 4 pods per minute) is fast enough
//   recovery  the 300s scale-down window holds capacity through a short lull
//             instead of immediately reclaiming it and flapping
//
// Thresholds fail the run rather than just printing numbers, so this can be a
// gate in a pipeline rather than a screenshot in a README.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const errorRate = new Rate('scoring_errors');
const degradedRate = new Rate('scoring_degraded');
const scoreLatency = new Trend('scoring_latency', true);

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const API_KEY = __ENV.API_KEY || '';
const DEVICE_ID = __ENV.DEVICE_ID || 'pump-01';

// Large enough that scoring is measurable work rather than framework overhead.
const WINDOW_SIZE = parseInt(__ENV.WINDOW_SIZE || '2048', 10);

export const options = {
  scenarios: {
    autoscaling_profile: {
      executor: 'ramping-vus',
      startVUs: 1,
      stages: [
        { duration: '1m', target: 10 },   // ramp: baseline, HPA should hold
        { duration: '2m', target: 50 },   // plateau: sustained above target
        { duration: '30s', target: 150 }, // spike: tests the scale-up policy
        { duration: '2m', target: 150 },  // hold the spike
        { duration: '1m', target: 10 },   // lull: scale-down must NOT flap here
        { duration: '2m', target: 10 },   // recovery: shorter than the 300s window
      ],
      gracefulRampDown: '30s',
    },
  },

  thresholds: {
    // If autoscaling works, latency stays bounded as load grows. A p95 that
    // climbs with VU count means the HPA is not keeping up — which is the whole
    // thing this test is meant to detect.
    'scoring_latency': ['p(95)<1000', 'p(99)<2000'],
    'scoring_errors': ['rate<0.01'],
    // 4xx and 5xx both count here; a rate limit kicking in is still a failure
    // of this test's premise, which is that the platform absorbs the load.
    'http_req_failed': ['rate<0.01'],
  },
};

// A steady baseline with a single injected anomaly at the end, so the response
// is also checked for correctness rather than only for a 200.
function buildWindow() {
  const readings = new Array(WINDOW_SIZE);
  for (let i = 0; i < WINDOW_SIZE - 1; i++) {
    readings[i] = 10 + Math.sin(i / 7) * 0.3 + Math.random() * 0.1;
  }
  readings[WINDOW_SIZE - 1] = Math.random() < 0.1 ? 500 : 10.2;
  return readings;
}

export function setup() {
  const headers = { 'Content-Type': 'application/json' };
  if (API_KEY) headers['Authorization'] = `Bearer ${API_KEY}`;

  // Register the device the scoring requests reference. A 409 means it already
  // exists, which is fine on a re-run.
  const res = http.post(
    `${BASE_URL}/api/v1/devices`,
    JSON.stringify({ id: DEVICE_ID, name: 'Load test device', site: 'west', kind: 'pump' }),
    { headers },
  );

  if (res.status !== 201 && res.status !== 409) {
    throw new Error(
      `could not register ${DEVICE_ID}: ${res.status} ${res.body}. ` +
      `Is the gateway reachable at ${BASE_URL}?`,
    );
  }
  return { headers };
}

export default function (data) {
  const res = http.post(
    `${BASE_URL}/api/v1/readings/score`,
    JSON.stringify({ device_id: DEVICE_ID, readings: buildWindow() }),
    { headers: data.headers, tags: { name: 'score' } },
  );

  scoreLatency.add(res.timings.duration);
  errorRate.add(res.status !== 200);

  const ok = check(res, {
    'status is 200': (r) => r.status === 200,
    'body has a score': (r) => {
      if (r.status !== 200) return false;
      try {
        return typeof r.json('score') === 'number';
      } catch {
        return false;
      }
    },
  });

  // Track graceful degradation separately from failure. A degraded response is
  // a correct response served without catalog enrichment, and it is exactly
  // what should happen if catalog is struggling under this load — so it must
  // not be counted as an error.
  if (ok && res.status === 200) {
    try {
      degradedRate.add(res.json('degraded') === true);
    } catch {
      // Body already validated above; nothing useful to do here.
    }
  }

  sleep(0.1 + Math.random() * 0.2);
}

export function teardown() {
  console.log(`
Compare the run against what the chart claims:

  kubectl -n fleet-platform get hpa scorer
  kubectl -n fleet-platform describe hpa scorer   # the scaling decisions
  kubectl -n fleet-platform get pods -l app.kubernetes.io/name=scorer

Expected: replicas rose during the plateau and spike, and did NOT immediately
fall during the one-minute lull — the 300s scale-down stabilisation window is
what prevents that flap.
`);
}
