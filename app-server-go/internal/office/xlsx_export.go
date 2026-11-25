package office

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// SheetExportInput represents one worksheet to write into an .xlsx workbook.
type SheetExportInput struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// XlsxExportRequest is the JSON payload accepted by POST /api/v1/office/export/xlsx.
type XlsxExportRequest struct {
	FileName string             `json:"file_name"`
	Sheets   []SheetExportInput `json:"sheets"`
	Markdown string             `json:"markdown"`
}

type sharedStringPool struct {
	list  []string
	index map[string]int
}

func newSharedStringPool() *sharedStringPool {
	return &sharedStringPool{
		list:  make([]string, 0, 128),
		index: make(map[string]int, 128),
	}
}

func (p *sharedStringPool) Add(s string) int {
	clean := SanitizeXMLText(s)
	if idx, ok := p.index[clean]; ok {
		return idx
	}
	idx := len(p.list)
	p.list = append(p.list, clean)
	p.index[clean] = idx
	return idx
}

// ExportSheetsToXlsx writes one or more tabular sheets into a valid OOXML .xlsx
// file using Go's standard archive/zip and encoding/xml packages.
func ExportSheetsToXlsx(req XlsxExportRequest) ([]byte, error) {
	sheets := req.Sheets
	if len(sheets) == 0 && strings.TrimSpace(req.Markdown) != "" {
		extracted := ExtractMarkdownTables(req.Markdown)
		for _, t := range extracted {
			sheets = append(sheets, SheetExportInput{
				Name:    t.Name,
				Columns: t.Columns,
				Rows:    t.Rows,
			})
		}
	}
	if len(sheets) == 0 {
		return nil, errors.New("没有可导出的表格数据")
	}

	usedNames := make(map[string]bool, len(sheets))
	sheetNames := make([]string, len(sheets))
	for i, s := range sheets {
		sheetNames[i] = uniqueSheetName(s.Name, i+1, usedNames)
	}

	ss := newSharedStringPool()
	sheetXMLs := make([][]byte, len(sheets))
	for i, s := range sheets {
		sheetXMLs[i] = buildWorksheetXML(s, i == 0, ss)
	}

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	writePart := func(path string, data []byte) error {
		w, err := zw.Create(path)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}

	if err := writePart("[Content_Types].xml", []byte(xlsxContentTypesXML(len(sheets)))); err != nil {
		return nil, err
	}
	if err := writePart("_rels/.rels", []byte(xlsxRootRelsXML)); err != nil {
		return nil, err
	}
	if err := writePart("xl/workbook.xml", []byte(xlsxWorkbookXML(sheetNames))); err != nil {
		return nil, err
	}
	if err := writePart("xl/_rels/workbook.xml.rels", []byte(xlsxWorkbookRelsXML(len(sheets)))); err != nil {
		return nil, err
	}
	if err := writePart("xl/styles.xml", []byte(xlsxStylesXML)); err != nil {
		return nil, err
	}
	if err := writePart("xl/sharedStrings.xml", buildSharedStringsXML(ss)); err != nil {
		return nil, err
	}
	for i, xmlBytes := range sheetXMLs {
		if err := writePart(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), xmlBytes); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return zipBuf.Bytes(), nil
}

func uniqueSheetName(raw string, seq int, used map[string]bool) string {
	cleaned := strings.Map(func(r rune) rune {
		if strings.ContainsRune("[]:*?/\\\r\n\t", r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(SanitizeXMLText(raw)))
	cleaned = strings.Trim(strings.Join(strings.Fields(cleaned), " "), "'")
	if cleaned == "" {
		cleaned = fmt.Sprintf("Sheet%d", seq)
	}
	cleaned = truncateRunes(cleaned, 31)
	key := strings.ToLower(cleaned)
	if !used[key] {
		used[key] = true
		return cleaned
	}
	for n := 2; n < 1000; n++ {
		suffix := fmt.Sprintf(" (%d)", n)
		cand := truncateRunes(cleaned, 31-len([]rune(suffix))) + suffix
		candKey := strings.ToLower(cand)
		if !used[candKey] {
			used[candKey] = true
			return cand
		}
	}
	fallback := fmt.Sprintf("Sheet%d", seq)
	used[strings.ToLower(fallback)] = true
	return fallback
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

func columnIndexToLetters(col int) string {
	var chars []byte
	for n := col + 1; n > 0; {
		n--
		chars = append(chars, byte('A'+(n%26)))
		n /= 26
	}
	for l, r := 0, len(chars)-1; l < r; l, r = l+1, r-1 {
		chars[l], chars[r] = chars[r], chars[l]
	}
	return string(chars)
}

func estimateDisplayWidth(s string) float64 {
	var w float64
	for _, r := range s {
		if r > 0x7F {
			w += 2.0
		} else {
			w += 1.05
		}
	}
	return w
}

func buildWorksheetXML(sheet SheetExportInput, selected bool, ss *sharedStringPool) []byte {
	colCount := len(sheet.Columns)
	for _, row := range sheet.Rows {
		if len(row) > colCount {
			colCount = len(row)
		}
	}
	if colCount == 0 {
		colCount = 1
	}

	widths := make([]float64, colCount)
	for ci := 0; ci < colCount; ci++ {
		widths[ci] = 10
		if ci < len(sheet.Columns) {
			if w := estimateDisplayWidth(sheet.Columns[ci]) + 4; w > widths[ci] {
				widths[ci] = w
			}
		}
	}

	var rowsBuf bytes.Buffer
	rowNum := 0

	hasHeader := len(sheet.Columns) > 0
	if hasHeader {
		rowNum++
		rowsBuf.WriteString(fmt.Sprintf(`<row r="%d" ht="24" customHeight="1">`, rowNum))
		for ci := 0; ci < colCount; ci++ {
			colName := ""
			if ci < len(sheet.Columns) {
				colName = sheet.Columns[ci]
			}
			ref := fmt.Sprintf("%s%d", columnIndexToLetters(ci), rowNum)
			idx := ss.Add(colName)
			rowsBuf.WriteString(fmt.Sprintf(`<c r="%s" s="1" t="s"><v>%d</v></c>`, ref, idx))
		}
		rowsBuf.WriteString(`</row>`)
	}

	for ri, row := range sheet.Rows {
		rowNum++
		isAlt := ri%2 == 1
		rowsBuf.WriteString(fmt.Sprintf(`<row r="%d">`, rowNum))
		for ci := 0; ci < colCount; ci++ {
			var val any
			if ci < len(row) {
				val = row[ci]
			}
			if val == nil {
				continue
			}
			ref := fmt.Sprintf("%s%d", columnIndexToLetters(ci), rowNum)
			if num, isNum := coerceXlsxNumber(val); isNum {
				style := 2
				if isAlt {
					style = 4
				}
				formatted := strconv.FormatFloat(num, 'f', -1, 64)
				if w := estimateDisplayWidth(formatted) + 3; w > widths[ci] {
					widths[ci] = w
				}
				rowsBuf.WriteString(fmt.Sprintf(`<c r="%s" s="%d"><v>%s</v></c>`, ref, style, formatted))
				continue
			}
			text := formatCellAsString(val)
			if text == "" {
				continue
			}
			if w := estimateDisplayWidth(text) + 3; w > widths[ci] {
				widths[ci] = w
			}
			style := 0
			if isAlt {
				style = 3
			}
			idx := ss.Add(text)
			rowsBuf.WriteString(fmt.Sprintf(`<c r="%s" s="%d" t="s"><v>%d</v></c>`, ref, style, idx))
		}
		rowsBuf.WriteString(`</row>`)
	}

	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)

	tabSel := "0"
	if selected {
		tabSel = "1"
	}
	b.WriteString(`<sheetViews><sheetView tabSelected="` + tabSel + `" workbookViewId="0">`)
	if hasHeader {
		b.WriteString(`<pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/>`)
	}
	b.WriteString(`</sheetView></sheetViews>`)
	b.WriteString(`<sheetFormatPr defaultRowHeight="20"/>`)

	b.WriteString(`<cols>`)
	for ci, w := range widths {
		if w < 10 {
			w = 10
		}
		if w > 48 {
			w = 48
		}
		b.WriteString(fmt.Sprintf(`<col min="%d" max="%d" width="%.2f" customWidth="1"/>`, ci+1, ci+1, w))
	}
	b.WriteString(`</cols>`)

	b.WriteString(`<sheetData>`)
	b.Write(rowsBuf.Bytes())
	b.WriteString(`</sheetData>`)

	if hasHeader && rowNum >= 1 {
		lastCol := columnIndexToLetters(colCount - 1)
		b.WriteString(fmt.Sprintf(`<autoFilter ref="A1:%s%d"/>`, lastCol, rowNum))
	}
	b.WriteString(`</worksheet>`)
	return b.Bytes()
}

func coerceXlsxNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		if n > 9007199254740991 || n < -9007199254740991 {
			return 0, false
		}
		return float64(n), true
	case uint64:
		if n > 9007199254740991 {
			return 0, false
		}
		return float64(n), true
	case float32:
		f := float64(n)
		return f, !math.IsNaN(f) && !math.IsInf(f, 0)
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case json.Number:
		raw := n.String()
		if parsed, ok := parseScalarCellValue(raw).(float64); ok {
			return parsed, true
		}
		return 0, false
	default:
		return 0, false
	}
}

func formatCellAsString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "TRUE"
		}
		return "FALSE"
	case json.Number:
		return x.String()
	default:
		encoded, err := json.Marshal(x)
		if err == nil {
			return string(encoded)
		}
		return fmt.Sprint(x)
	}
}

func buildSharedStringsXML(ss *sharedStringPool) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(fmt.Sprintf(`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%d" uniqueCount="%d">`, len(ss.list), len(ss.list)))
	for _, s := range ss.list {
		b.WriteString(`<si><t xml:space="preserve">`)
		_ = xml.EscapeText(&b, []byte(s))
		b.WriteString(`</t></si>`)
	}
	b.WriteString(`</sst>`)
	return b.Bytes()
}

func xlsxContentTypesXML(sheetCount int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	b.WriteString(`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`)
	b.WriteString(`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	b.WriteString(`<Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>`)
	for i := 1; i <= sheetCount; i++ {
		b.WriteString(fmt.Sprintf(`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i))
	}
	b.WriteString(`</Types>`)
	return b.String()
}

const xlsxRootRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
	`</Relationships>`

func xlsxWorkbookXML(names []string) string {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	for i, name := range names {
		b.WriteString(`<sheet name="`)
		_ = xml.EscapeText(&b, []byte(name))
		b.WriteString(fmt.Sprintf(`" sheetId="%d" r:id="rId%d"/>`, i+1, i+1))
	}
	b.WriteString(`</sheets></workbook>`)
	return b.String()
}

func xlsxWorkbookRelsXML(sheetCount int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 1; i <= sheetCount; i++ {
		b.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i, i))
	}
	b.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`, sheetCount+1))
	b.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/>`, sheetCount+2))
	b.WriteString(`</Relationships>`)
	return b.String()
}

const xlsxStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
	`<fonts count="2">` +
	`<font><sz val="10"/><color rgb="FF1E293B"/><name val="Microsoft YaHei"/></font>` +
	`<font><b/><sz val="10"/><color rgb="FF0F172A"/><name val="Microsoft YaHei"/></font>` +
	`</fonts>` +
	`<fills count="4">` +
	`<fill><patternFill patternType="none"/></fill>` +
	`<fill><patternFill patternType="gray125"/></fill>` +
	`<fill><patternFill patternType="solid"><fgColor rgb="FFEEF2F7"/><bgColor indexed="64"/></patternFill></fill>` +
	`<fill><patternFill patternType="solid"><fgColor rgb="FFF8FAFC"/><bgColor indexed="64"/></patternFill></fill>` +
	`</fills>` +
	`<borders count="2">` +
	`<border><left style="thin"><color rgb="FFE2E8F0"/></left><right style="thin"><color rgb="FFE2E8F0"/></right><top style="thin"><color rgb="FFE2E8F0"/></top><bottom style="thin"><color rgb="FFE2E8F0"/></bottom></border>` +
	`<border><left style="thin"><color rgb="FFCBD5E1"/></left><right style="thin"><color rgb="FFCBD5E1"/></right><top style="thin"><color rgb="FFCBD5E1"/></top><bottom style="medium"><color rgb="FF94A3B8"/></bottom></border>` +
	`</borders>` +
	`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
	`<cellXfs count="5">` +
	`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0" applyBorder="1" applyAlignment="1"><alignment vertical="center"/></xf>` +
	`<xf numFmtId="0" fontId="1" fillId="2" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1" applyAlignment="1"><alignment vertical="center"/></xf>` +
	`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0" applyBorder="1" applyAlignment="1"><alignment horizontal="right" vertical="center"/></xf>` +
	`<xf numFmtId="0" fontId="0" fillId="3" borderId="0" xfId="0" applyFill="1" applyBorder="1" applyAlignment="1"><alignment vertical="center"/></xf>` +
	`<xf numFmtId="0" fontId="0" fillId="3" borderId="0" xfId="0" applyFill="1" applyBorder="1" applyAlignment="1"><alignment horizontal="right" vertical="center"/></xf>` +
	`</cellXfs>` +
	`</styleSheet>`
