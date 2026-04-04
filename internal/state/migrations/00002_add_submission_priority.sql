-- +goose Up
ALTER TABLE integration_submissions
ADD COLUMN priority TEXT NOT NULL DEFAULT 'normal';

-- +goose Down
SELECT 1;
