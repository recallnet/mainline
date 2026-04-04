-- +goose Up
ALTER TABLE publish_requests
ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE publish_requests
ADD COLUMN next_attempt_at DATETIME;

-- +goose Down
SELECT 1;
