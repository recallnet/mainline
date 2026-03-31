package app

import (
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

func printVersion(w io.Writer, name string) {
	fmt.Fprintln(w, versionText(name))
}
