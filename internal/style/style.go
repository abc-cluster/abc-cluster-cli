// Package style provides the abc CLI's three-color terminal palette.
//
// Colors are suppressed automatically when stdout is not a TTY (pipes,
// redirects, CI environments with NO_COLOR set).
//
// Palette:
//
//	R — red    archived / dead-end / failed / destructive confirmations
//	G — green  active / complete / create-update confirmations
//	B — blue   column headers, key labels, IDs
//	Dim        timestamps and secondary detail
package style

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// enabled is true when stdout is a terminal and the user has not requested
// no-color via the NO_COLOR environment variable (https://no-color.org).
var enabled = term.IsTerminal(int(os.Stdout.Fd())) &&
	os.Getenv("NO_COLOR") == ""

const (
	esc   = "\033["
	reset = "\033[0m"
	cRed  = "\033[31m"
	cGrn  = "\033[32m"
	cBlu  = "\033[34m"
	cDim  = "\033[2m"
	cBold = "\033[1m"
)

func wrap(code, s string) string {
	if !enabled || s == "" {
		return s
	}
	return code + s + reset
}

// R wraps s in red — use for archived, dead-end, failed states and
// destructive-action confirmations.
func R(s string) string { return wrap(cRed, s) }

// G wraps s in green — use for active/complete states and create/update
// confirmations.
func G(s string) string { return wrap(cGrn, s) }

// B wraps s in blue — use for column headers and key labels in tabular output.
func B(s string) string { return wrap(cBlu, s) }

// Dim wraps s in dim — use for timestamps and secondary detail.
func Dim(s string) string { return wrap(cDim, s) }

// Bold wraps s in bold — use sparingly for the active-item indicator.
func Bold(s string) string { return wrap(cBold, s) }

// Status applies R or G based on the canonical status string. Unknown values
// are returned unchanged.
func Status(s string) string {
	switch strings.ToLower(s) {
	case "active":
		return G(s)
	case "complete", "completed":
		return G(s)
	case "archived", "dead-end", "deadend", "failed":
		return R(s)
	default:
		return s
	}
}

// Header returns a tab-separated header row with each column name wrapped in B.
// Pass the column labels as individual arguments; they are joined with "\t".
func Header(cols ...string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = B(c)
	}
	return strings.Join(out, "\t")
}
