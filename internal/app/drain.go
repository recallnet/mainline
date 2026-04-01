package app

import "strings"

func isIdleWorkerResult(result string) bool {
	return result == "No queued publish requests." || strings.HasPrefix(result, "No ready publish requests.")
}

func isBusyWorkerResult(result string) bool {
	return result == "Integration worker busy." || result == "Publish worker busy."
}

func drainRepoUntilSettled(repoPath string) (string, error) {
	last := ""
	for {
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
		if isIdleWorkerResult(result) || isBusyWorkerResult(result) {
			if last != "" {
				return last, nil
			}
			return result, nil
		}
	}
}
