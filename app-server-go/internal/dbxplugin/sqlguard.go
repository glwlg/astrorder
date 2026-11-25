package dbxplugin

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	RiskReadOnly    = "read_only"
	RiskWrite       = "write"
	RiskDestructive = "destructive"
)

// SQLClassification summarizes the static safety analysis of an SQL payload.
type SQLClassification struct {
	RiskLevel       string   `json:"risk_level"`
	StatementCount  int      `json:"statement_count"`
	PrimaryVerb     string   `json:"primary_verb"`
	Warnings        []string `json:"warnings,omitempty"`
	ReadOnlyAllowed bool     `json:"read_only_allowed"`
}

type sqlToken struct {
	text  string
	upper string
	kind  string // "word", "semi", "lparen", "rparen", "other"
}

// ClassifySQL performs a lexical scan over SQL text, ignoring comments and
// quoted literals (including PostgreSQL dollar-quoted strings), splits top-level
// statements on semicolons, and classifies the overall risk.
//
// Policy (fail-closed):
//   - Only a single, unambiguously read-only query returns RiskReadOnly with ReadOnlyAllowed=true.
//   - Multi-statement scripts, DML/DDL, locking SELECTs, EXPLAIN ANALYZE, CTEs
//     containing write verbs, and unrecognized commands require confirmation.
func ClassifySQL(sql string) SQLClassification {
	stmts := splitSQLStatements(sql)
	if len(stmts) == 0 {
		return SQLClassification{
			RiskLevel:       RiskWrite,
			StatementCount:  0,
			PrimaryVerb:     "",
			Warnings:        []string{"SQL 内容为空"},
			ReadOnlyAllowed: false,
		}
	}

	overallRisk := RiskReadOnly
	primaryVerb := ""
	var warnings []string
	seenWarnings := make(map[string]bool)
	addWarning := func(msg string) {
		if msg == "" || seenWarnings[msg] {
			return
		}
		seenWarnings[msg] = true
		warnings = append(warnings, msg)
	}

	promote := func(next string) {
		if next == RiskDestructive {
			overallRisk = RiskDestructive
		} else if next == RiskWrite && overallRisk == RiskReadOnly {
			overallRisk = RiskWrite
		}
	}

	if len(stmts) > 1 {
		addWarning(fmt.Sprintf("多语句脚本（共 %d 条语句）", len(stmts)))
	}

	for idx, toks := range stmts {
		verb, risk, stmtWarnings := classifySingleStatement(toks)
		if idx == 0 {
			primaryVerb = verb
		}
		promote(risk)
		for _, w := range stmtWarnings {
			addWarning(w)
		}
	}

	readOnlyAllowed := overallRisk == RiskReadOnly
	return SQLClassification{
		RiskLevel:       overallRisk,
		StatementCount:  len(stmts),
		PrimaryVerb:     primaryVerb,
		Warnings:        warnings,
		ReadOnlyAllowed: readOnlyAllowed,
	}
}

func classifySingleStatement(toks []sqlToken) (string, string, []string) {
	firstWordIdx := -1
	for i, tok := range toks {
		if tok.kind == "word" {
			firstWordIdx = i
			break
		}
	}
	if firstWordIdx < 0 {
		return "", RiskWrite, []string{"未识别出有效 SQL 关键字"}
	}

	verb := toks[firstWordIdx].upper
	var warnings []string

	switch verb {
	case "DROP", "TRUNCATE", "ALTER", "CREATE", "GRANT", "REVOKE", "RENAME":
		warnings = append(warnings, fmt.Sprintf("包含 DDL 或权限变更语句（%s）", verb))
		return verb, RiskDestructive, warnings

	case "BEGIN", "START", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "XA":
		warnings = append(warnings, fmt.Sprintf("包含事务控制语句（%s）", verb))
		return verb, RiskWrite, warnings

	case "DELETE", "UPDATE":
		if !hasTopLevelWord(toks[firstWordIdx+1:], "WHERE") {
			warnings = append(warnings, fmt.Sprintf("检测到缺少 WHERE 条件的 %s 语句", verb))
			return verb, RiskDestructive, warnings
		}
		warnings = append(warnings, fmt.Sprintf("包含数据写入语句（%s）", verb))
		return verb, RiskWrite, warnings

	case "INSERT", "MERGE", "REPLACE", "UPSERT", "COPY", "LOAD", "CALL", "EXEC", "EXECUTE", "DO", "SET", "RESET", "LOCK", "UNLOCK", "VACUUM", "ANALYZE", "ANALYSE", "REINDEX", "CLUSTER", "REFRESH", "PREPARE", "DEALLOCATE", "COMMENT", "ATTACH", "DETACH":
		warnings = append(warnings, fmt.Sprintf("包含写入或会话变更语句（%s）", verb))
		return verb, RiskWrite, warnings

	case "SELECT", "SHOW", "DESCRIBE", "DESC", "VALUES", "TABLE":
		if verb == "SELECT" {
			if hasTopLevelWord(toks[firstWordIdx+1:], "INTO") {
				warnings = append(warnings, "SELECT ... INTO 会创建或写入目标表")
				return verb, RiskWrite, warnings
			}
			if hasRowLockClause(toks[firstWordIdx+1:]) {
				warnings = append(warnings, "SELECT 包含行锁子句（FOR UPDATE / FOR SHARE）")
				return verb, RiskWrite, warnings
			}
		}
		return verb, RiskReadOnly, nil

	case "EXPLAIN":
		rest := toks[firstWordIdx+1:]
		if containsWord(rest, "ANALYZE") || containsWord(rest, "ANALYSE") {
			warnings = append(warnings, "EXPLAIN ANALYZE 会实际执行内部语句")
			if containsAnyWord(rest, "DROP", "TRUNCATE", "ALTER", "CREATE", "GRANT", "REVOKE") {
				return verb, RiskDestructive, warnings
			}
			return verb, RiskWrite, warnings
		}
		if containsAnyWord(rest, "INSERT", "UPDATE", "DELETE", "MERGE", "DROP", "TRUNCATE", "ALTER", "CREATE") {
			warnings = append(warnings, "EXPLAIN 目标包含写入或 DDL 语句")
			return verb, RiskWrite, warnings
		}
		return verb, RiskReadOnly, nil

	case "WITH":
		rest := toks[firstWordIdx+1:]
		if containsAnyWord(rest, "DROP", "TRUNCATE", "ALTER", "CREATE", "GRANT", "REVOKE") {
			warnings = append(warnings, "WITH 语句包含高危 DDL/权限变更")
			return verb, RiskDestructive, warnings
		}
		if containsAnyWord(rest, "INSERT", "UPDATE", "DELETE", "MERGE", "REPLACE", "INTO") {
			warnings = append(warnings, "WITH 语句包含数据写入子句")
			return verb, RiskWrite, warnings
		}
		if hasRowLockClause(rest) {
			warnings = append(warnings, "WITH 查询包含行锁子句（FOR UPDATE / FOR SHARE）")
			return verb, RiskWrite, warnings
		}
		if containsAnyWord(rest, "SELECT", "VALUES", "TABLE") {
			return verb, RiskReadOnly, nil
		}
		warnings = append(warnings, "无法确认 WITH 语句为纯只读查询")
		return verb, RiskWrite, warnings

	default:
		warnings = append(warnings, fmt.Sprintf("未归类为只读的语句（%s）", verb))
		return verb, RiskWrite, warnings
	}
}

func containsWord(toks []sqlToken, target string) bool {
	for _, tok := range toks {
		if tok.kind == "word" && tok.upper == target {
			return true
		}
	}
	return false
}

func containsAnyWord(toks []sqlToken, targets ...string) bool {
	for _, tok := range toks {
		if tok.kind != "word" {
			continue
		}
		for _, target := range targets {
			if tok.upper == target {
				return true
			}
		}
	}
	return false
}

func hasTopLevelWord(toks []sqlToken, target string) bool {
	depth := 0
	for _, tok := range toks {
		switch tok.kind {
		case "lparen":
			depth++
		case "rparen":
			if depth > 0 {
				depth--
			}
		case "word":
			if depth == 0 && tok.upper == target {
				return true
			}
		}
	}
	return false
}

func hasRowLockClause(toks []sqlToken) bool {
	for i := 0; i < len(toks); i++ {
		if toks[i].kind != "word" || toks[i].upper != "FOR" {
			continue
		}
		for j := i + 1; j < len(toks) && j <= i+4; j++ {
			if toks[j].kind != "word" {
				break
			}
			switch toks[j].upper {
			case "UPDATE", "SHARE":
				return true
			case "NO", "KEY":
				continue
			default:
				j = len(toks)
			}
		}
	}
	return false
}

// splitSQLStatements lexes SQL into non-empty statements split on top-level
// semicolons, properly skipping comments and quoted strings.
func splitSQLStatements(sql string) [][]sqlToken {
	tokens := tokenizeSQL(sql)
	var stmts [][]sqlToken
	var cur []sqlToken
	depth := 0
	for _, tok := range tokens {
		if tok.kind == "lparen" {
			depth++
		} else if tok.kind == "rparen" && depth > 0 {
			depth--
		}
		if tok.kind == "semi" && depth == 0 {
			if len(cur) > 0 {
				stmts = append(stmts, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, tok)
	}
	if len(cur) > 0 {
		stmts = append(stmts, cur)
	}
	return stmts
}

func tokenizeSQL(input string) []sqlToken {
	var tokens []sqlToken
	n := len(input)
	i := 0
	for i < n {
		ch := input[i]
		// Skip whitespace
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == '\f' {
			i++
			continue
		}
		// Line comment: -- ...
		if ch == '-' && i+1 < n && input[i+1] == '-' {
			i += 2
			for i < n && input[i] != '\n' {
				i++
			}
			continue
		}
		// Block comment: /* ... */ (supports nesting for PostgreSQL)
		if ch == '/' && i+1 < n && input[i+1] == '*' {
			depth := 1
			i += 2
			for i < n && depth > 0 {
				if input[i] == '/' && i+1 < n && input[i+1] == '*' {
					depth++
					i += 2
					continue
				}
				if input[i] == '*' && i+1 < n && input[i+1] == '/' {
					depth--
					i += 2
					continue
				}
				i++
			}
			continue
		}
		// Single-quoted string literal: '...'
		if ch == '\'' {
			i++
			for i < n {
				if input[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				if input[i] == '\'' {
					i++
					if i < n && input[i] == '\'' {
						i++
						continue
					}
					break
				}
				i++
			}
			tokens = append(tokens, sqlToken{kind: "other"})
			continue
		}
		// Double-quoted identifier: "..."
		if ch == '"' {
			i++
			for i < n {
				if input[i] == '"' {
					i++
					if i < n && input[i] == '"' {
						i++
						continue
					}
					break
				}
				i++
			}
			tokens = append(tokens, sqlToken{kind: "other"})
			continue
		}
		// Backtick identifier: `...`
		if ch == '`' {
			i++
			for i < n {
				if input[i] == '`' {
					i++
					if i < n && input[i] == '`' {
						i++
						continue
					}
					break
				}
				i++
			}
			tokens = append(tokens, sqlToken{kind: "other"})
			continue
		}
		// PostgreSQL dollar-quoted string: $tag$...$tag$
		if ch == '$' {
			if tag, tagLen, ok := matchDollarTag(input[i:]); ok {
				i += tagLen
				if endIdx := strings.Index(input[i:], tag); endIdx >= 0 {
					i += endIdx + len(tag)
				} else {
					i = n
				}
				tokens = append(tokens, sqlToken{kind: "other"})
				continue
			}
		}
		// Semicolon / parens
		if ch == ';' {
			tokens = append(tokens, sqlToken{text: ";", kind: "semi"})
			i++
			continue
		}
		if ch == '(' {
			tokens = append(tokens, sqlToken{text: "(", kind: "lparen"})
			i++
			continue
		}
		if ch == ')' {
			tokens = append(tokens, sqlToken{text: ")", kind: "rparen"})
			i++
			continue
		}
		// Word / keyword
		if isWordStart(ch) {
			start := i
			i++
			for i < n && isWordContinue(input[i]) {
				i++
			}
			word := input[start:i]
			tokens = append(tokens, sqlToken{
				text:  word,
				upper: strings.ToUpper(word),
				kind:  "word",
			})
			continue
		}
		// Non-ASCII / punctuation
		if ch >= 0x80 {
			r, size := firstRune(input[i:])
			if unicode.IsLetter(r) {
				i += size
				tokens = append(tokens, sqlToken{kind: "other"})
				continue
			}
		}
		i++
	}
	return tokens
}

func matchDollarTag(s string) (string, int, bool) {
	if len(s) < 2 || s[0] != '$' {
		return "", 0, false
	}
	if s[1] == '$' {
		return "$$", 2, true
	}
	if !isWordStart(s[1]) {
		return "", 0, false
	}
	for i := 2; i < len(s); i++ {
		if s[i] == '$' {
			return s[:i+1], i + 1, true
		}
		if !isWordContinue(s[i]) {
			return "", 0, false
		}
	}
	return "", 0, false
}

func isWordStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isWordContinue(ch byte) bool {
	return isWordStart(ch) || (ch >= '0' && ch <= '9')
}

func firstRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 1
}
