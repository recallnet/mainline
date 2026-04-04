-- +goose Up
ALTER TABLE integration_submissions
ADD COLUMN allow_newer_head INTEGER NOT NULL DEFAULT 0;

-- +goose Down
SELECT 1;
