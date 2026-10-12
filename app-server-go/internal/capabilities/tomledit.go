package capabilities

import (
	"bytes"
	"fmt"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// tomlDoc edits a TOML file line by line. Agents own these files (Codex keeps
// hook trust state and project trust in config.toml), so every edit touches
// only the table it targets and leaves all other bytes, comments and key order
// as they were.
type tomlDoc struct {
	lines []string
	eol   string
	bom   bool
}

func newTOMLDoc(data []byte) *tomlDoc {
	text := strings.TrimPrefix(string(data), "\ufeff")
	bom := len(text) != len(data)
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	text = strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	doc := &tomlDoc{eol: eol, bom: bom}
	if text != "" {
		doc.lines = strings.Split(text, "\n")
	}
	return doc
}

func (d *tomlDoc) bytes() []byte {
	if len(d.lines) == 0 {
		return nil
	}
	out := strings.Join(d.lines, d.eol) + d.eol
	if d.bom {
		out = "\ufeff" + out
	}
	return []byte(out)
}

type tomlSection struct {
	start, end int // start is the header line (-1 for the root); end is exclusive
	path       []string
	array      bool
}

// trackMulti follows multi-line strings so a "[x]" inside one is not a header.
func trackMulti(open, line string) string {
	if open != "" {
		if strings.Contains(line, open) {
			return ""
		}
		return open
	}
	if strings.HasPrefix(strings.TrimSpace(line), "#") {
		return ""
	}
	for _, delim := range []string{`"""`, `'''`} {
		if strings.Count(line, delim)%2 == 1 {
			return delim
		}
	}
	return ""
}

func (d *tomlDoc) sections() []tomlSection {
	var out []tomlSection
	current := tomlSection{start: -1}
	open := ""
	for index, line := range d.lines {
		if open == "" {
			if path, array, ok := parseHeader(line); ok {
				current.end = index
				out = append(out, current)
				current = tomlSection{start: index, path: path, array: array}
				continue
			}
		}
		open = trackMulti(open, line)
	}
	current.end = len(d.lines)
	return append(out, current)
}

// parseKeyPart reads one bare or quoted key starting at text[i].
func parseKeyPart(text string, i int) (string, int, bool) {
	if i >= len(text) {
		return "", i, false
	}
	switch text[i] {
	case '"':
		j := i + 1
		for j < len(text) && text[j] != '"' {
			if text[j] == '\\' {
				j++
			}
			j++
		}
		if j >= len(text) {
			return "", i, false
		}
		value, err := decodeString(text[i : j+1])
		if err != nil {
			return "", i, false
		}
		return value, j + 1, true
	case '\'':
		j := strings.IndexByte(text[i+1:], '\'')
		if j < 0 {
			return "", i, false
		}
		return text[i+1 : i+1+j], i + j + 2, true
	}
	j := i
	for j < len(text) && (text[j] == '-' || text[j] == '_' || text[j] >= '0' && text[j] <= '9' ||
		text[j] >= 'a' && text[j] <= 'z' || text[j] >= 'A' && text[j] <= 'Z') {
		j++
	}
	if j == i {
		return "", i, false
	}
	return text[i:j], j, true
}

func decodeString(quoted string) (string, error) {
	var decoded struct {
		V string `toml:"v"`
	}
	err := toml.Unmarshal([]byte("v = "+quoted), &decoded)
	return decoded.V, err
}

func skipBlank(text string, i int) int {
	for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
		i++
	}
	return i
}

func parseHeader(line string) ([]string, bool, bool) {
	text := strings.TrimSpace(line)
	if !strings.HasPrefix(text, "[") {
		return nil, false, false
	}
	array := strings.HasPrefix(text, "[[")
	i := 1
	if array {
		i = 2
	}
	var parts []string
	for {
		i = skipBlank(text, i)
		part, next, ok := parseKeyPart(text, i)
		if !ok {
			return nil, false, false
		}
		parts = append(parts, part)
		i = skipBlank(text, next)
		if i < len(text) && text[i] == '.' {
			i++
			continue
		}
		break
	}
	closing := "]"
	if array {
		closing = "]]"
	}
	if !strings.HasPrefix(text[i:], closing) {
		return nil, false, false
	}
	return parts, array, true
}

// parseKeyLine recognises `key = value` and returns the column of the value.
func parseKeyLine(line string) (string, int, bool) {
	i := skipBlank(line, 0)
	if i >= len(line) || line[i] == '#' || line[i] == '[' {
		return "", 0, false
	}
	key, next, ok := parseKeyPart(line, i)
	if !ok {
		return "", 0, false
	}
	next = skipBlank(line, next)
	if next >= len(line) || line[next] != '=' {
		return "", 0, false
	}
	return key, next + 1, true
}

// valueExtent returns the exclusive end line of the value that starts on line
// `first` at column `col`, following arrays, inline tables and multi-line
// strings across lines.
func valueExtent(lines []string, first, col int) int {
	depth := 0
	for n := first; n < len(lines); n++ {
		line := lines[n]
		j := 0
		if n == first {
			j = col
		}
		for j < len(line) {
			switch c := line[j]; {
			case c == '#':
				j = len(line)
			case strings.HasPrefix(line[j:], `"""`) || strings.HasPrefix(line[j:], `'''`):
				delim := line[j : j+3]
				if k := strings.Index(line[j+3:], delim); k >= 0 {
					j += 3 + k + 3
					continue
				}
				for m := n + 1; m < len(lines); m++ {
					if strings.Contains(lines[m], delim) {
						return m + 1
					}
				}
				return len(lines)
			case c == '"':
				j++
				for j < len(line) && line[j] != '"' {
					if line[j] == '\\' {
						j++
					}
					j++
				}
				j++
			case c == '\'':
				if k := strings.IndexByte(line[j+1:], '\''); k < 0 {
					j = len(line)
				} else {
					j += k + 2
				}
			case c == '[' || c == '{':
				depth++
				j++
			case c == ']' || c == '}':
				depth--
				j++
			default:
				j++
			}
		}
		if depth <= 0 {
			return n + 1
		}
	}
	return len(lines)
}

func samePath(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasPrefix(path, prefix []string) bool {
	return len(path) >= len(prefix) && samePath(path[:len(prefix)], prefix)
}

func (d *tomlDoc) find(path []string) (tomlSection, bool) {
	for _, section := range d.sections() {
		if !section.array && samePath(section.path, path) {
			return section, true
		}
	}
	return tomlSection{}, false
}

func (d *tomlDoc) splice(from, to int, replacement []string) {
	next := make([]string, 0, len(d.lines)-(to-from)+len(replacement))
	next = append(next, d.lines[:from]...)
	next = append(next, replacement...)
	d.lines = append(next, d.lines[to:]...)
}

func blankOrComment(line string) bool {
	text := strings.TrimSpace(line)
	return text == "" || strings.HasPrefix(text, "#")
}

// tail is the line after the last content line of the section; blank and
// comment lines at the end belong to whatever follows.
func (d *tomlDoc) tail(section tomlSection) int {
	at := section.end
	for at > section.start+1 && blankOrComment(d.lines[at-1]) {
		at--
	}
	if at <= section.start {
		at = section.start + 1
	}
	return at
}

func encodeKey(key string) string {
	bare := key != ""
	for _, c := range key {
		if !(c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			bare = false
		}
	}
	if bare {
		return key
	}
	return encodeValue(key)
}

func encodeValue(value any) string {
	var buffer bytes.Buffer
	encoder := toml.NewEncoder(&buffer)
	encoder.SetTablesInline(true)
	if err := encoder.Encode(map[string]any{"v": value}); err != nil {
		return `""`
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(buffer.String()), "v ="))
}

func headerLine(path []string, array bool) string {
	parts := make([]string, len(path))
	for i, part := range path {
		parts[i] = encodeKey(part)
	}
	joined := strings.Join(parts, ".")
	if array {
		return "[[" + joined + "]]"
	}
	return "[" + joined + "]"
}

// addBlock inserts header and body after the block that holds the parent
// table, or at the end of the file when there is none.
func (d *tomlDoc) addBlock(path []string, array bool, body []string) {
	at := len(d.lines)
	sections := d.sections()
	for i := len(sections) - 1; i >= 0; i-- {
		section := sections[i]
		if section.start >= 0 && len(path) > 1 && hasPrefix(section.path, path[:len(path)-1]) || section.start >= 0 && array && samePath(section.path, path) {
			at = d.tail(section)
			break
		}
	}
	block := []string{headerLine(path, array)}
	block = append(block, body...)
	if at > 0 && strings.TrimSpace(d.lines[at-1]) != "" {
		block = append([]string{""}, block...)
	}
	d.splice(at, at, block)
}

func (d *tomlDoc) ensureTable(path []string) tomlSection {
	if len(path) == 0 {
		return d.sections()[0]
	}
	if section, ok := d.find(path); ok {
		return section
	}
	d.addBlock(path, false, nil)
	section, _ := d.find(path)
	return section
}

// setKey writes `key = raw` into the table, replacing a previous definition.
func (d *tomlDoc) setKey(path []string, key, raw string) {
	section := d.ensureTable(path)
	line := encodeKey(key) + " = " + raw
	for i := section.start + 1; i < section.end; i++ {
		if name, col, ok := parseKeyLine(d.lines[i]); ok && name == key {
			d.splice(i, valueExtent(d.lines, i, col), []string{line})
			return
		}
	}
	at := d.tail(section)
	if len(path) == 0 {
		at = section.end
		for at > 0 && blankOrComment(d.lines[at-1]) {
			at--
		}
	}
	d.splice(at, at, []string{line})
}

func (d *tomlDoc) removeKey(path []string, key string) {
	section, ok := d.find(path)
	if !ok {
		return
	}
	for i := section.start + 1; i < section.end; i++ {
		if name, col, ok := parseKeyLine(d.lines[i]); ok && name == key {
			d.splice(i, valueExtent(d.lines, i, col), nil)
			return
		}
	}
}

func (d *tomlDoc) hasKey(path []string, key string) bool {
	section, ok := d.find(path)
	if !ok {
		return false
	}
	for i := section.start + 1; i < section.end; i++ {
		if name, _, ok := parseKeyLine(d.lines[i]); ok && name == key {
			return true
		}
	}
	return false
}

// deleteTable removes the table, its sub-tables and its array items. Sections
// are re-read after every removal because dropping the blank line in front of
// one moves the end of the section before it.
func (d *tomlDoc) deleteTable(path []string) {
	for {
		sections := d.sections()
		last := -1
		for i := len(sections) - 1; i >= 0; i-- {
			if sections[i].start >= 0 && hasPrefix(sections[i].path, path) {
				last = i
				break
			}
		}
		if last < 0 {
			return
		}
		section := sections[last]
		leading := section.start > 0 && strings.TrimSpace(d.lines[section.start-1]) == ""
		trailing := section.end < len(d.lines) && section.end-1 > section.start && strings.TrimSpace(d.lines[section.end-1]) == ""
		switch {
		case leading && trailing:
			// keep the blank line that separated the section from its successor
			d.splice(section.start-1, section.end-1, nil)
		case leading && section.end >= len(d.lines):
			d.splice(section.start-1, section.end, nil)
		default:
			d.splice(section.start, section.end, nil)
		}
	}
}

// replaceMapTable rewrites a flat string table such as `env` or `headers`.
func (d *tomlDoc) replaceMapTable(path []string, values map[string]string, order []string) {
	d.deleteTable(path)
	if len(values) == 0 {
		return
	}
	body := make([]string, 0, len(order))
	for _, key := range order {
		body = append(body, encodeKey(key)+" = "+encodeValue(values[key]))
	}
	d.addBlock(path, false, body)
}

func (d *tomlDoc) arrayItems(path []string) []tomlSection {
	var out []tomlSection
	for _, section := range d.sections() {
		if section.array && samePath(section.path, path) {
			out = append(out, section)
		}
	}
	return out
}

func (d *tomlDoc) itemString(section tomlSection, key string) (string, bool) {
	for i := section.start + 1; i < section.end; i++ {
		if name, col, ok := parseKeyLine(d.lines[i]); ok && name == key {
			var decoded struct {
				V string `toml:"v"`
			}
			end := valueExtent(d.lines, i, col)
			text := strings.Join(d.lines[i:end], "\n")
			if toml.Unmarshal([]byte("v ="+text[strings.IndexByte(text, '=')+1:]), &decoded) != nil {
				return "", false
			}
			return decoded.V, true
		}
	}
	return "", false
}

func (d *tomlDoc) deleteArrayItem(path []string, key, value string) bool {
	items := d.arrayItems(path)
	for i := len(items) - 1; i >= 0; i-- {
		if got, ok := d.itemString(items[i], key); ok && got == value {
			from := items[i].start
			if from > 0 && strings.TrimSpace(d.lines[from-1]) == "" && (items[i].end >= len(d.lines) || strings.TrimSpace(d.lines[items[i].end-1]) == "") {
				from--
			}
			d.splice(from, items[i].end, nil)
			return true
		}
	}
	return false
}

func (d *tomlDoc) setItemString(path []string, matchKey, matchValue, key, value string) bool {
	for _, item := range d.arrayItems(path) {
		if got, ok := d.itemString(item, matchKey); !ok || got != matchValue {
			continue
		}
		line := encodeKey(key) + " = " + encodeValue(value)
		for i := item.start + 1; i < item.end; i++ {
			if name, col, ok := parseKeyLine(d.lines[i]); ok && name == key {
				d.splice(i, valueExtent(d.lines, i, col), []string{line})
				return true
			}
		}
		at := d.tail(item)
		d.splice(at, at, []string{line})
		return true
	}
	return false
}

func (d *tomlDoc) appendArrayItem(path []string, pairs [][2]string) {
	body := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		body = append(body, encodeKey(pair[0])+" = "+encodeValue(pair[1]))
	}
	d.addBlock(path, true, body)
}

// parseTOML reads a whole document into a map for the read side.
func parseTOML(data []byte) (map[string]any, error) {
	out := map[string]any{}
	if strings.TrimSpace(string(data)) == "" {
		return out, nil
	}
	if err := toml.Unmarshal([]byte(strings.TrimPrefix(string(data), "\ufeff")), &out); err != nil {
		return nil, fmt.Errorf("invalid TOML: %w", err)
	}
	return out, nil
}
