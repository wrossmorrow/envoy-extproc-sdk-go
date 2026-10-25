import http from 'k6/http';
import { Trend } from 'k6/metrics';

// Delayed/upstream-latency load test: send requests through the full
// containerized chain with an artificial upstream delay (via the echo
// server's `X-Echo-Delay` header) and/or an injected upstream status (via
// `X-Echo-Status`).
//
// Tunables via k6 env vars (pass through just recipes or `k6 run -e`):
//   TEST_TARGET  base URL of the envoy listener (default http://localhost:8080)
//   RPS          requests per second (default 10)
//   DURATION     test duration (default 30s)
//   ECHO_DELAY   upstream delay in seconds (default 2, e.g. "2" or "2s")
//   ECHO_STATUS  optional upstream status to inject (e.g. "503"); empty = none
//
// Run: just loadtest-delayed

const baseUrl = __ENV.TEST_TARGET || 'http://localhost:8080';
const RPS = parseInt(__ENV.RPS || '10', 10);
const DURATION = __ENV.DURATION || '30s';
const ECHO_DELAY = __ENV.ECHO_DELAY || '2';
const ECHO_STATUS = __ENV.ECHO_STATUS || '';

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

function expectedStatus() {
  if (ECHO_STATUS) {
    const status = parseInt(ECHO_STATUS, 10);
    return { min: status, max: status };
  }
  // No status injection: expect a 2xx echo response.
  return { min: 200, max: 299 };
}

export const options = {
  scenarios: {
    delayed: {
      executor: 'constant-arrival-rate',
      rate: RPS,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 10,
      maxVUs: 100,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<5000'],
  },
};

http.setResponseCallback(http.expectedStatuses(expectedStatus()));

export default function () {
  const headers = {
    'Content-Type': 'application/json',
  };
  if (ECHO_DELAY) {
    headers['X-Echo-Delay'] = ECHO_DELAY;
  }
  if (ECHO_STATUS) {
    headers['X-Echo-Status'] = ECHO_STATUS;
  }
  const res = http.post(`${baseUrl}/`, JSON.stringify({}), { headers });
  const durNs = getHeaderValue(res, 'x-extproc-duration-ns');
  if (durNs) {
    extprocDuration.add(parseInt(durNs, 10) / 1e6);
  }
}
