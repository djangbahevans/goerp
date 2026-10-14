local updates = KEYS[1]
if redis.call('HGET', updates, 'dirty') ~= '1' then
    return 1
end
if redis.call('HGET', updates, 'revision') ~= ARGV[1] then
    return 0
end
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local last = tonumber(redis.call('HGET', updates, 'published_at') or '0')
if now - last < 250 then
    return 0
end
redis.call('PUBLISH', ARGV[2], ARGV[3])
redis.call('HSET', updates, 'dirty', 0, 'published_at', now)
redis.call('EXPIRE', updates, 120)
return 1
