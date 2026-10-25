import http from 'k6/http';
import { Trend } from 'k6/metrics';

// Smoke test: single request through the full containerized chain
// (k6 -> envoy -> extproc (gRPC) -> echo upstream).
//
// Target: TEST_TARGET (default http://localhost:8080, i.e. the envoy listener
// published by test/compose.yaml).
// Run: just loadtest-smoke

const baseUrl = __ENV.TEST_TARGET || 'http://localhost:8080';

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
    smoke: {
      executor: 'per-vu-iterations',
      vus: 1,
      iterations: 1,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<1000'],
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
