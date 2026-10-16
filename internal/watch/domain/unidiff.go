package domain

import (
	"fmt"
	"strings"
)

// UnifiedDiff answers a line diff from a to b in unified form, with two
// lines of context around each hunk.
func UnifiedDiff(a, b string) string {
	x, y := splitLines(a), splitLines(b)
	ops := lineOps(x, y)
	const ctx = 2
	var out strings.Builder
	for i := 0; i < len(ops); {
		if ops[i].op == ' ' {
			i++
			continue
		}
		start := max(0, i-ctx)
		end := i
		for j := i; j < len(ops); j++ {
			if ops[j].op != ' ' {
				end = j
			} else if j-end > 2*ctx {
				break
			}
		}
		end = min(len(ops)-1, end+ctx)
		ai, bi, an, bn := ops[start].ai, ops[start].bi, 0, 0
		var body strings.Builder
		for _, o := range ops[start : end+1] {
			body.WriteString(string(o.op) + o.text + "\n")
			if o.op != '+' {
				an++
			}
			if o.op != '-' {
				bn++
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n%s", ai+1, an, bi+1, bn, body.String())
		i = end + 1
	}
	return strings.TrimSuffix(out.String(), "\n")
}

type lineOp struct {
	op     byte
	text   string
	ai, bi int // positions in a and b before this line
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// lineOps is a longest-common-subsequence diff, fine for the size of a
// title or a description.
func lineOps(x, y []string) []lineOp {
	n, m := len(x), len(y)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []lineOp
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && x[i] == y[j]:
			ops = append(ops, lineOp{' ', x[i], i, j})
			i, j = i+1, j+1
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, lineOp{'-', x[i], i, j})
			i++
		default:
			ops = append(ops, lineOp{'+', y[j], i, j})
			j++
		}
	}
	return ops
}
