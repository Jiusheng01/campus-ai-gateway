package ratelimit

import "github.com/redis/go-redis/v9"

// incrWithTTLScript increments a counter and guarantees it has a TTL, even when
// the key already existed without one. A counter that loses its expiry would
// lock a source out permanently, which is the failure mode this guards against.
//
// It returns the new value and the remaining TTL so the caller can tell the
// client how long to wait without a second round trip.
var incrWithTTLScript = redis.NewScript(`
local current = redis.call('INCR', KEYS[1])
if current == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
local ttl = redis.call('TTL', KEYS[1])
if ttl < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
  ttl = ARGV[1]
end
return {current, ttl}
`)
