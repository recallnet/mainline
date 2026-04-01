package app

import (
	"context"
	"strings"
	"time"
)

func isIdleWorkerResult(result string) bool {
	return result == "No queued publish requests." || strings.HasPrefix(result, "No ready publish requests.")
}

func isBusyWorkerResult(result string) bool {
	return result == "Integration worker busy." || result == "Publish worker busy."
}

func nextScheduledRetry(result string) (time.Time, bool) {
	const prefix = "No ready publish requests. Next retry for request "
	if !strings.HasPrefix(result, prefix) {
		return time.Time{}, false
	}
	idx := strings.LastIndex(result, " at ")
	if idx == -1 {
		return time.Time{}, false
	}
	ts := strings.TrimSpace(result[idx+4:])
	retryAt, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}, false
	}
	return retryAt, true
}

func sleepUntilScheduledRetry(ctx context.Context, retryAt time.Time) error {
	wait := time.Until(retryAt)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func drainRepoUntilSettled(repoPath string) (string, error) {
	return drainRepoUntilSettledContext(context.Background(), repoPath)
}

func drainRepoUntilSettledContext(ctx context.Context, repoPath string) (string, error) {
	last := ""
	for {
		if err := ctx.Err(); err != nil {
			if last != "" {
				return last, err
			}
			return "", err
		}

		result, err := runOneCycle(repoPath)
		if result != "" && !isIdleWorkerResult(result) {
			last = result
		}
		if err != nil {
			if last != "" {
				return last, err
			}
			return result, err
		}
		if isBusyWorkerResult(result) {
			if last != "" {
				return last, nil
			}
			return result, nil
		}
		if retryAt, ok := nextScheduledRetry(result); ok {
			if err := sleepUntilScheduledRetry(ctx, retryAt); err != nil {
				if last != "" {
					return last, err
				}
				return result, err
			}
			continue
		}
		if isIdleWorkerResult(result) {
			if last != "" {
				return last, nil
			}
			return result, nil
		}
	}
}
