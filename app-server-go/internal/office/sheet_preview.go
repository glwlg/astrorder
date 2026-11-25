package office

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

const (
	maxSheetBytes         = 32 << 20
	defaultMaxPreviewRows = 5000
	defaultMaxPreviewCols = 200
)

type SheetTabPreview struct {
	Name      string   `json:"name"`
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	TotalRows int      `json:"total_rows"`
	TotalCols int      `json:"total_cols"`
	Truncated bool     `json:"truncated"`
}

type SheetPreviewResponse struct {
	Path     string            `json:"path"`
	Name     string            `json:"name"`
	Format   string            `json:"format"` // xlsx, csv, tsv
	Encoding string            `json:"encoding"`
	Sheets   []SheetTabPreview `json:"sheets"`
}

// ParseSheetBytes parses .xlsx, .csv, or .tsv files into a structured multi-sheet
// preview payload bounded by maxRows and maxCols.
func ParseSheetBytes(name, filePath string, data []byte, maxRows, maxCols int) (*SheetPreviewResponse, error) {
	if len(data) > maxSheetBytes {
		return nil, fmt.Errorf("表格文件超过预览上限 (%d MB)", maxSheetBytes>>20)
	}
	if maxRows <= 0 || maxRows > 20000 {
		maxRows = defaultMaxPreviewRows
	}
	if maxCols <= 0 || maxCols > 500 {
		maxCols = defaultMaxPreviewCols
	}

	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".xlsx":
		return parseXlsxBytes(name, filePath, data, maxRows, maxCols)
	case ".csv", ".tsv":
		return parseDelimitedBytes(name, filePath, ext, data, maxRows, maxCols)
	default:
		return nil, fmt.Errorf("不支持的表格格式: %s", ext)
	}
}

func parseDelimitedBytes(name, filePath, ext string, data []byte, maxRows, maxCols int) (*SheetPreviewResponse, error) {
	encodingName := "UTF-8"
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		data = data[3:]
		encodingName = "UTF-8 (BOM)"
	}
	var text string
	if utf8.Valid(data) {
		text = string(data)
	} else {
		decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data)
		if err != nil {
			text = strings.ToValidUTF8(string(data), "")
		} else {
			text = string(decoded)
			encodingName = "GB18030"
		}
	}

	comma := ','
	format := "csv"
	if ext == ".tsv" {
		comma = '\t'
		format = "tsv"
	} else {
		comma = sniffCSVDelimiter(text)
	}

	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = comma
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1

	var columns []string
	rows := make([][]any, 0, 64)
	totalRows := 0
	totalCols := 0
	truncated := false
	hasHeader := false

	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, csv.ErrFieldCount) {
				// FieldsPerRecord=-1 avoids this, but keep safe
			} else {
				return nil, fmt.Errorf("解析 CSV 失败: %w", err)
			}
		}
		if len(rec) > totalCols {
			totalCols = len(rec)
		}
		if !hasHeader {
			hasHeader = true
			limit := len(rec)
			if limit > maxCols {
				limit = maxCols
				truncated = true
			}
			columns = make([]string, limit)
			for i := 0; i < limit; i++ {
				h := strings.TrimSpace(rec[i])
				if h == "" {
					h = fmt.Sprintf("列%d", i+1)
				}
				columns[i] = h
			}
			continue
		}
		totalRows++
		if len(rows) >= maxRows {
			truncated = true
			continue
		}
		limit := len(rec)
		if limit > maxCols {
			limit = maxCols
			truncated = true
		}
		if limit > len(columns) {
			for i := len(columns); i < limit; i++ {
				columns = append(columns, fmt.Sprintf("列%d", i+1))
			}
		}
		row := make([]any, len(columns))
		for i := 0; i < len(columns); i++ {
			if i < limit {
				row[i] = parseScalarCellValue(rec[i])
			} else {
				row[i] = ""
			}
		}
		rows = append(rows, row)
	}
	if columns == nil {
		columns = []string{}
	}
	if totalCols > maxCols {
		truncated = true
	}

	tabName := strings.TrimSuffix(name, filepath.Ext(name))
	if tabName == "" {
		tabName = "Sheet1"
	}
	return &SheetPreviewResponse{
		Path:     filePath,
		Name:     name,
		Format:   format,
		Encoding: encodingName,
		Sheets: []SheetTabPreview{
			{
				Name:      tabName,
				Columns:   columns,
				Rows:      rows,
				TotalRows: totalRows,
				TotalCols: totalCols,
				Truncated: truncated,
			},
		},
	}, nil
}

func sniffCSVDelimiter(text string) rune {
	lines := strings.SplitN(text, "\n", 6)
	sample := strings.Join(lines[:min(len(lines), 5)], "\n")
	commas := strings.Count(sample, ",")
	tabs := strings.Count(sample, "\t")
	semis := strings.Count(sample, ";")
	if tabs > commas && tabs >= semis {
		return '\t'
	}
	if semis > commas && commas == 0 {
		return ';'
	}
	return ','
}

func parseXlsxBytes(name, filePath string, data []byte, maxRows, maxCols int) (*SheetPreviewResponse, error) {
	if len(data) == 0 {
		return nil, errors.New("Excel 文件为空")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("无法解析 .xlsx 压缩包: %w", err)
	}

	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		norm := strings.TrimPrefix(path.Clean(strings.ReplaceAll(f.Name, "\\", "/")), "/")
		files[strings.ToLower(norm)] = f
	}
	readEntry := func(p string) ([]byte, error) {
		f, ok := files[strings.ToLower(strings.TrimPrefix(path.Clean(p), "/"))]
		if !ok {
			return nil, errors.New("entry not found")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, maxZipEntryBytes+1))
		if err != nil {
			return nil, err
		}
		if len(b) > maxZipEntryBytes {
			return nil, errors.New("zip entry exceeds decompression safety limit")
		}
		return b, nil
	}

	var sharedStrings []string
	if ssBytes, err := readEntry("xl/sharedStrings.xml"); err == nil {
		sharedStrings = parseSharedStringsXML(ssBytes)
	}

	dateStyleIdx := make(map[int]bool)
	if styleBytes, err := readEntry("xl/styles.xml"); err == nil {
		dateStyleIdx = parseXlsxDateStyles(styleBytes)
	}

	wbBytes, err := readEntry("xl/workbook.xml")
	if err != nil {
		return nil, fmt.Errorf("缺少 xl/workbook.xml: %w", err)
	}
	relsMap := make(map[string]string)
	if relBytes, err := readEntry("xl/_rels/workbook.xml.rels"); err == nil {
		relsMap = parseSimpleRels(relBytes)
	}

	sheetDefs := parseWorkbookSheets(wbBytes)
	if len(sheetDefs) == 0 {
		return nil, errors.New("工作簿中未找到任何工作表")
	}

	tabs := make([]SheetTabPreview, 0, len(sheetDefs))
	for idx, def := range sheetDefs {
		target := relsMap[def.RelID]
		if target == "" {
			target = fmt.Sprintf("worksheets/sheet%d.xml", idx+1)
		}
		target = strings.TrimPrefix(target, "/")
		if !strings.HasPrefix(strings.ToLower(target), "xl/") {
			target = path.Join("xl", target)
		}
		wsBytes, err := readEntry(target)
		if err != nil {
			continue
		}
		tab := parseWorksheetXML(def.Name, wsBytes, sharedStrings, dateStyleIdx, maxRows, maxCols)
		tabs = append(tabs, tab)
	}

	return &SheetPreviewResponse{
		Path:     filePath,
		Name:     name,
		Format:   "xlsx",
		Encoding: "UTF-8",
		Sheets:   tabs,
	}, nil
}

type wbSheetDef struct {
	Name  string
	RelID string
}

func parseWorkbookSheets(data []byte) []wbSheetDef {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var defs []wbSheetDef
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sheet" {
			name := xmlAttr(se.Attr, "name")
			relID := xmlAttr(se.Attr, "id")
			if name != "" {
				defs = append(defs, wbSheetDef{Name: name, RelID: relID})
			}
		}
	}
	return defs
}

func parseSimpleRels(data []byte) map[string]string {
	var parsed struct {
		Rels []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	out := make(map[string]string)
	if xml.Unmarshal(data, &parsed) != nil {
		return out
	}
	for _, r := range parsed.Rels {
		out[r.ID] = r.Target
	}
	return out
}

func parseSharedStringsXML(data []byte) []string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []string
	var cur strings.Builder
	inSI := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "si" {
				inSI = true
				cur.Reset()
			} else if t.Name.Local == "t" && inSI {
				var s string
				if dec.DecodeElement(&s, &t) == nil {
					cur.WriteString(s)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "si" && inSI {
				out = append(out, cur.String())
				inSI = false
			}
		}
	}
	return out
}

func parseXlsxDateStyles(data []byte) map[int]bool {
	customDateFmts := make(map[int]bool)
	dateXfs := make(map[int]bool)

	dec := xml.NewDecoder(bytes.NewReader(data))
	inCellXfs := false
	xfIdx := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "numFmt":
				id, err := strconv.Atoi(xmlAttr(t.Attr, "numFmtId"))
				code := strings.ToLower(xmlAttr(t.Attr, "formatCode"))
				if err == nil && isLikelyDateFormatCode(code) {
					customDateFmts[id] = true
				}
			case "cellXfs":
				inCellXfs = true
				xfIdx = 0
			case "xf":
				if inCellXfs {
					fmtID, _ := strconv.Atoi(xmlAttr(t.Attr, "numFmtId"))
					if isBuiltinDateFmtID(fmtID) || customDateFmts[fmtID] {
						dateXfs[xfIdx] = true
					}
					xfIdx++
				}
			}
		case xml.EndElement:
			if t.Name.Local == "cellXfs" {
				inCellXfs = false
			}
		}
	}
	return dateXfs
}

func isBuiltinDateFmtID(id int) bool {
	return (id >= 14 && id <= 22) || (id >= 27 && id <= 36) || (id >= 45 && id <= 47) || (id >= 50 && id <= 58)
}

func isLikelyDateFormatCode(code string) bool {
	if code == "" {
		return false
	}
	// Strip quoted literals and bracket color/locale markers
	cleaned := strings.Builder{}
	inQuote := false
	inBracket := false
	for _, r := range code {
		if r == '"' {
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		if r == '[' {
			inBracket = true
			continue
		}
		if r == ']' {
			inBracket = false
			continue
		}
		if !inBracket {
			cleaned.WriteRune(r)
		}
	}
	s := cleaned.String()
	return strings.Contains(s, "yy") || strings.Contains(s, "dd") || (strings.Contains(s, "mm") && (strings.Contains(s, "/") || strings.Contains(s, "-")))
}

func parseWorksheetXML(name string, data []byte, sharedStrings []string, dateStyles map[int]bool, maxRows, maxCols int) SheetTabPreview {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var rawRows [][]any
	totalRows := 0
	totalCols := 0
	truncated := false

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "row" {
			continue
		}
		rowCells, rowCols := parseWorksheetRow(dec, sharedStrings, dateStyles, maxCols)
		if rowCols > totalCols {
			totalCols = rowCols
		}
		if rowCols == 0 {
			continue
		}
		totalRows++
		// Capture header + up to maxRows data rows
		if len(rawRows) <= maxRows {
			rawRows = append(rawRows, rowCells)
		} else {
			truncated = true
		}
	}

	if totalCols > maxCols {
		truncated = true
	}
	if len(rawRows) == 0 {
		return SheetTabPreview{
			Name:    name,
			Columns: []string{},
			Rows:    [][]any{},
		}
	}

	effectiveCols := totalCols
	if effectiveCols > maxCols {
		effectiveCols = maxCols
	}
	if effectiveCols == 0 {
		effectiveCols = len(rawRows[0])
	}

	// Promote first row as columns
	headerRow := rawRows[0]
	columns := make([]string, effectiveCols)
	for ci := 0; ci < effectiveCols; ci++ {
		if ci < len(headerRow) && headerRow[ci] != nil && strings.TrimSpace(fmt.Sprint(headerRow[ci])) != "" {
			columns[ci] = strings.TrimSpace(fmt.Sprint(headerRow[ci]))
		} else {
			columns[ci] = columnIndexToLetters(ci)
		}
	}

	dataRows := make([][]any, 0, max(0, len(rawRows)-1))
	for ri := 1; ri < len(rawRows); ri++ {
		src := rawRows[ri]
		out := make([]any, effectiveCols)
		for ci := 0; ci < effectiveCols; ci++ {
			if ci < len(src) && src[ci] != nil {
				out[ci] = src[ci]
			} else {
				out[ci] = ""
			}
		}
		dataRows = append(dataRows, out)
	}

	dataTotalRows := totalRows - 1
	if dataTotalRows < 0 {
		dataTotalRows = 0
	}

	return SheetTabPreview{
		Name:      name,
		Columns:   columns,
		Rows:      dataRows,
		TotalRows: dataTotalRows,
		TotalCols: totalCols,
		Truncated: truncated,
	}
}

func parseWorksheetRow(dec *xml.Decoder, sharedStrings []string, dateStyles map[int]bool, maxCols int) ([]any, int) {
	var row []any
	maxColSeen := 0
	seqCol := 0

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "c" {
				_ = dec.Skip()
				continue
			}
			cellRef := xmlAttr(t.Attr, "r")
			cellType := xmlAttr(t.Attr, "t")
			styleIdx, _ := strconv.Atoi(xmlAttr(t.Attr, "s"))

			colIdx := seqCol
			if parsedCol, ok := parseCellRefColumn(cellRef); ok {
				colIdx = parsedCol
			}
			seqCol = colIdx + 1

			val := readWorksheetCellValue(dec, cellType, styleIdx, sharedStrings, dateStyles)
			if val == nil || val == "" {
				continue
			}
			if colIdx+1 > maxColSeen {
				maxColSeen = colIdx + 1
			}
			if colIdx >= maxCols {
				continue
			}
			for len(row) <= colIdx {
				row = append(row, "")
			}
			row[colIdx] = val
		case xml.EndElement:
			if t.Name.Local == "row" {
				return row, maxColSeen
			}
		}
	}
	return row, maxColSeen
}

func parseCellRefColumn(ref string) (int, bool) {
	if ref == "" {
		return 0, false
	}
	col := 0
	letters := 0
	for i := 0; i < len(ref); i++ {
		ch := ref[i]
		if ch >= 'A' && ch <= 'Z' {
			col = col*26 + int(ch-'A'+1)
			letters++
		} else if ch >= 'a' && ch <= 'z' {
			col = col*26 + int(ch-'a'+1)
			letters++
		} else {
			break
		}
	}
	if letters == 0 {
		return 0, false
	}
	return col - 1, true
}

func readWorksheetCellValue(dec *xml.Decoder, cellType string, styleIdx int, sharedStrings []string, dateStyles map[int]bool) any {
	var rawV string
	var inlineBuf strings.Builder
	inInline := false

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "v":
				_ = dec.DecodeElement(&rawV, &t)
			case "is":
				inInline = true
			case "t":
				if inInline {
					var s string
					if dec.DecodeElement(&s, &t) == nil {
						inlineBuf.WriteString(s)
					}
				} else {
					_ = dec.Skip()
				}
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == "is" {
				inInline = false
			} else if t.Name.Local == "c" {
				return decodeXlsxCellValue(cellType, styleIdx, rawV, inlineBuf.String(), sharedStrings, dateStyles)
			}
		}
	}
	return ""
}

func decodeXlsxCellValue(cellType string, styleIdx int, rawV, inlineStr string, sharedStrings []string, dateStyles map[int]bool) any {
	switch cellType {
	case "s":
		idx, err := strconv.Atoi(strings.TrimSpace(rawV))
		if err == nil && idx >= 0 && idx < len(sharedStrings) {
			return sharedStrings[idx]
		}
		return ""
	case "inlineStr":
		return inlineStr
	case "str", "e":
		return rawV
	case "b":
		if strings.TrimSpace(rawV) == "1" {
			return "TRUE"
		}
		return "FALSE"
	default:
		trimmed := strings.TrimSpace(rawV)
		if trimmed == "" {
			return ""
		}
		f, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return trimmed
		}
		if dateStyles[styleIdx] && f >= 1 && f < 2958466 {
			return formatExcelSerialDate(f)
		}
		return f
	}
}

func formatExcelSerialDate(serial float64) string {
	// Excel 1900 date system (day 1 = 1900-01-01, with the historic 1900 leap-year offset)
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	days := int(math.Floor(serial))
	frac := serial - float64(days)
	secs := int(math.Round(frac * 86400))
	t := base.AddDate(0, 0, days).Add(time.Duration(secs) * time.Second)
	if secs == 0 {
		return t.Format("2006-01-02")
	}
	return t.Format("2006-01-02 15:04:05")
}
