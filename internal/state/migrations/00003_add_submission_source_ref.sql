-- +goose Up
ALTER TABLE integration_submissions
ADD COLUMN source_ref TEXT NOT NULL DEFAULT '';

ALTER TABLE integration_submissions
ADD COLUMN ref_kind TEXT NOT NULL DEFAULT 'branch';

UPDATE integration_submissions
SET source_ref = CASE
	WHEN source_ref <> '' THEN source_ref
	WHEN branch_name <> '' THEN branch_name
	ELSE source_sha
END,
    ref_kind = CASE
	WHEN ref_kind <> '' THEN ref_kind
	WHEN branch_name <> '' THEN 'branch'
	ELSE 'sha'
END;

-- +goose Down
SELECT 1;
