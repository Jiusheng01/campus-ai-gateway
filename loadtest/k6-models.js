import http from 'k6/http';
import { check } from 'k6';

const BASE_URL = (__ENV.BASE_URL || 'http://127.0.0.1:8080').replace(/\/$/, '');
const API_KEY = __ENV.API_KEY || '';
const TARGET_RPS = Number(__ENV.TARGET_RPS || 500);

export const options = {
  scenarios: {
    models: {
      executor: 'constant-arrival-rate',
      rate: TARGET_RPS,
      timeUnit: '1s',
      duration: __ENV.DURATION || '1m',
      preAllocatedVUs: Number(__ENV.PREALLOCATED_VUS || 100),
      maxVUs: Number(__ENV.MAX_VUS || 1000),
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<1000'],
  },
};

export default function () {
  if (!API_KEY) throw new Error('API_KEY is required');
  const res = http.get(BASE_URL + '/v1/models', {
    headers: { Authorization: 'Bearer ' + API_KEY },
  });
  check(res, { 'status is 200': (r) => r.status === 200 });
}
