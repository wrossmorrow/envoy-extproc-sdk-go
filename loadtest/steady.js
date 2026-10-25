import http from 'k6/http';
import { Trend } from 'k6/metrics';

// Steady-state load test: constant arrival rate through the full containerized
// chain (k6 -> envoy -> extproc (gRPC) -> echo upstream).
//
// Tunables via k6 env vars (pass through just recipes or `k6 run -e`):
//   TEST_TARGET  base URL of the envoy listener (default http://localhost:8080)
//   RPS          requests per second (default 25)
//   DURATION     test duration (default 60s)
//
// Run: just loadtest-steady

const baseUrl = __ENV.TEST_TARGET || 'http://localhost:8080';
const RPS = parseInt(__ENV.RPS || '25', 10);
const DURATION = __ENV.DURATION || '60s';

const extprocDuration = new Trend('extproc_duration', 'ExtProc processing time in milliseconds');

function getHeaderValue(res, name) {
  const wanted = name.toLowerCase();
  for (const k of Object.keys(res.headers)) {
    if (k.toLowerCase() === wanted) {
      const v = res.headers[k];
      if (v !== undefined && v !== null) return v;
    }
  }
  return undefined;
}

export const options = {
  scenarios: {
    steady: {
      executor: 'constant-arrival-rate',
      rate: RPS,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 20,
      maxVUs: 200,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<2000'],
  },
};

http.setResponseCallback(http.expectedStatuses({ min: 200, max: 299 }));

export default function () {
  const res = http.post(`${baseUrl}/`, JSON.stringify({}), {
    headers: {
      'Content-Type': 'application/json',
    },
  });
  const durNs = getHeaderValue(res, 'x-extproc-duration-ns');
  if (durNs) {
    extprocDuration.add(parseInt(durNs, 10) / 1e6);
  }
}
