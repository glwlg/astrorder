package modelgateway

import (
	"regexp"
	"strconv"
	"strings"
)

// diffMaxLines caps the emitted diff the same way the retired Python
// implementation did (first 400 lines of difflib output).
const diffMaxLines = 400

// diffContext is the unified diff context line count (difflib default n=2 in
// the retired Python implementation).
const diffContext = 2

var diffSecretPattern = regexp.MustCompile(`(?mi)^(\s*[+-]?\s*api_key\s*[=:]\s*).+$`)

type diffOp struct {
	kind byte // ' ', '-', '+'
	line string
}

// unifiedDiffText renders a unified diff between the current and expected
// content of a managed configuration file. An empty string means no visible
// difference. Output mirrors difflib.unified_diff with lineterm "" (headers
// included, first 400 lines kept, api_key values redacted).
func unifiedDiffText(before, after, name string) string {
	if before == after {
		return ""
	}
	ops := diffOps(splitDiffLines(before), splitDiffLines(after))
	lines := []string{"--- 当前/" + name, "+++ 同步后/" + name}
	lines = append(lines, formatDiffHunks(ops, diffContext)...)
	if len(lines) > diffMaxLines {
		lines = lines[:diffMaxLines]
	}
	text := strings.Join(lines, "\n")
	return diffSecretPattern.ReplaceAllString(text, `${1}"[已配置]"`)
}

func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	// Match Python str.splitlines semantics for content ending in a newline.
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// diffOps computes a line-level LCS diff between a and b.
func diffOps(a, b []string) []diffOp {
	const maxCells = 4_000_000
	if len(a)*len(b) > maxCells {
		return nil
	}
	n, m := len(a), len(b)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// formatDiffHunks groups change ops into unified hunks with the given context
// size, mirroring difflib.get_grouped_opcodes.
func formatDiffHunks(ops []diffOp, context int) []string {
	var changed []int
	for idx, op := range ops {
		if op.kind != ' ' {
			changed = append(changed, idx)
		}
	}
	if len(changed) == 0 {
		return nil
	}

	// 1-based line numbers of each op position in the before/after files.
	posA := make([]int, len(ops)+1)
	posB := make([]int, len(ops)+1)
	ca, cb := 1, 1
	for idx, op := range ops {
		posA[idx], posB[idx] = ca, cb
		switch op.kind {
		case ' ':
			ca++
			cb++
		case '-':
			ca++
		case '+':
			cb++
		}
	}
	posA[len(ops)], posB[len(ops)] = ca, cb

	clamp := func(v, lo, hi int) int {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}

	var out []string
	flush := func(s, e int) {
		countA, countB := 0, 0
		for _, op := range ops[s:e] {
			if op.kind != '+' {
				countA++
			}
			if op.kind != '-' {
				countB++
			}
		}
		out = append(out, "@@ -"+formatDiffRange(posA[s], countA)+" +"+formatDiffRange(posB[s], countB)+" @@")
		for _, op := range ops[s:e] {
			out = append(out, string(op.kind)+op.line)
		}
	}

	start := clamp(changed[0]-context, 0, len(ops))
	end := clamp(changed[0]+context+1, 0, len(ops))
	for _, idx := range changed[1:] {
		if idx-context <= end {
			end = clamp(idx+context+1, 0, len(ops))
			continue
		}
		flush(start, end)
		start = clamp(idx-context, 0, len(ops))
		end = clamp(idx+context+1, 0, len(ops))
	}
	flush(start, end)
	return out
}

func formatDiffRange(start, length int) string {
	if length == 1 {
		return strconv.Itoa(start)
	}
	if length == 0 {
		start--
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(length)
}
