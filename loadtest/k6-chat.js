import http from 'k6/http';
import { check } from 'k6';

const BASE_URL = (__ENV.BASE_URL || 'http://127.0.0.1:8080').replace(/\/$/, '');
const API_KEY = __ENV.API_KEY || '';
const MODEL = __ENV.MODEL || 'campus-smart';
const TARGET_RPS = Number(__ENV.TARGET_RPS || 500);
const DURATION = __ENV.DURATION || '2m';

export const options = {
  scenarios: {
    chat: {
      executor: 'constant-arrival-rate',
      rate: TARGET_RPS,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: Number(__ENV.PREALLOCATED_VUS || 200),
      maxVUs: Number(__ENV.MAX_VUS || 2000),
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<5000'],
  },
};

export default function () {
  if (!API_KEY) {
    throw new Error('API_KEY is required');
  }
  const payload = JSON.stringify({
    model: MODEL,
    messages: [{ role: 'user', content: 'Reply with OK' }],
    max_tokens: 8,
    stream: false,
  });
  const res = http.post(BASE_URL + '/v1/chat/completions', payload, {
    headers: {
      Authorization: 'Bearer ' + API_KEY,
      'Content-Type': 'application/json',
    },
    timeout: '30s',
  });
  check(res, { 'status is 200': (r) => r.status === 200 });
}
