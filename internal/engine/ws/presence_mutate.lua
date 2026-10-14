local hash, updates = KEYS[1], KEYS[2]
local operation, connection, value, now, cutoff = ARGV[1], ARGV[2], ARGV[3], ARGV[4], ARGV[5]
local changed = false
local existing = redis.call('HGET', hash, connection)

if operation == 'join' then
    if not existing then
        redis.call('HSET', hash, connection, value)
    end
    changed = true
elseif operation == 'leave' then
    changed = redis.call('HDEL', hash, connection) > 0
elseif existing and (operation == 'focus' or operation == 'refresh') then
    local entry = cjson.decode(existing)
    entry.last_seen_at = now
    if operation == 'focus' then
        entry.editing_field = cjson.decode(value)
        entry.focused_at = now
        changed = true
    end
    redis.call('HSET', hash, connection, cjson.encode(entry))
end

local entries = redis.call('HGETALL', hash)
for i = 1, #entries, 2 do
    local entry = cjson.decode(entries[i + 1])
    if entry.last_seen_at <= cutoff then
        redis.call('HDEL', hash, entries[i])
        changed = true
    end
end

if changed then
    redis.call('HINCRBY', updates, 'revision', 1)
    redis.call('HSET', updates, 'dirty', 1)
end
redis.call('EXPIRE', hash, 120)
redis.call('EXPIRE', updates, 120)
return {redis.call('HGET', updates, 'revision') or '0', redis.call('HGET', updates, 'dirty') or '0', redis.call('HGETALL', hash)}
