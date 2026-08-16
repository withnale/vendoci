package cli

import (
	"fmt"
	"strings"

	"github.com/pterm/pterm"
)

// TablePrinter provides a simple colored table printer without boxes
type TablePrinter struct {
	headers []string
	rows    [][]string
	colors  bool
}

// NewTable creates a new table printer
func NewTable(headers ...string) *TablePrinter {
	return &TablePrinter{
		headers: headers,
		rows:    [][]string{},
		colors:  true, // Enable colors by default
	}
}

// DisableColors turns off colored output
func (t *TablePrinter) DisableColors() {
	t.colors = false
}

// AddRow adds a row to the table
func (t *TablePrinter) AddRow(values ...string) {
	t.rows = append(t.rows, values)
}

// Render prints the table to stdout
func (t *TablePrinter) Render() {
	if len(t.rows) == 0 {
		return
	}

	// Calculate column widths
	widths := make([]int, len(t.headers))
	for i, header := range t.headers {
		widths[i] = len(header)
	}

	for _, row := range t.rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Print header
	if t.colors {
		headerParts := make([]string, len(t.headers))
		for i, header := range t.headers {
			headerParts[i] = t.pad(pterm.FgCyan.Sprint(header), widths[i], len(header))
		}
		fmt.Println(strings.Join(headerParts, "  "))
	} else {
		headerParts := make([]string, len(t.headers))
		for i, header := range t.headers {
			headerParts[i] = t.pad(header, widths[i], len(header))
		}
		fmt.Println(strings.Join(headerParts, "  "))
	}

	// Print rows
	for _, row := range t.rows {
		rowParts := make([]string, len(t.headers))
		for i := range t.headers {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}

			// Apply colors based on column and content
			displayCell := cell
			if t.colors && cell != "" && cell != "-" {
				switch t.headers[i] {
				case "GROUP":
					displayCell = pterm.FgLightYellow.Sprint(cell)
				case "NAME":
					displayCell = pterm.FgLightGreen.Sprint(cell)
				case "TYPE":
					if cell == "helm" {
						displayCell = pterm.FgLightMagenta.Sprint(cell)
					} else {
						displayCell = pterm.FgLightBlue.Sprint(cell)
					}
				case "SOURCE", "DEST":
					if cell == "PRESENT" {
						displayCell = pterm.FgGreen.Sprint(cell)
					} else if cell == "MISSING" {
						displayCell = pterm.FgRed.Sprint(cell)
					} else if cell == "UNKNOWN" {
						displayCell = pterm.FgYellow.Sprint(cell)
					}
				case "STATUS":
					if strings.Contains(cell, "✓") || cell == "OK" {
						displayCell = pterm.FgGreen.Sprint(cell)
					} else if strings.Contains(cell, "✗") || cell == "FAILED" {
						displayCell = pterm.FgRed.Sprint(cell)
					} else if cell == "SKIPPED" {
						displayCell = pterm.FgYellow.Sprint(cell)
					}
				case "VERSION", "TAG":
					displayCell = pterm.FgCyan.Sprint(cell)
				}
			}

			rowParts[i] = t.pad(displayCell, widths[i], len(cell))
		}
		fmt.Println(strings.Join(rowParts, "  "))
	}
}

// pad pads a string (that may contain ANSI codes) to the specified width
// actualLen is the visible length without ANSI codes
func (t *TablePrinter) pad(s string, width int, actualLen int) string {
	if actualLen >= width {
		return s
	}
	// Add padding to reach desired width
	padding := width - actualLen
	return s + strings.Repeat(" ", padding)
}
