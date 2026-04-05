package app

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestOperatorCommandsUseStepPrinter(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	appDir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(appDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", appDir, err)
	}

	banned := regexp.MustCompile(`fmt\.Fprint(?:f|ln)?\(\s*stdout\b|fmt\.Print(?:f|ln)?\(`)
	violations := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(appDir, name)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		if banned.Match(content) {
			violations = append(violations, name)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("operator-facing commands must use stepPrinter, found raw stdout formatting in %v", violations)
	}
}
