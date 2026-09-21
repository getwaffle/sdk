package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// emitJSON prints any value as indented JSON for --json mode. Response
// shapes pass through untouched — --json is the machine-readable
// contract, never a re-modeling of it.
func emitJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// kv is one row of human-oriented key/value output.
type kv struct{ k, v string }

func row(k, v string) kv { return kv{k: k, v: v} }

// printKV renders human-friendly aligned output:
//
//	Charge chg_123
//	  Status:    pending
//	  Amount:    Rp100.000
func printKV(w io.Writer, title string, rows []kv) {
	width := 0
	for _, r := range rows {
		if len(r.k) > width {
			width = len(r.k)
		}
	}
	if title != "" {
		fmt.Fprintln(w, title)
	}
	for _, r := range rows {
		if r.v == "" {
			continue
		}
		fmt.Fprintf(w, "  %-*s  %s\n", width, r.k+":", r.v)
	}
}

// printTable renders aligned multi-row tables (keys list, banks list).
func printTable(w io.Writer, header []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	tw.Flush()
}

// money renders an integer amount of the currency's minor unit for
// humans. IDR has no minor-unit subdivision in waffle's model —
// 287500 literally means Rp287.500 — so it renders with dot thousands
// separators and an Rp prefix. Any other currency renders as
// "<amount> <CODE>" (only IDR exists on the platform today).
func money(amount int64, currency string) string {
	if currency != "IDR" {
		return fmt.Sprintf("%d %s", amount, currency)
	}
	neg := amount < 0
	if neg {
		amount = -amount
	}
	digits := fmt.Sprintf("%d", amount)
	var parts []string
	for len(digits) > 3 {
		parts = append([]string{digits[len(digits)-3:]}, parts...)
		digits = digits[:len(digits)-3]
	}
	parts = append([]string{digits}, parts...)
	prefix := "Rp"
	if neg {
		prefix = "-Rp"
	}
	return prefix + strings.Join(parts, ".")
}

// orDash replaces empty strings with a dash in table cells.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
