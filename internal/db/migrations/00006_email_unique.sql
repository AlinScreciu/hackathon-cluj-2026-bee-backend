-- +goose Up

-- Case-insensitive unique index on email so login-by-email never picks the
-- wrong user even if rows differ only in case. The companion query
-- GetUserByEmail filters with LOWER(email) = LOWER($1) so it hits this index.
CREATE UNIQUE INDEX users_email_lower_key ON users (LOWER(email));

-- +goose Down

DROP INDEX users_email_lower_key;
