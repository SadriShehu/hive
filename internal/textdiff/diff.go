// Package textdiff writes a small line-based unified diff: enough for an
// install plan to show what it would change, with no dependency.
package textdiff

import (
	"fmt"
	"strconv"
	"strings"
)

type opKind int

const (
	equal opKind = iota
	del
	add
)

type op struct {
	kind opKind
	line string
}

// Unified returns a unified diff of old and new under the given names, with
// context lines around each change. It returns "" when they are equal.
func Unified(oldName, newName string, old, new []byte, context int) string {
	if string(old) == string(new) {
		return ""
	}
	oldLines := splitLines(old)
	newLines := splitLines(new)
	ops := diffLines(oldLines, newLines)
	changes := make([]int, 0)
	for i, o := range ops {
		if o.kind != equal {
			changes = append(changes, i)
		}
	}
	if len(changes) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", oldName, newName)
	oldNo := make([]int, len(ops)+1)
	newNo := make([]int, len(ops)+1)
	oldNo[0], newNo[0] = 1, 1
	for i, o := range ops {
		oldNo[i+1], newNo[i+1] = oldNo[i], newNo[i]
		switch o.kind {
		case equal:
			oldNo[i+1]++
			newNo[i+1]++
		case del:
			oldNo[i+1]++
		case add:
			newNo[i+1]++
		}
	}

	start := changes[0]
	prev := changes[0]
	for _, i := range changes[1:] {
		if i-prev <= 2*context+1 {
			prev = i
			continue
		}
		writeHunk(&b, ops, oldNo, newNo, max(0, start-context), min(len(ops)-1, prev+context))
		start, prev = i, i
	}
	writeHunk(&b, ops, oldNo, newNo, max(0, start-context), min(len(ops)-1, prev+context))
	return b.String()
}

// writeHunk writes ops[from..to] with its header.
func writeHunk(b *strings.Builder, ops []op, oldNo, newNo []int, from, to int) {
	oldCount, newCount := 0, 0
	for _, o := range ops[from : to+1] {
		switch o.kind {
		case equal:
			oldCount++
			newCount++
		case del:
			oldCount++
		case add:
			newCount++
		}
	}
	oldStart, newStart := oldNo[from], newNo[from]
	if oldCount == 0 {
		oldStart--
	}
	if newCount == 0 {
		newStart--
	}
	fmt.Fprintf(b, "@@ -%s +%s @@\n", hunkRange(oldStart, oldCount), hunkRange(newStart, newCount))
	for _, o := range ops[from : to+1] {
		prefix := " "
		switch o.kind {
		case del:
			prefix = "-"
		case add:
			prefix = "+"
		}
		if strings.HasSuffix(o.line, "\n") {
			b.WriteString(prefix + o.line)
		} else {
			b.WriteString(prefix + o.line + "\n\\ No newline at end of file\n")
		}
	}
}

// hunkRange words one side of a hunk header the way git does.
func hunkRange(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// splitLines keeps each line's newline; a last line without one stays as it is.
func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// diffLines returns the edit script for two line lists from their longest
// common subsequence.
func diffLines(old, new []string) []op {
	dp := make([][]int, len(old)+1)
	for i := range dp {
		dp[i] = make([]int, len(new)+1)
	}
	for i := len(old) - 1; i >= 0; i-- {
		for j := len(new) - 1; j >= 0; j-- {
			if old[i] == new[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	var ops []op
	for i, j := 0, 0; i < len(old) || j < len(new); {
		switch {
		case i < len(old) && j < len(new) && old[i] == new[j]:
			ops = append(ops, op{equal, old[i]})
			i++
			j++
		case j < len(new) && (i == len(old) || dp[i][j+1] > dp[i+1][j]):
			ops = append(ops, op{add, new[j]})
			j++
		default:
			ops = append(ops, op{del, old[i]})
			i++
		}
	}
	return ops
}
