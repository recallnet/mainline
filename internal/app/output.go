package app

import (
	"fmt"
	"io"
	"time"

	"github.com/pterm/pterm"
)

type stepPrinter struct {
	writer io.Writer
}

func newStepPrinter(writer io.Writer) *stepPrinter {
	return &stepPrinter{writer: writer}
}

func (p *stepPrinter) Raw() io.Writer {
	if p == nil {
		return io.Discard
	}
	return p.writer
}

func (p stepPrinter) Section(format string, args ...any) {
	pterm.DefaultSection.
		WithWriter(p.writer).
		WithTopPadding(0).
		WithBottomPadding(0).
		Printfln(format, args...)
}

func (p stepPrinter) Line(format string, args ...any) {
	pterm.DefaultBasicText.WithWriter(p.writer).Printfln(format, args...)
}

func (p stepPrinter) Success(format string, args ...any) {
	pterm.Success.WithWriter(p.writer).Printfln(format, args...)
}

func (p stepPrinter) Info(format string, args ...any) {
	pterm.Info.WithWriter(p.writer).Printfln(format, args...)
}

func (p stepPrinter) Warning(format string, args ...any) {
	pterm.Warning.WithWriter(p.writer).Printfln(format, args...)
}

func (p stepPrinter) Bullet(items ...string) {
	if len(items) == 0 {
		return
	}
	listItems := make([]pterm.BulletListItem, 0, len(items))
	for _, item := range items {
		listItems = append(listItems, pterm.BulletListItem{Text: item})
	}
	_ = pterm.DefaultBulletList.WithWriter(p.writer).WithItems(listItems).Render()
}

func formatBool(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func formatDurationMS(ms int64) string {
	return fmt.Sprintf("%s", (time.Duration(ms) * time.Millisecond).Round(time.Millisecond))
}
