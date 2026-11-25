package office

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// InlineSpan represents a styled inline text fragment inside a Markdown block.
type InlineSpan struct {
	Text   string
	Bold   bool
	Italic bool
	Strike bool
	Code   bool
	Href   string
}

// MDBlock represents a parsed block-level Markdown element.
type MDBlock struct {
	Kind         string // heading, paragraph, list_item, blockquote, code, mermaid, table, hr
	Level        int    // heading level 1..6 or list nesting 0..4
	Ordered      bool
	Language     string
	Code         string
	DiagramIndex int
	Spans        []InlineSpan
	Headers      []string
	Alignments   []string
	Rows         [][]string
}

// TableSheet represents a table extracted from Markdown for spreadsheet export.
type TableSheet struct {
	Name    string
	Columns []string
	Rows    [][]any
}

var (
	orderedListRe = regexp.MustCompile(`^(\s*)(\d+)[.)]\s+(.*)$`)
	bulletListRe  = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	headingRe     = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	hrRe          = regexp.MustCompile(`^\s*([-*_])(\s*[-*_]){2,}\s*$`)
	tableSepRe    = regexp.MustCompile(`^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$`)
)

// SanitizeXMLText strips characters illegal in XML 1.0 documents.
func SanitizeXMLText(s string) string {
	if s == "" {
		return ""
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == 0x09 || r == 0x0A || r == 0x0D ||
			(r >= 0x20 && r <= 0xD7FF) ||
			(r >= 0xE000 && r <= 0xFFFD) ||
			(r >= 0x10000 && r <= 0x10FFFF) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ParseMarkdownBlocks converts GFM Markdown into structured blocks suitable for
// Word/Excel export.
func ParseMarkdownBlocks(md string) []MDBlock {
	normalized := strings.ReplaceAll(md, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")

	blocks := make([]MDBlock, 0, 32)
	diagramIdx := 0
	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			i++
			continue
		}

		// Fenced code block (``` or ~~~)
		if fenceChar, fenceLen, lang, ok := parseFenceOpen(trimmed); ok {
			i++
			var codeLines []string
			for i < len(lines) {
				if isFenceClose(strings.TrimSpace(lines[i]), fenceChar, fenceLen) {
					i++
					break
				}
				codeLines = append(codeLines, lines[i])
				i++
			}
			codeBody := strings.Join(codeLines, "\n")
			if strings.EqualFold(lang, "mermaid") {
				blocks = append(blocks, MDBlock{
					Kind:         "mermaid",
					Language:     "mermaid",
					Code:         codeBody,
					DiagramIndex: diagramIdx,
				})
				diagramIdx++
			} else {
				blocks = append(blocks, MDBlock{
					Kind:     "code",
					Language: lang,
					Code:     codeBody,
				})
			}
			continue
		}

		// Heading
		if m := headingRe.FindStringSubmatch(trimmed); m != nil {
			text := strings.TrimSpace(strings.TrimRight(m[2], "#"))
			blocks = append(blocks, MDBlock{
				Kind:  "heading",
				Level: len(m[1]),
				Spans: ParseInlineSpans(text),
			})
			i++
			continue
		}

		// Horizontal rule
		if hrRe.MatchString(line) && !strings.Contains(trimmed, "|") {
			blocks = append(blocks, MDBlock{Kind: "hr"})
			i++
			continue
		}

		// GFM table
		if i+1 < len(lines) && strings.Contains(line, "|") && tableSepRe.MatchString(lines[i+1]) {
			headers := splitTableRow(line)
			aligns := parseTableAlignments(lines[i+1], len(headers))
			i += 2
			rows := make([][]string, 0)
			for i < len(lines) {
				rowLine := strings.TrimSpace(lines[i])
				if rowLine == "" || !strings.Contains(rowLine, "|") {
					break
				}
				if tableSepRe.MatchString(rowLine) {
					i++
					continue
				}
				cells := splitTableRow(lines[i])
				for len(cells) < len(headers) {
					cells = append(cells, "")
				}
				if len(cells) > len(headers) && len(headers) > 0 {
					cells = cells[:len(headers)]
				}
				rows = append(rows, cells)
				i++
			}
			blocks = append(blocks, MDBlock{
				Kind:       "table",
				Headers:    headers,
				Alignments: aligns,
				Rows:       rows,
			})
			continue
		}

		// Blockquote (consecutive > lines)
		if strings.HasPrefix(trimmed, ">") {
			var quoteLines []string
			for i < len(lines) {
				qt := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(qt, ">") {
					break
				}
				content := strings.TrimPrefix(qt, ">")
				content = strings.TrimPrefix(content, " ")
				content = normalizeCalloutTag(content)
				if strings.TrimSpace(content) != "" {
					quoteLines = append(quoteLines, content)
				}
				i++
			}
			if len(quoteLines) > 0 {
				blocks = append(blocks, MDBlock{
					Kind:  "blockquote",
					Spans: ParseInlineSpans(strings.Join(quoteLines, "\n")),
				})
			}
			continue
		}

		// Unordered list item
		if m := bulletListRe.FindStringSubmatch(line); m != nil {
			level := computeIndentLevel(m[1])
			text := normalizeTaskPrefix(strings.TrimSpace(m[2]))
			blocks = append(blocks, MDBlock{
				Kind:    "list_item",
				Level:   level,
				Ordered: false,
				Spans:   ParseInlineSpans(text),
			})
			i++
			continue
		}

		// Ordered list item
		if m := orderedListRe.FindStringSubmatch(line); m != nil {
			level := computeIndentLevel(m[1])
			text := normalizeTaskPrefix(strings.TrimSpace(m[3]))
			blocks = append(blocks, MDBlock{
				Kind:    "list_item",
				Level:   level,
				Ordered: true,
				Spans:   ParseInlineSpans(text),
			})
			i++
			continue
		}

		// Paragraph: accumulate until blank line or next block opener
		var paraLines []string
		for i < len(lines) {
			cur := lines[i]
			curTrim := strings.TrimSpace(cur)
			if curTrim == "" {
				break
			}
			if len(paraLines) > 0 && isBlockStarter(lines, i) {
				break
			}
			paraLines = append(paraLines, curTrim)
			i++
		}
		if len(paraLines) > 0 {
			blocks = append(blocks, MDBlock{
				Kind:  "paragraph",
				Spans: ParseInlineSpans(strings.Join(paraLines, " ")),
			})
		}
	}

	return blocks
}

func isBlockStarter(lines []string, i int) bool {
	line := lines[i]
	trimmed := strings.TrimSpace(line)
	if _, _, _, ok := parseFenceOpen(trimmed); ok {
		return true
	}
	if headingRe.MatchString(trimmed) || hrRe.MatchString(line) {
		return true
	}
	if strings.HasPrefix(trimmed, ">") || bulletListRe.MatchString(line) || orderedListRe.MatchString(line) {
		return true
	}
	if i+1 < len(lines) && strings.Contains(line, "|") && tableSepRe.MatchString(lines[i+1]) {
		return true
	}
	return false
}

func parseFenceOpen(trimmed string) (byte, int, string, bool) {
	if len(trimmed) < 3 {
		return 0, 0, "", false
	}
	ch := trimmed[0]
	if ch != '`' && ch != '~' {
		return 0, 0, "", false
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == ch {
		n++
	}
	if n < 3 {
		return 0, 0, "", false
	}
	rest := strings.TrimSpace(trimmed[n:])
	if ch == '`' && strings.Contains(rest, "`") {
		return 0, 0, "", false
	}
	lang := rest
	if idx := strings.IndexAny(lang, " \t{"); idx >= 0 {
		lang = lang[:idx]
	}
	return ch, n, strings.TrimSpace(lang), true
}

func isFenceClose(trimmed string, ch byte, minLen int) bool {
	if len(trimmed) < minLen {
		return false
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] != ch {
			return false
		}
	}
	return true
}

func computeIndentLevel(indent string) int {
	spaces := 0
	for _, r := range indent {
		if r == '\t' {
			spaces += 4
		} else {
			spaces++
		}
	}
	level := spaces / 2
	if level > 4 {
		level = 4
	}
	return level
}

func normalizeTaskPrefix(s string) string {
	switch {
	case strings.HasPrefix(s, "[ ] "):
		return "☐ " + strings.TrimPrefix(s, "[ ] ")
	case strings.HasPrefix(s, "[x] ") || strings.HasPrefix(s, "[X] "):
		return "☑ " + s[4:]
	default:
		return s
	}
}

func normalizeCalloutTag(s string) string {
	trimmed := strings.TrimSpace(s)
	tags := map[string]string{
		"[!NOTE]":      "【说明】",
		"[!TIP]":       "【提示】",
		"[!IMPORTANT]": "【重要】",
		"[!WARNING]":   "【警告】",
		"[!CAUTION]":   "【注意】",
	}
	for tag, label := range tags {
		if strings.HasPrefix(strings.ToUpper(trimmed), tag) {
			rest := strings.TrimSpace(trimmed[len(tag):])
			if rest == "" {
				return label
			}
			return label + " " + rest
		}
	}
	return s
}

func splitTableRow(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")

	var cells []string
	var cur strings.Builder
	escaped := false
	inCode := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			cur.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' && i+1 < len(s) && s[i+1] == '|' {
			escaped = true
			continue
		}
		if ch == '`' {
			inCode = !inCode
			cur.WriteByte(ch)
			continue
		}
		if ch == '|' && !inCode {
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	return cells
}

func parseTableAlignments(line string, count int) []string {
	parts := splitTableRow(line)
	aligns := make([]string, count)
	for i := 0; i < count; i++ {
		aligns[i] = "left"
		if i >= len(parts) {
			continue
		}
		p := strings.TrimSpace(parts[i])
		left := strings.HasPrefix(p, ":")
		right := strings.HasSuffix(p, ":")
		switch {
		case left && right:
			aligns[i] = "center"
		case right:
			aligns[i] = "right"
		default:
			aligns[i] = "left"
		}
	}
	return aligns
}

// ParseInlineSpans parses common GFM inline formatting into flat spans.
func ParseInlineSpans(text string) []InlineSpan {
	return parseInlineWithFlags(text, false, false, false)
}

func parseInlineWithFlags(text string, bold, italic, strike bool) []InlineSpan {
	if text == "" {
		return nil
	}
	var spans []InlineSpan
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			spans = append(spans, InlineSpan{
				Text:   buf.String(),
				Bold:   bold,
				Italic: italic,
				Strike: strike,
			})
			buf.Reset()
		}
	}

	i := 0
	n := len(text)
	for i < n {
		// Escaped punctuation
		if text[i] == '\\' && i+1 < n && isEscapableMarkdown(text[i+1]) {
			buf.WriteByte(text[i+1])
			i += 2
			continue
		}

		// Inline code `...`
		if text[i] == '`' {
			end := strings.IndexByte(text[i+1:], '`')
			if end >= 0 {
				flush()
				spans = append(spans, InlineSpan{
					Text:   text[i+1 : i+1+end],
					Bold:   bold,
					Italic: italic,
					Strike: strike,
					Code:   true,
				})
				i += end + 2
				continue
			}
		}

		// Markdown link [label](url) or image ![alt](url)
		if text[i] == '[' || (text[i] == '!' && i+1 < n && text[i+1] == '[') {
			isImg := text[i] == '!'
			startBracket := i
			if isImg {
				startBracket++
			}
			if closeBracket := findMatchingBracket(text, startBracket); closeBracket > startBracket &&
				closeBracket+1 < n && text[closeBracket+1] == '(' {
				if closeParen := findMatchingParen(text, closeBracket+1); closeParen > closeBracket+1 {
					label := text[startBracket+1 : closeBracket]
					href := strings.TrimSpace(text[closeBracket+2 : closeParen])
					// Strip optional title inside link target
					if sp := strings.IndexAny(href, " \t"); sp > 0 {
						href = strings.TrimSpace(href[:sp])
					}
					href = strings.Trim(href, "<>")
					flush()
					if isImg {
						if label == "" {
							label = "[图片]"
						}
						spans = append(spans, InlineSpan{Text: label, Italic: true})
					} else {
						linkSpans := parseInlineWithFlags(label, bold, italic, strike)
						for idx := range linkSpans {
							if isExternalURL(href) {
								linkSpans[idx].Href = href
							}
						}
						spans = append(spans, linkSpans...)
					}
					i = closeParen + 1
					continue
				}
			}
		}

		// Bold + Italic ***...***
		if strings.HasPrefix(text[i:], "***") {
			if end := strings.Index(text[i+3:], "***"); end > 0 {
				flush()
				spans = append(spans, parseInlineWithFlags(text[i+3:i+3+end], true, true, strike)...)
				i += end + 6
				continue
			}
		}

		// Bold **...** or __...__
		if strings.HasPrefix(text[i:], "**") || strings.HasPrefix(text[i:], "__") {
			delim := text[i : i+2]
			if end := strings.Index(text[i+2:], delim); end > 0 {
				flush()
				spans = append(spans, parseInlineWithFlags(text[i+2:i+2+end], true, italic, strike)...)
				i += end + 4
				continue
			}
		}

		// Strikethrough ~~...~~
		if strings.HasPrefix(text[i:], "~~") {
			if end := strings.Index(text[i+2:], "~~"); end > 0 {
				flush()
				spans = append(spans, parseInlineWithFlags(text[i+2:i+2+end], bold, italic, true)...)
				i += end + 4
				continue
			}
		}

		// Italic *...* (avoid matching standalone * followed by space)
		if text[i] == '*' && i+1 < n && text[i+1] != ' ' && text[i+1] != '*' {
			if end := strings.IndexByte(text[i+1:], '*'); end > 0 {
				flush()
				spans = append(spans, parseInlineWithFlags(text[i+1:i+1+end], bold, true, strike)...)
				i += end + 2
				continue
			}
		}

		buf.WriteByte(text[i])
		i++
	}
	flush()
	return spans
}

func isEscapableMarkdown(b byte) bool {
	return strings.IndexByte("\\`*_{}[]()#+-.!|~", b) >= 0
}

func findMatchingBracket(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '[' {
			depth++
		} else if s[i] == ']' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func findMatchingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '(' {
			depth++
		} else if s[i] == ')' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func isExternalURL(u string) bool {
	lower := strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:")
}

// PlainTextFromSpans joins all inline spans into plain unformatted text.
func PlainTextFromSpans(spans []InlineSpan) string {
	var b strings.Builder
	for _, sp := range spans {
		b.WriteString(sp.Text)
	}
	return b.String()
}

// ExtractMarkdownTables extracts all GFM tables from Markdown as spreadsheet sheets.
func ExtractMarkdownTables(md string) []TableSheet {
	blocks := ParseMarkdownBlocks(md)
	sheets := make([]TableSheet, 0)
	lastHeading := ""
	tableSeq := 0
	for _, blk := range blocks {
		if blk.Kind == "heading" {
			lastHeading = strings.TrimSpace(PlainTextFromSpans(blk.Spans))
			continue
		}
		if blk.Kind != "table" || len(blk.Headers) == 0 {
			continue
		}
		tableSeq++
		name := lastHeading
		if name == "" {
			name = fmt.Sprintf("表格%d", tableSeq)
		}
		cols := make([]string, len(blk.Headers))
		for idx, h := range blk.Headers {
			cols[idx] = strings.TrimSpace(PlainTextFromSpans(ParseInlineSpans(h)))
		}
		rows := make([][]any, 0, len(blk.Rows))
		for _, r := range blk.Rows {
			out := make([]any, len(cols))
			for ci := 0; ci < len(cols); ci++ {
				raw := ""
				if ci < len(r) {
					raw = strings.TrimSpace(PlainTextFromSpans(ParseInlineSpans(r[ci])))
				}
				out[ci] = parseScalarCellValue(raw)
			}
			rows = append(rows, out)
		}
		sheets = append(sheets, TableSheet{
			Name:    name,
			Columns: cols,
			Rows:    rows,
		})
	}
	return sheets
}

// parseScalarCellValue converts clean finite numbers (excluding leading-zero
// identifiers like "007" or long numeric IDs > 15 digits) into float64.
func parseScalarCellValue(raw string) any {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// Preserve strings with leading zeros (e.g. "007", "0102") except "0" or "0.xxx"
	numPart := s
	if strings.HasPrefix(numPart, "-") || strings.HasPrefix(numPart, "+") {
		numPart = numPart[1:]
	}
	if len(numPart) > 1 && numPart[0] == '0' && numPart[1] != '.' {
		return s
	}
	// Preserve long numeric IDs (>15 digits lose precision in IEEE-754 float64)
	digits := 0
	for _, r := range numPart {
		if unicode.IsDigit(r) {
			digits++
		}
	}
	if digits == 0 || digits > 15 {
		return s
	}
	// Reject strings with commas, spaces, or suffixes unless plain number
	for _, r := range s {
		if !unicode.IsDigit(r) && r != '.' && r != '-' && r != '+' {
			return s
		}
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
		return v
	}
	return s
}
