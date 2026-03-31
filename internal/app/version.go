package app

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func versionText(name string) string {
	var fields []string
	fields = append(fields, fmt.Sprintf("%s %s", name, Version))
	if Commit != "" && Commit != "unknown" {
		fields = append(fields, fmt.Sprintf("commit=%s", Commit))
	}
	if Date != "" && Date != "unknown" {
		fields = append(fields, fmt.Sprintf("date=%s", Date))
	}
	return strings.Join(fields, " ")
}

type versionResult struct {
	Program string `json:"program"`
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

func printVersion(w io.Writer, name string, asJSON bool) {
	if asJSON {
		result := versionResult{
			Program: name,
			Version: Version,
		}
		if Commit != "" && Commit != "unknown" {
			result.Commit = Commit
		}
		if Date != "" && Date != "unknown" {
			result.Date = Date
		}
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(result)
		return
	}
	fmt.Fprintln(w, versionText(name))
}
