-- +goose Up
ALTER TABLE publish_requests
ADD COLUMN priority TEXT NOT NULL DEFAULT 'normal';

-- +goose Down
SELECT 1;
