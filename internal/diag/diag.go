// Package diag holds diagnostics: errors and warnings tied to a place in a
// file.
package diag

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Severity of a diagnostic.
type Severity int

// Severities.
const (
	Error Severity = iota
	Warning
)

func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// MarshalText encodes the severity as its name.
func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText reads a severity's name, as in a saved plan.
func (s *Severity) UnmarshalText(b []byte) error {
	switch string(b) {
	case "error":
		*s = Error
	case "warning":
		*s = Warning
	default:
		return fmt.Errorf("unknown severity %q", b)
	}
	return nil
}

// Pos is a 1-based line and column. The zero Pos means "the whole file".
type Pos struct {
	Line   int `json:"line,omitempty"`
	Column int `json:"column,omitempty"`
}

// Diagnostic is one finding.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	File     string   `json:"file,omitempty"`
	Pos
	Message string `json:"message"`
}

// String formats the diagnostic as file:line:col: severity: message.
func (d Diagnostic) String() string {
	var b strings.Builder
	if d.File != "" {
		b.WriteString(d.File)
		if d.Line > 0 {
			fmt.Fprintf(&b, ":%d", d.Line)
			if d.Column > 0 {
				fmt.Fprintf(&b, ":%d", d.Column)
			}
		}
		b.WriteString(": ")
	}
	fmt.Fprintf(&b, "%s: %s", d.Severity, d.Message)
	return b.String()
}

// Errorf returns an error diagnostic.
func Errorf(file string, pos Pos, format string, args ...any) Diagnostic {
	return Diagnostic{Severity: Error, File: file, Pos: pos, Message: fmt.Sprintf(format, args...)}
}

// Warnf returns a warning diagnostic.
func Warnf(file string, pos Pos, format string, args ...any) Diagnostic {
	return Diagnostic{Severity: Warning, File: file, Pos: pos, Message: fmt.Sprintf(format, args...)}
}

// List is a list of diagnostics.
type List []Diagnostic

// Count returns the number of errors and warnings.
func (l List) Count() (errors, warnings int) {
	for _, d := range l {
		if d.Severity == Error {
			errors++
		} else {
			warnings++
		}
	}
	return errors, warnings
}

// HasErrors reports whether the list holds an error.
func (l List) HasErrors() bool {
	n, _ := l.Count()
	return n > 0
}

// Sort orders the list by file, position and message.
func (l List) Sort() {
	slices.SortStableFunc(l, func(a, b Diagnostic) int {
		return cmp.Or(
			cmp.Compare(a.File, b.File),
			cmp.Compare(a.Line, b.Line),
			cmp.Compare(a.Column, b.Column),
			cmp.Compare(a.Message, b.Message),
		)
	})
}

// String formats the list one diagnostic per line.
func (l List) String() string {
	var b strings.Builder
	for _, d := range l {
		b.WriteString(d.String())
		b.WriteByte('\n')
	}
	return b.String()
}
