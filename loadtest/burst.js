import http from 'k6/http';
import { Trend } from 'k6/metrics';

// Burst load test: ramp VUs up through the full containerized chain
// (k6 -> envoy -> extproc (gRPC) -> echo upstream).
//
// Tunables via k6 env vars (pass through just recipes or `k6 run -e`):
//   TEST_TARGET  base URL of the envoy listener (default http://localhost:8080)
//   VUS          target concurrency for the ramp (default 100)
//
// Run: just loadtest-burst

const baseUrl = __ENV.TEST_TARGET || 'http://localhost:8080';
const TARGET_VUS = parseInt(__ENV.VUS || '100', 10);

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
    burst: {
      executor: 'ramping-vus',
      stages: [
        { target: TARGET_VUS, duration: '10s' },
        { target: 0, duration: '10s' },
      ],
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
