package office

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode"
)

const (
	maxDocxBytes           = 32 << 20 // 32 MiB raw file cap
	maxZipEntryBytes       = 16 << 20 // 16 MiB per uncompressed XML entry (zip bomb guard)
	maxEmbeddedImageBudget = 4 << 20  // 4 MiB total embedded image data URIs per preview
)

type DocxOutlineItem struct {
	ID    string `json:"id"`
	Level int    `json:"level"`
	Text  string `json:"text"`
}

type DocxRun struct {
	Text      string `json:"text"`
	Bold      bool   `json:"bold,omitempty"`
	Italic    bool   `json:"italic,omitempty"`
	Underline bool   `json:"underline,omitempty"`
	Strike    bool   `json:"strike,omitempty"`
	Code      bool   `json:"code,omitempty"`
	Color     string `json:"color,omitempty"`
	Href      string `json:"href,omitempty"`
}

type DocxTableCell struct {
	Runs     []DocxRun `json:"runs"`
	Text     string    `json:"text"`
	ColSpan  int       `json:"colspan,omitempty"`
	RowSpan  int       `json:"rowspan,omitempty"`
	IsHeader bool      `json:"is_header,omitempty"`
}

type DocxTableRow struct {
	Cells    []DocxTableCell `json:"cells"`
	IsHeader bool            `json:"is_header,omitempty"`
}

type DocxBlock struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind"` // heading, paragraph, list_item, table, image, hr
	Level        int            `json:"level,omitempty"`
	Ordered      bool           `json:"ordered,omitempty"`
	Align        string         `json:"align,omitempty"`
	Runs         []DocxRun      `json:"runs,omitempty"`
	Rows         []DocxTableRow `json:"rows,omitempty"`
	ImageDataURI string         `json:"image_data_uri,omitempty"`
	ImageAlt     string         `json:"image_alt,omitempty"`
}

type DocxStats struct {
	Words      int `json:"words"`
	Paragraphs int `json:"paragraphs"`
	Tables     int `json:"tables"`
	Images     int `json:"images"`
	Headings   int `json:"headings"`
}

type DocxPreviewResponse struct {
	Path            string            `json:"path"`
	Name            string            `json:"name"`
	Title           string            `json:"title"`
	Outline         []DocxOutlineItem `json:"outline"`
	Blocks          []DocxBlock       `json:"blocks"`
	PlainParagraphs []string          `json:"plain_paragraphs"`
	Stats           DocxStats         `json:"stats"`
}

type docxRelInfo struct {
	Type       string
	Target     string
	TargetMode string
}

type docxParserCtx struct {
	files            map[string]*zip.File
	rels             map[string]docxRelInfo
	headingStyles    map[string]int
	orderedNumIDs    map[string]bool
	imageBudgetUsed  int
	blockSeq         int
	outline          []DocxOutlineItem
	blocks           []DocxBlock
	plainParagraphs  []string
	stats            DocxStats
	detectedTitle    string
}

// ParseDocxBytes parses a .docx binary into a structured preview model suitable
// for outline navigation, rich rendering, and paragraph-level diffing.
func ParseDocxBytes(name, filePath string, data []byte) (*DocxPreviewResponse, error) {
	if len(data) == 0 {
		return nil, errors.New("Word 文档为空")
	}
	if len(data) > maxDocxBytes {
		return nil, fmt.Errorf("Word 文档超过预览上限 (%d MB)", maxDocxBytes>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("无法解析 .docx 压缩包: %w", err)
	}

	ctx := &docxParserCtx{
		files:         make(map[string]*zip.File, len(zr.File)),
		rels:          make(map[string]docxRelInfo),
		headingStyles: make(map[string]int),
		orderedNumIDs: make(map[string]bool),
		outline:       make([]DocxOutlineItem, 0),
		blocks:        make([]DocxBlock, 0),
		plainParagraphs: make([]string, 0),
	}
	for _, f := range zr.File {
		norm := strings.TrimPrefix(path.Clean(strings.ReplaceAll(f.Name, "\\", "/")), "/")
		ctx.files[strings.ToLower(norm)] = f
	}

	docBytes, err := ctx.readEntry("word/document.xml")
	if err != nil {
		return nil, fmt.Errorf("缺少 word/document.xml: %w", err)
	}

	if relsBytes, err := ctx.readEntry("word/_rels/document.xml.rels"); err == nil {
		ctx.parseRels(relsBytes)
	}
	if stylesBytes, err := ctx.readEntry("word/styles.xml"); err == nil {
		ctx.parseStyles(stylesBytes)
	}
	if numBytes, err := ctx.readEntry("word/numbering.xml"); err == nil {
		ctx.parseNumbering(numBytes)
	}
	if coreBytes, err := ctx.readEntry("docProps/core.xml"); err == nil {
		ctx.detectedTitle = parseCoreTitle(coreBytes)
	}

	if err := ctx.parseDocument(docBytes); err != nil {
		return nil, fmt.Errorf("解析文档正文失败: %w", err)
	}

	title := ctx.detectedTitle
	if title == "" {
		for _, item := range ctx.outline {
			if item.Level == 1 && strings.TrimSpace(item.Text) != "" {
				title = item.Text
				break
			}
		}
	}
	if title == "" {
		title = strings.TrimSuffix(name, path.Ext(name))
	}

	return &DocxPreviewResponse{
		Path:            filePath,
		Name:            name,
		Title:           title,
		Outline:         ctx.outline,
		Blocks:          ctx.blocks,
		PlainParagraphs: ctx.plainParagraphs,
		Stats:           ctx.stats,
	}, nil
}

func (c *docxParserCtx) readEntry(entryPath string) ([]byte, error) {
	f, ok := c.files[strings.ToLower(strings.TrimPrefix(path.Clean(entryPath), "/"))]
	if !ok {
		return nil, errors.New("entry not found")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxZipEntryBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxZipEntryBytes {
		return nil, errors.New("zip entry exceeds decompression safety limit")
	}
	return data, nil
}

func (c *docxParserCtx) parseRels(data []byte) {
	var parsed struct {
		Rels []struct {
			ID         string `xml:"Id,attr"`
			Type       string `xml:"Type,attr"`
			Target     string `xml:"Target,attr"`
			TargetMode string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	if xml.Unmarshal(data, &parsed) != nil {
		return
	}
	for _, r := range parsed.Rels {
		c.rels[r.ID] = docxRelInfo{
			Type:       r.Type,
			Target:     r.Target,
			TargetMode: r.TargetMode,
		}
	}
}

func (c *docxParserCtx) parseStyles(data []byte) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var curStyleID, curName string
	var curOutlineLvl = -1
	inStyle := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "style":
				inStyle = true
				curStyleID = xmlAttr(t.Attr, "styleId")
				curName = ""
				curOutlineLvl = -1
			case "name":
				if inStyle {
					curName = xmlAttr(t.Attr, "val")
				}
			case "outlineLvl":
				if inStyle {
					if v, err := strconv.Atoi(xmlAttr(t.Attr, "val")); err == nil && v >= 0 && v <= 5 {
						curOutlineLvl = v + 1
					}
				}
			}
		case xml.EndElement:
			if t.Name.Local == "style" && inStyle {
				if curStyleID != "" {
					lvl := curOutlineLvl
					if lvl <= 0 {
						lvl = detectHeadingLevelByName(curStyleID, curName)
					}
					if lvl > 0 {
						c.headingStyles[strings.ToLower(curStyleID)] = lvl
					}
				}
				inStyle = false
			}
		}
	}
}

func detectHeadingLevelByName(styleID, styleName string) int {
	for _, candidate := range []string{styleID, styleName} {
		low := strings.ToLower(strings.TrimSpace(candidate))
		if low == "title" || low == "标题" {
			return 1
		}
		for lvl := 1; lvl <= 6; lvl++ {
			d := strconv.Itoa(lvl)
			if low == "heading"+d || low == "heading "+d || low == "标题 "+d || low == "标题"+d || low == "toc"+d {
				return lvl
			}
		}
	}
	return 0
}

func (c *docxParserCtx) parseNumbering(data []byte) {
	abstractOrdered := make(map[string]bool)
	numToAbstract := make(map[string]string)

	dec := xml.NewDecoder(bytes.NewReader(data))
	var curAbsID, curNumID string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "abstractNum":
				curAbsID = xmlAttr(t.Attr, "abstractNumId")
			case "numFmt":
				if curAbsID != "" {
					fmtVal := strings.ToLower(xmlAttr(t.Attr, "val"))
					if fmtVal != "" && fmtVal != "bullet" && fmtVal != "none" {
						abstractOrdered[curAbsID] = true
					}
				}
			case "num":
				curNumID = xmlAttr(t.Attr, "numId")
			case "abstractNumId":
				if curNumID != "" {
					numToAbstract[curNumID] = xmlAttr(t.Attr, "val")
				}
			}
		case xml.EndElement:
			if t.Name.Local == "abstractNum" {
				curAbsID = ""
			} else if t.Name.Local == "num" {
				curNumID = ""
			}
		}
	}
	for numID, absID := range numToAbstract {
		if abstractOrdered[absID] {
			c.orderedNumIDs[numID] = true
		}
	}
}

func parseCoreTitle(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "title" {
			var title string
			if dec.DecodeElement(&title, &se) == nil {
				return strings.TrimSpace(title)
			}
		}
	}
	return ""
}

func (c *docxParserCtx) parseDocument(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "body" {
			return c.parseContainerChildren(dec, "body")
		}
	}
}

func (c *docxParserCtx) parseContainerChildren(dec *xml.Decoder, endLocal string) error {
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				if err := c.parseParagraph(dec); err != nil {
					return err
				}
			case "tbl":
				if err := c.parseTable(dec); err != nil {
					return err
				}
			case "sdt":
				// Structured document tags can wrap paragraphs/tables (e.g. TOC / cover blocks)
				if err := c.parseSDT(dec); err != nil {
					return err
				}
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if t.Name.Local == endLocal {
				return nil
			}
		}
	}
}

func (c *docxParserCtx) parseSDT(dec *xml.Decoder) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "sdtContent" {
				if err := c.parseContainerChildren(dec, "sdtContent"); err != nil {
					return err
				}
			} else {
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if t.Name.Local == "sdt" {
				return nil
			}
		}
	}
}

func (c *docxParserCtx) parseParagraph(dec *xml.Decoder) error {
	var (
		styleID    string
		outlineLvl = -1
		numID      string
		ilvl       int
		hasNumPr   bool
		align      string
		hasBottomB bool
		runs       []DocxRun
		imageIDs   []string
	)

	readRun := func(d *xml.Decoder, href string) error {
		var r DocxRun
		r.Href = href
		var textBuf strings.Builder
		for {
			tok, err := d.Token()
			if err != nil {
				return err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				switch t.Name.Local {
				case "rPr":
					if err := parseRunProps(d, &r); err != nil {
						return err
					}
				case "t", "instrText":
					if t.Name.Local == "t" {
						var s string
						if err := d.DecodeElement(&s, &t); err != nil {
							return err
						}
						textBuf.WriteString(s)
					} else {
						if err := d.Skip(); err != nil {
							return err
						}
					}
				case "tab":
					textBuf.WriteString("    ")
					if err := d.Skip(); err != nil {
						return err
					}
				case "br", "cr":
					textBuf.WriteByte('\n')
					if err := d.Skip(); err != nil {
						return err
					}
				case "drawing", "pict":
					ids, err := extractEmbeddedImageIDs(d, t.Name.Local)
					if err != nil {
						return err
					}
					imageIDs = append(imageIDs, ids...)
				default:
					if err := d.Skip(); err != nil {
						return err
					}
				}
			case xml.EndElement:
				if t.Name.Local == "r" {
					r.Text = textBuf.String()
					if r.Text != "" {
						runs = append(runs, r)
					}
					return nil
				}
			}
		}
	}

	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "pPr":
				if err := parseParagraphProps(dec, &styleID, &outlineLvl, &numID, &ilvl, &hasNumPr, &align, &hasBottomB); err != nil {
					return err
				}
			case "r":
				if err := readRun(dec, ""); err != nil {
					return err
				}
			case "hyperlink":
				relID := xmlAttr(t.Attr, "id")
				href := ""
				if info, ok := c.rels[relID]; ok && isExternalURL(info.Target) {
					href = info.Target
				}
			hyperlinkLoop:
				for {
					ht, err := dec.Token()
					if err != nil {
						return err
					}
					switch h := ht.(type) {
					case xml.StartElement:
						if h.Name.Local == "r" {
							if err := readRun(dec, href); err != nil {
								return err
							}
						} else if err := dec.Skip(); err != nil {
							return err
						}
					case xml.EndElement:
						if h.Name.Local == "hyperlink" {
							break hyperlinkLoop
						}
					}
				}
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if t.Name.Local == "p" {
				goto emit
			}
		}
	}

emit:
	plain := strings.TrimSpace(joinDocxRuns(runs))

	// Emit embedded images first if paragraph has drawing(s)
	for _, rid := range imageIDs {
		if dataURI, ok := c.loadEmbeddedImage(rid); ok {
			c.blockSeq++
			c.stats.Images++
			c.blocks = append(c.blocks, DocxBlock{
				ID:           fmt.Sprintf("blk-%d", c.blockSeq),
				Kind:         "image",
				ImageDataURI: dataURI,
				ImageAlt:     fmt.Sprintf("嵌入图片 %d", c.stats.Images),
			})
		}
	}

	if plain == "" {
		if hasBottomB && len(imageIDs) == 0 {
			c.blockSeq++
			c.blocks = append(c.blocks, DocxBlock{
				ID:   fmt.Sprintf("blk-%d", c.blockSeq),
				Kind: "hr",
			})
		}
		return nil
	}

	c.blockSeq++
	blkID := fmt.Sprintf("blk-%d", c.blockSeq)
	headingLvl := 0
	if styleID != "" {
		if lvl, ok := c.headingStyles[strings.ToLower(styleID)]; ok {
			headingLvl = lvl
		} else {
			headingLvl = detectHeadingLevelByName(styleID, "")
		}
	}
	if headingLvl == 0 && outlineLvl >= 0 && outlineLvl <= 5 {
		headingLvl = outlineLvl + 1
	}

	c.stats.Words += countMixedWords(plain)
	c.plainParagraphs = append(c.plainParagraphs, plain)

	if headingLvl > 0 {
		c.stats.Headings++
		c.outline = append(c.outline, DocxOutlineItem{
			ID:    blkID,
			Level: headingLvl,
			Text:  plain,
		})
		c.blocks = append(c.blocks, DocxBlock{
			ID:    blkID,
			Kind:  "heading",
			Level: headingLvl,
			Align: align,
			Runs:  runs,
		})
		return nil
	}

	if hasNumPr {
		c.stats.Paragraphs++
		c.blocks = append(c.blocks, DocxBlock{
			ID:      blkID,
			Kind:    "list_item",
			Level:   ilvl,
			Ordered: c.orderedNumIDs[numID],
			Align:   align,
			Runs:    runs,
		})
		return nil
	}

	c.stats.Paragraphs++
	c.blocks = append(c.blocks, DocxBlock{
		ID:    blkID,
		Kind:  "paragraph",
		Align: align,
		Runs:  runs,
	})
	return nil
}

func parseParagraphProps(dec *xml.Decoder, styleID *string, outlineLvl *int, numID *string, ilvl *int, hasNumPr *bool, align *string, hasBottomB *bool) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "pStyle":
				*styleID = xmlAttr(t.Attr, "val")
				_ = dec.Skip()
			case "outlineLvl":
				if v, err := strconv.Atoi(xmlAttr(t.Attr, "val")); err == nil {
					*outlineLvl = v
				}
				_ = dec.Skip()
			case "jc":
				*align = strings.ToLower(xmlAttr(t.Attr, "val"))
				_ = dec.Skip()
			case "numPr":
				*hasNumPr = true
			numPrLoop:
				for {
					nt, err := dec.Token()
					if err != nil {
						return err
					}
					switch n := nt.(type) {
					case xml.StartElement:
						if n.Name.Local == "ilvl" {
							*ilvl, _ = strconv.Atoi(xmlAttr(n.Attr, "val"))
						} else if n.Name.Local == "numId" {
							*numID = xmlAttr(n.Attr, "val")
						}
						_ = dec.Skip()
					case xml.EndElement:
						if n.Name.Local == "numPr" {
							break numPrLoop
						}
					}
				}
			case "pBdr":
			pBdrLoop:
				for {
					bt, err := dec.Token()
					if err != nil {
						return err
					}
					switch b := bt.(type) {
					case xml.StartElement:
						if b.Name.Local == "bottom" && xmlAttr(b.Attr, "val") != "none" {
							*hasBottomB = true
						}
						_ = dec.Skip()
					case xml.EndElement:
						if b.Name.Local == "pBdr" {
							break pBdrLoop
						}
					}
				}
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if t.Name.Local == "pPr" {
				return nil
			}
		}
	}
}

func parseRunProps(dec *xml.Decoder, r *DocxRun) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "b":
				val := strings.ToLower(xmlAttr(t.Attr, "val"))
				r.Bold = val != "0" && val != "false"
			case "i":
				val := strings.ToLower(xmlAttr(t.Attr, "val"))
				r.Italic = val != "0" && val != "false"
			case "u":
				val := strings.ToLower(xmlAttr(t.Attr, "val"))
				r.Underline = val != "none" && val != "0" && val != "false"
			case "strike", "dstrike":
				val := strings.ToLower(xmlAttr(t.Attr, "val"))
				r.Strike = val != "0" && val != "false"
			case "color":
				col := strings.TrimSpace(xmlAttr(t.Attr, "val"))
				if col != "" && !strings.EqualFold(col, "auto") && len(col) == 6 {
					r.Color = "#" + col
				}
			case "rFonts":
				ascii := strings.ToLower(xmlAttr(t.Attr, "ascii"))
				if strings.Contains(ascii, "consolas") || strings.Contains(ascii, "courier") || strings.Contains(ascii, "mono") {
					r.Code = true
				}
			}
			if err := dec.Skip(); err != nil {
				return err
			}
		case xml.EndElement:
			if t.Name.Local == "rPr" {
				return nil
			}
		}
	}
}

func extractEmbeddedImageIDs(dec *xml.Decoder, endLocal string) ([]string, error) {
	var ids []string
	for {
		tok, err := dec.Token()
		if err != nil {
			return ids, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "blip" {
				if id := xmlAttr(t.Attr, "embed"); id != "" {
					ids = append(ids, id)
				}
			} else if t.Name.Local == "imagedata" {
				if id := xmlAttr(t.Attr, "id"); id != "" {
					ids = append(ids, id)
				}
			}
		case xml.EndElement:
			if t.Name.Local == endLocal {
				return ids, nil
			}
		}
	}
}

func (c *docxParserCtx) loadEmbeddedImage(relID string) (string, bool) {
	info, ok := c.rels[relID]
	if !ok || strings.EqualFold(info.TargetMode, "External") {
		return "", false
	}
	target := strings.TrimPrefix(info.Target, "/")
	if !strings.HasPrefix(strings.ToLower(target), "word/") {
		target = path.Join("word", target)
	}
	ext := strings.ToLower(path.Ext(target))
	var mimeType string
	switch ext {
	case ".png":
		mimeType = "image/png"
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".gif":
		mimeType = "image/gif"
	case ".webp":
		mimeType = "image/webp"
	case ".svg":
		mimeType = "image/svg+xml"
	default:
		return "", false
	}
	raw, err := c.readEntry(target)
	if err != nil || len(raw) == 0 {
		return "", false
	}
	if c.imageBudgetUsed+len(raw) > maxEmbeddedImageBudget {
		return "", false
	}
	c.imageBudgetUsed += len(raw)
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(raw), true
}

type rawTableCell struct {
	cell     DocxTableCell
	colSpan  int
	vMerge   string // "", "restart", "continue"
	gridCol  int
	skipped  bool
}

func (c *docxParserCtx) parseTable(dec *xml.Decoder) error {
	var rawRows [][]rawTableCell
	var rowHeaders []bool

	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "tr" {
				cells, isHeader, err := c.parseTableRow(dec)
				if err != nil {
					return err
				}
				rawRows = append(rawRows, cells)
				rowHeaders = append(rowHeaders, isHeader)
			} else {
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if t.Name.Local == "tbl" {
				goto finalize
			}
		}
	}

finalize:
	if len(rawRows) == 0 {
		return nil
	}

	// Resolve vertical merges (vMerge) into RowSpan values
	type activeAnchor struct {
		rowIdx  int
		cellIdx int
	}
	anchors := make(map[int]activeAnchor)
	for ri := range rawRows {
		gridCol := 0
		for ci := range rawRows[ri] {
			rc := &rawRows[ri][ci]
			rc.gridCol = gridCol
			span := rc.colSpan
			if span <= 0 {
				span = 1
			}
			switch rc.vMerge {
			case "restart":
				rc.cell.RowSpan = 1
				anchors[gridCol] = activeAnchor{rowIdx: ri, cellIdx: ci}
			case "continue":
				if anc, ok := anchors[gridCol]; ok {
					rawRows[anc.rowIdx][anc.cellIdx].cell.RowSpan++
					rc.skipped = true
				}
			default:
				delete(anchors, gridCol)
			}
			gridCol += span
		}
	}

	// Build final rows; if no explicit tblHeader, treat row 0 as header when >= 2 rows
	anyHeader := false
	for _, h := range rowHeaders {
		if h {
			anyHeader = true
			break
		}
	}
	if !anyHeader && len(rawRows) >= 2 {
		rowHeaders[0] = true
	}

	rows := make([]DocxTableRow, 0, len(rawRows))
	for ri, rCells := range rawRows {
		outCells := make([]DocxTableCell, 0, len(rCells))
		var rowPlain []string
		for _, rc := range rCells {
			if rc.skipped {
				continue
			}
			cell := rc.cell
			if rowHeaders[ri] {
				cell.IsHeader = true
			}
			outCells = append(outCells, cell)
			if strings.TrimSpace(cell.Text) != "" {
				rowPlain = append(rowPlain, strings.TrimSpace(cell.Text))
			}
		}
		if len(outCells) > 0 {
			rows = append(rows, DocxTableRow{
				Cells:    outCells,
				IsHeader: rowHeaders[ri],
			})
			if len(rowPlain) > 0 {
				line := strings.Join(rowPlain, " | ")
				c.stats.Words += countMixedWords(line)
				c.plainParagraphs = append(c.plainParagraphs, line)
			}
		}
	}

	if len(rows) > 0 {
		c.blockSeq++
		c.stats.Tables++
		c.blocks = append(c.blocks, DocxBlock{
			ID:   fmt.Sprintf("blk-%d", c.blockSeq),
			Kind: "table",
			Rows: rows,
		})
	}
	return nil
}

func (c *docxParserCtx) parseTableRow(dec *xml.Decoder) ([]rawTableCell, bool, error) {
	var cells []rawTableCell
	isHeader := false
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, false, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "trPr":
			trPrLoop:
				for {
					pt, err := dec.Token()
					if err != nil {
						return nil, false, err
					}
					switch p := pt.(type) {
					case xml.StartElement:
						if p.Name.Local == "tblHeader" {
							isHeader = true
						}
						_ = dec.Skip()
					case xml.EndElement:
						if p.Name.Local == "trPr" {
							break trPrLoop
						}
					}
				}
			case "tc":
				rc, err := c.parseTableCell(dec)
				if err != nil {
					return nil, false, err
				}
				cells = append(cells, rc)
			default:
				if err := dec.Skip(); err != nil {
					return nil, false, err
				}
			}
		case xml.EndElement:
			if t.Name.Local == "tr" {
				return cells, isHeader, nil
			}
		}
	}
}

func (c *docxParserCtx) parseTableCell(dec *xml.Decoder) (rawTableCell, error) {
	rc := rawTableCell{colSpan: 1}
	var allRuns []DocxRun
	paraCount := 0

	for {
		tok, err := dec.Token()
		if err != nil {
			return rc, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tcPr":
			tcPrLoop:
				for {
					pt, err := dec.Token()
					if err != nil {
						return rc, err
					}
					switch p := pt.(type) {
					case xml.StartElement:
						switch p.Name.Local {
						case "gridSpan":
							if v, err := strconv.Atoi(xmlAttr(p.Attr, "val")); err == nil && v > 1 {
								rc.colSpan = v
							}
						case "vMerge":
							val := strings.ToLower(xmlAttr(p.Attr, "val"))
							if val == "restart" {
								rc.vMerge = "restart"
							} else {
								rc.vMerge = "continue"
							}
						}
						_ = dec.Skip()
					case xml.EndElement:
						if p.Name.Local == "tcPr" {
							break tcPrLoop
						}
					}
				}
			case "p":
				pRuns, err := parseCellParagraphRuns(dec)
				if err != nil {
					return rc, err
				}
				if len(pRuns) > 0 {
					if paraCount > 0 {
						allRuns = append(allRuns, DocxRun{Text: "\n"})
					}
					allRuns = append(allRuns, pRuns...)
					paraCount++
				}
			default:
				if err := dec.Skip(); err != nil {
					return rc, err
				}
			}
		case xml.EndElement:
			if t.Name.Local == "tc" {
				rc.cell = DocxTableCell{
					Runs:    allRuns,
					Text:    strings.TrimSpace(joinDocxRuns(allRuns)),
					ColSpan: rc.colSpan,
				}
				if rc.colSpan <= 1 {
					rc.cell.ColSpan = 0
				}
				return rc, nil
			}
		}
	}
}

func parseCellParagraphRuns(dec *xml.Decoder) ([]DocxRun, error) {
	var runs []DocxRun
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "r" {
				var r DocxRun
				var buf strings.Builder
			cellRunLoop:
				for {
					rt, err := dec.Token()
					if err != nil {
						return nil, err
					}
					switch re := rt.(type) {
					case xml.StartElement:
						switch re.Name.Local {
						case "rPr":
							if err := parseRunProps(dec, &r); err != nil {
								return nil, err
							}
						case "t":
							var s string
							if err := dec.DecodeElement(&s, &re); err != nil {
								return nil, err
							}
							buf.WriteString(s)
						case "br", "cr":
							buf.WriteByte('\n')
							_ = dec.Skip()
						case "tab":
							buf.WriteString("  ")
							_ = dec.Skip()
						default:
							_ = dec.Skip()
						}
					case xml.EndElement:
						if re.Name.Local == "r" {
							r.Text = buf.String()
							if r.Text != "" {
								runs = append(runs, r)
							}
							break cellRunLoop
						}
					}
				}
			} else if t.Name.Local == "hyperlink" {
				// Read nested runs inside hyperlink in cell
				sub, err := parseCellParagraphRuns(dec)
				if err != nil {
					return nil, err
				}
				runs = append(runs, sub...)
			} else {
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			if t.Name.Local == "p" || t.Name.Local == "hyperlink" {
				return runs, nil
			}
		}
	}
}

func xmlAttr(attrs []xml.Attr, local string) string {
	for _, a := range attrs {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

func joinDocxRuns(runs []DocxRun) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.Text)
	}
	return b.String()
}

func countMixedWords(s string) int {
	count := 0
	inWord := false
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			count++
			inWord = false
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if !inWord {
				count++
				inWord = true
			}
		} else {
			inWord = false
		}
	}
	return count
}
