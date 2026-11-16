-- name: InsertBucketIfNotExists :exec
INSERT INTO rate_limit_buckets (
    key,
    tokens,
    last_refill_at,
    updated_at
) VALUES (
    @key,
    @capacity,
    clock_timestamp(),
    clock_timestamp()
)
ON CONFLICT (key) DO NOTHING;

-- name: GetRateLimitBucketForUpdate :one
SELECT key, tokens, last_refill_at, updated_at, clock_timestamp()::timestamptz AS db_time
FROM rate_limit_buckets
WHERE key = @key
FOR UPDATE;

-- name: UpdateRateLimitBucket :execrows
UPDATE rate_limit_buckets
SET
    tokens = @tokens,
    last_refill_at = @last_refill_at,
    updated_at = @updated_at
WHERE key = @key;

-- name: DeleteInactiveBuckets :execrows
DELETE FROM rate_limit_buckets
WHERE key IN (
    SELECT key FROM rate_limit_buckets
    WHERE updated_at < @cutoff::timestamptz
    ORDER BY updated_at ASC
    LIMIT @limit_count::bigint
);

-- name: GetRateLimitBucket :one
SELECT key, tokens, last_refill_at, updated_at
FROM rate_limit_buckets
WHERE key = @key
LIMIT 1;
