package office

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// DiagramImage represents a rasterized PNG diagram (e.g. Mermaid flowchart)
// to be embedded inline in place of the corresponding Mermaid block.
type DiagramImage struct {
	Index     int    `json:"index"`
	PNGBase64 string `json:"png_base64"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

// DocxExportRequest is the JSON payload accepted by POST /api/v1/office/export/docx.
type DocxExportRequest struct {
	Title    string         `json:"title"`
	Subtitle string         `json:"subtitle"`
	Markdown string         `json:"markdown"`
	Diagrams []DiagramImage `json:"diagrams"`
	FileName string         `json:"file_name"`
}

type docxRel struct {
	ID         string
	Type       string
	Target     string
	TargetMode string
}

type docxMedia struct {
	ZipPath string
	Data    []byte
}

// ExportMarkdownToDocx generates an ECMA-376 compliant .docx binary from Markdown
// plus optional Mermaid PNG diagrams, using pure Go standard library packages.
func ExportMarkdownToDocx(req DocxExportRequest) ([]byte, error) {
	blocks := ParseMarkdownBlocks(req.Markdown)

	diagramMap := make(map[int]DiagramImage, len(req.Diagrams))
	for _, d := range req.Diagrams {
		if strings.TrimSpace(d.PNGBase64) != "" {
			diagramMap[d.Index] = d
		}
	}

	rels := []docxRel{
		{
			ID:     "rId1",
			Type:   "http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles",
			Target: "styles.xml",
		},
		{
			ID:     "rId2",
			Type:   "http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering",
			Target: "numbering.xml",
		},
	}
	nextRelID := 3
	mediaFiles := make([]docxMedia, 0, len(req.Diagrams))
	nextDocPrID := 1

	addHyperlinkRel := func(href string) string {
		id := fmt.Sprintf("rId%d", nextRelID)
		nextRelID++
		rels = append(rels, docxRel{
			ID:         id,
			Type:       "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink",
			Target:     SanitizeXMLText(href),
			TargetMode: "External",
		})
		return id
	}

	addImageRel := func(pngBytes []byte) string {
		id := fmt.Sprintf("rId%d", nextRelID)
		nextRelID++
		name := fmt.Sprintf("diagram_%d.png", len(mediaFiles)+1)
		mediaFiles = append(mediaFiles, docxMedia{
			ZipPath: "word/media/" + name,
			Data:    pngBytes,
		})
		rels = append(rels, docxRel{
			ID:     id,
			Type:   "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image",
			Target: "media/" + name,
		})
		return id
	}

	var body bytes.Buffer
	title := strings.TrimSpace(SanitizeXMLText(req.Title))
	subtitle := strings.TrimSpace(SanitizeXMLText(req.Subtitle))

	// Avoid adding a redundant filename banner when Markdown already begins with an H1 heading
	skipTitleHeader := len(blocks) > 0 && blocks[0].Kind == "heading" && blocks[0].Level == 1
	if title != "" && !skipTitleHeader {
		writeDocxTitleBlock(&body, title, subtitle)
	} else if subtitle != "" {
		writeDocxSubtitleParagraph(&body, subtitle)
	}

	for _, blk := range blocks {
		switch blk.Kind {
		case "heading":
			lvl := blk.Level
			if lvl < 1 {
				lvl = 1
			}
			if lvl > 4 {
				lvl = 4
			}
			body.WriteString(fmt.Sprintf(`<w:p><w:pPr><w:pStyle w:val="Heading%d"/></w:pPr>`, lvl))
			writeDocxRuns(&body, blk.Spans, addHyperlinkRel)
			body.WriteString(`</w:p>`)

		case "paragraph":
			body.WriteString(`<w:p><w:pPr><w:pStyle w:val="Normal"/></w:pPr>`)
			writeDocxRuns(&body, blk.Spans, addHyperlinkRel)
			body.WriteString(`</w:p>`)

		case "list_item":
			numID := 1
			if blk.Ordered {
				numID = 2
			}
			ilvl := blk.Level
			if ilvl < 0 {
				ilvl = 0
			}
			if ilvl > 4 {
				ilvl = 4
			}
			body.WriteString(fmt.Sprintf(
				`<w:p><w:pPr><w:pStyle w:val="ListParagraph"/><w:numPr><w:ilvl w:val="%d"/><w:numId w:val="%d"/></w:numPr></w:pPr>`,
				ilvl, numID,
			))
			writeDocxRuns(&body, blk.Spans, addHyperlinkRel)
			body.WriteString(`</w:p>`)

		case "blockquote":
			body.WriteString(`<w:p><w:pPr><w:pStyle w:val="Quote"/></w:pPr>`)
			writeDocxRuns(&body, blk.Spans, addHyperlinkRel)
			body.WriteString(`</w:p>`)

		case "code":
			writeDocxCodeBlock(&body, blk.Language, blk.Code)

		case "mermaid":
			if img, ok := diagramMap[blk.DiagramIndex]; ok {
				if pngBytes, w, h, decErr := decodeDiagramPNG(img); decErr == nil {
					relID := addImageRel(pngBytes)
					writeDocxImageParagraph(&body, relID, nextDocPrID, w, h, fmt.Sprintf("Diagram %d", blk.DiagramIndex+1))
					nextDocPrID++
					continue
				}
			}
			writeDocxCodeBlock(&body, "mermaid", blk.Code)

		case "table":
			writeDocxTable(&body, blk, addHyperlinkRel)

		case "hr":
			body.WriteString(`<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="6" w:space="4" w:color="CBD5E1"/></w:pBdr><w:spacing w:before="120" w:after="120"/></w:pPr></w:p>`)
		}
	}

	if len(blocks) == 0 && title == "" {
		body.WriteString(`<w:p><w:pPr><w:pStyle w:val="Normal"/></w:pPr></w:p>`)
	}

	// A4 page size: 11906 x 16838 dxa (210mm x 297mm), margins 1440 dxa (25.4mm)
	body.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr>`)

	documentXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
		`xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" ` +
		`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
		`xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">` +
		`<w:body>` + body.String() + `</w:body></w:document>`

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

	if err := writePart("[Content_Types].xml", []byte(docxContentTypesXML(len(mediaFiles) > 0))); err != nil {
		return nil, err
	}
	if err := writePart("_rels/.rels", []byte(docxRootRelsXML)); err != nil {
		return nil, err
	}
	if err := writePart("docProps/core.xml", []byte(docxCorePropsXML(title))); err != nil {
		return nil, err
	}
	if err := writePart("word/styles.xml", []byte(docxStylesXML)); err != nil {
		return nil, err
	}
	if err := writePart("word/numbering.xml", []byte(docxNumberingXML)); err != nil {
		return nil, err
	}
	if err := writePart("word/_rels/document.xml.rels", []byte(docxDocumentRelsXML(rels))); err != nil {
		return nil, err
	}
	if err := writePart("word/document.xml", []byte(documentXML)); err != nil {
		return nil, err
	}
	for _, mf := range mediaFiles {
		if err := writePart(mf.ZipPath, mf.Data); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return zipBuf.Bytes(), nil
}

func writeDocxTitleBlock(b *bytes.Buffer, title, subtitle string) {
	b.WriteString(`<w:p><w:pPr><w:spacing w:before="0" w:after="100"/><w:pBdr><w:bottom w:val="single" w:sz="12" w:space="6" w:color="1D4ED8"/></w:pBdr></w:pPr>`)
	b.WriteString(`<w:r><w:rPr><w:rFonts w:ascii="Microsoft YaHei" w:hAnsi="Microsoft YaHei" w:eastAsia="Microsoft YaHei"/><w:b/><w:color w:val="0F172A"/><w:sz w:val="44"/><w:szCs w:val="44"/></w:rPr><w:t xml:space="preserve">`)
	_ = xml.EscapeText(b, []byte(title))
	b.WriteString(`</w:t></w:r></w:p>`)
	if subtitle != "" {
		writeDocxSubtitleParagraph(b, subtitle)
	}
}

func writeDocxSubtitleParagraph(b *bytes.Buffer, subtitle string) {
	b.WriteString(`<w:p><w:pPr><w:spacing w:before="0" w:after="200"/></w:pPr>`)
	b.WriteString(`<w:r><w:rPr><w:rFonts w:ascii="Microsoft YaHei" w:hAnsi="Microsoft YaHei" w:eastAsia="Microsoft YaHei"/><w:color w:val="64748B"/><w:sz w:val="19"/><w:szCs w:val="19"/></w:rPr><w:t xml:space="preserve">`)
	_ = xml.EscapeText(b, []byte(subtitle))
	b.WriteString(`</w:t></w:r></w:p>`)
}

func writeDocxRuns(b *bytes.Buffer, spans []InlineSpan, addLink func(string) string) {
	for _, sp := range spans {
		clean := SanitizeXMLText(sp.Text)
		if clean == "" {
			continue
		}
		if sp.Href != "" {
			relID := addLink(sp.Href)
			b.WriteString(fmt.Sprintf(`<w:hyperlink r:id="%s">`, relID))
		}
		b.WriteString(`<w:r>`)
		if sp.Bold || sp.Italic || sp.Strike || sp.Code || sp.Href != "" {
			b.WriteString(`<w:rPr>`)
			if sp.Code {
				b.WriteString(`<w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:eastAsia="Microsoft YaHei"/>`)
				b.WriteString(`<w:color w:val="0F172A"/>`)
				b.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="F1F5F9"/>`)
				b.WriteString(`<w:sz w:val="19"/><w:szCs w:val="19"/>`)
			}
			if sp.Bold {
				b.WriteString(`<w:b/>`)
			}
			if sp.Italic {
				b.WriteString(`<w:i/>`)
			}
			if sp.Strike {
				b.WriteString(`<w:strike/>`)
			}
			if sp.Href != "" {
				b.WriteString(`<w:color w:val="2563EB"/><w:u w:val="single"/>`)
			}
			b.WriteString(`</w:rPr>`)
		}
		lines := strings.Split(clean, "\n")
		for idx, seg := range lines {
			if idx > 0 {
				b.WriteString(`<w:br/>`)
			}
			b.WriteString(`<w:t xml:space="preserve">`)
			_ = xml.EscapeText(b, []byte(seg))
			b.WriteString(`</w:t>`)
		}
		b.WriteString(`</w:r>`)
		if sp.Href != "" {
			b.WriteString(`</w:hyperlink>`)
		}
	}
}

func writeDocxCodeBlock(b *bytes.Buffer, lang, code string) {
	cleanCode := SanitizeXMLText(strings.TrimRight(code, "\n"))
	lines := strings.Split(cleanCode, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	// Wrap code block in a single-cell shaded table for crisp borders across Word & WPS
	b.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="9026" w:type="dxa"/><w:tblBorders>`)
	b.WriteString(`<w:top w:val="single" w:sz="4" w:space="0" w:color="E2E8F0"/>`)
	b.WriteString(`<w:left w:val="single" w:sz="12" w:space="0" w:color="94A3B8"/>`)
	b.WriteString(`<w:bottom w:val="single" w:sz="4" w:space="0" w:color="E2E8F0"/>`)
	b.WriteString(`<w:right w:val="single" w:sz="4" w:space="0" w:color="E2E8F0"/>`)
	b.WriteString(`</w:tblBorders><w:tblCellMar><w:top w:w="100" w:type="dxa"/><w:left w:w="160" w:type="dxa"/><w:bottom w:w="100" w:type="dxa"/><w:right w:w="160" w:type="dxa"/></w:tblCellMar></w:tblPr>`)
	b.WriteString(`<w:tblGrid><w:gridCol w:w="9026"/></w:tblGrid>`)
	b.WriteString(`<w:tr><w:tc><w:tcPr><w:tcW w:w="9026" w:type="dxa"/><w:shd w:val="clear" w:color="auto" w:fill="F8FAFC"/></w:tcPr>`)
	if lang != "" {
		b.WriteString(`<w:p><w:pPr><w:spacing w:before="0" w:after="60"/></w:pPr><w:r><w:rPr><w:rFonts w:ascii="Consolas" w:hAnsi="Consolas"/><w:b/><w:color w:val="64748B"/><w:sz w:val="16"/><w:szCs w:val="16"/></w:rPr><w:t>`)
		_ = xml.EscapeText(b, []byte(strings.ToUpper(SanitizeXMLText(lang))))
		b.WriteString(`</w:t></w:r></w:p>`)
	}
	b.WriteString(`<w:p><w:pPr><w:pStyle w:val="CodeBlock"/></w:pPr><w:r><w:rPr><w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:eastAsia="Microsoft YaHei"/><w:color w:val="1E293B"/><w:sz w:val="18"/><w:szCs w:val="18"/></w:rPr>`)
	for idx, line := range lines {
		if idx > 0 {
			b.WriteString(`<w:br/>`)
		}
		b.WriteString(`<w:t xml:space="preserve">`)
		_ = xml.EscapeText(b, []byte(line))
		b.WriteString(`</w:t>`)
	}
	b.WriteString(`</w:r></w:p></w:tc></w:tr></w:tbl>`)
	b.WriteString(`<w:p><w:pPr><w:spacing w:before="0" w:after="100"/></w:pPr></w:p>`)
}

func writeDocxTable(b *bytes.Buffer, blk MDBlock, addLink func(string) string) {
	colCount := len(blk.Headers)
	if colCount == 0 {
		return
	}
	totalW := 9026
	colW := totalW / colCount
	if colW < 800 {
		colW = 800
	}

	b.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="9026" w:type="dxa"/><w:tblBorders>`)
	b.WriteString(`<w:top w:val="single" w:sz="4" w:space="0" w:color="CBD5E1"/>`)
	b.WriteString(`<w:left w:val="single" w:sz="4" w:space="0" w:color="CBD5E1"/>`)
	b.WriteString(`<w:bottom w:val="single" w:sz="4" w:space="0" w:color="CBD5E1"/>`)
	b.WriteString(`<w:right w:val="single" w:sz="4" w:space="0" w:color="CBD5E1"/>`)
	b.WriteString(`<w:insideH w:val="single" w:sz="4" w:space="0" w:color="E2E8F0"/>`)
	b.WriteString(`<w:insideV w:val="single" w:sz="4" w:space="0" w:color="E2E8F0"/>`)
	b.WriteString(`</w:tblBorders><w:tblCellMar><w:top w:w="80" w:type="dxa"/><w:left w:w="120" w:type="dxa"/><w:bottom w:w="80" w:type="dxa"/><w:right w:w="120" w:type="dxa"/></w:tblCellMar></w:tblPr>`)
	b.WriteString(`<w:tblGrid>`)
	for i := 0; i < colCount; i++ {
		b.WriteString(fmt.Sprintf(`<w:gridCol w:w="%d"/>`, colW))
	}
	b.WriteString(`</w:tblGrid>`)

	// Header row with repeat-on-page-break (<w:tblHeader/>)
	b.WriteString(`<w:tr><w:trPr><w:tblHeader/><w:cantSplit/></w:trPr>`)
	for ci, h := range blk.Headers {
		jc := alignmentToWordJC(blk.Alignments, ci)
		b.WriteString(fmt.Sprintf(`<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/><w:shd w:val="clear" w:color="auto" w:fill="EEF2F7"/><w:vAlign w:val="center"/></w:tcPr>`, colW))
		b.WriteString(fmt.Sprintf(`<w:p><w:pPr><w:spacing w:before="0" w:after="0"/><w:jc w:val="%s"/></w:pPr>`, jc))
		spans := ParseInlineSpans(h)
		for idx := range spans {
			spans[idx].Bold = true
		}
		writeDocxRuns(b, spans, addLink)
		b.WriteString(`</w:p></w:tc>`)
	}
	b.WriteString(`</w:tr>`)

	// Body rows with subtle alternating stripe
	for ri, row := range blk.Rows {
		fill := ""
		if ri%2 == 1 {
			fill = `<w:shd w:val="clear" w:color="auto" w:fill="F8FAFC"/>`
		}
		b.WriteString(`<w:tr><w:trPr><w:cantSplit/></w:trPr>`)
		for ci := 0; ci < colCount; ci++ {
			cell := ""
			if ci < len(row) {
				cell = row[ci]
			}
			jc := alignmentToWordJC(blk.Alignments, ci)
			b.WriteString(fmt.Sprintf(`<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/>%s<w:vAlign w:val="center"/></w:tcPr>`, colW, fill))
			b.WriteString(fmt.Sprintf(`<w:p><w:pPr><w:spacing w:before="0" w:after="0"/><w:jc w:val="%s"/></w:pPr>`, jc))
			writeDocxRuns(b, ParseInlineSpans(cell), addLink)
			b.WriteString(`</w:p></w:tc>`)
		}
		b.WriteString(`</w:tr>`)
	}
	b.WriteString(`</w:tbl><w:p><w:pPr><w:spacing w:before="0" w:after="100"/></w:pPr></w:p>`)
}

func alignmentToWordJC(aligns []string, idx int) string {
	if idx < len(aligns) {
		switch aligns[idx] {
		case "center":
			return "center"
		case "right":
			return "right"
		}
	}
	return "left"
}

func decodeDiagramPNG(img DiagramImage) ([]byte, int, int, error) {
	raw := strings.TrimSpace(img.PNGBase64)
	if comma := strings.IndexByte(raw, ','); comma >= 0 && strings.HasPrefix(strings.ToLower(raw[:comma]), "data:") {
		raw = raw[comma+1:]
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, 0, 0, err
	}
	// Verify PNG signature and read IHDR dimensions when available
	if len(data) < 24 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		return nil, 0, 0, fmt.Errorf("invalid PNG image")
	}
	w := img.Width
	h := img.Height
	if w <= 0 || h <= 0 {
		w = int(binary.BigEndian.Uint32(data[16:20]))
		h = int(binary.BigEndian.Uint32(data[20:24]))
	}
	if w <= 0 {
		w = 800
	}
	if h <= 0 {
		h = 450
	}
	return data, w, h, nil
}

func writeDocxImageParagraph(b *bytes.Buffer, relID string, docPrID, pxW, pxH int, name string) {
	// 1 px at 96dpi = 9525 EMU; clamp to max width 15.5cm (5580000 EMU) and max height 20cm (7200000 EMU)
	// Diagrams are rasterized at 2x DPI, so treat logical width as pxW / 2 for crisp printing.
	const maxCx int64 = 5580000
	const maxCy int64 = 7200000
	cx := int64(pxW) * 9525 / 2
	cy := int64(pxH) * 9525 / 2
	if cx < 952500 {
		// Small diagrams still render at least readable size unless naturally smaller
		cx = int64(pxW) * 9525
		cy = int64(pxH) * 9525
	}
	if cx > maxCx && cx > 0 {
		cy = cy * maxCx / cx
		cx = maxCx
	}
	if cy > maxCy && cy > 0 {
		cx = cx * maxCy / cy
		cy = maxCy
	}
	if cx <= 0 {
		cx = 4000000
	}
	if cy <= 0 {
		cy = 2250000
	}

	b.WriteString(`<w:p><w:pPr><w:jc w:val="center"/><w:spacing w:before="120" w:after="160"/></w:pPr><w:r><w:drawing>`)
	b.WriteString(`<wp:inline distT="0" distB="0" distL="0" distR="0">`)
	b.WriteString(fmt.Sprintf(`<wp:extent cx="%d" cy="%d"/>`, cx, cy))
	b.WriteString(`<wp:effectExtent l="0" t="0" r="0" b="0"/>`)
	b.WriteString(fmt.Sprintf(`<wp:docPr id="%d" name="%s"/>`, docPrID, name))
	b.WriteString(`<wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr>`)
	b.WriteString(`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic>`)
	b.WriteString(fmt.Sprintf(`<pic:nvPicPr><pic:cNvPr id="%d" name="%s.png"/><pic:cNvPicPr/></pic:nvPicPr>`, docPrID, name))
	b.WriteString(fmt.Sprintf(`<pic:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>`, relID))
	b.WriteString(fmt.Sprintf(`<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr>`, cx, cy))
	b.WriteString(`</pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`)
}

func docxContentTypesXML(hasPNG bool) string {
	pngDefault := ""
	if hasPNG {
		pngDefault = `<Default Extension="png" ContentType="image/png"/>`
	}
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		pngDefault +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
		`<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>` +
		`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>` +
		`</Types>`
}

const docxRootRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
	`</Relationships>`

func docxDocumentRelsXML(rels []docxRel) string {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for _, r := range rels {
		b.WriteString(fmt.Sprintf(`<Relationship Id="%s" Type="%s" Target="`, r.ID, r.Type))
		_ = xml.EscapeText(&b, []byte(r.Target))
		b.WriteString(`"`)
		if r.TargetMode != "" {
			b.WriteString(fmt.Sprintf(` TargetMode="%s"`, r.TargetMode))
		}
		b.WriteString(`/>`)
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

func docxCorePropsXML(title string) string {
	var b bytes.Buffer
	now := time.Now().UTC().Format(time.RFC3339)
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">`)
	b.WriteString(`<dc:title>`)
	_ = xml.EscapeText(&b, []byte(title))
	b.WriteString(`</dc:title><dc:creator></dc:creator>`)
	b.WriteString(fmt.Sprintf(`<dcterms:created xsi:type="dcterms:W3CDTF">%s</dcterms:created>`, now))
	b.WriteString(fmt.Sprintf(`<dcterms:modified xsi:type="dcterms:W3CDTF">%s</dcterms:modified>`, now))
	b.WriteString(`</cp:coreProperties>`)
	return b.String()
}

const docxStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:docDefaults><w:rPrDefault><w:rPr>` +
	`<w:rFonts w:ascii="Microsoft YaHei" w:hAnsi="Microsoft YaHei" w:eastAsia="Microsoft YaHei" w:cs="Microsoft YaHei"/>` +
	`<w:color w:val="1E293B"/><w:sz w:val="21"/><w:szCs w:val="21"/>` +
	`</w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="324" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>` +
	`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/>` +
	`<w:pPr><w:keepNext/><w:spacing w:before="280" w:after="140"/><w:pBdr><w:bottom w:val="single" w:sz="6" w:space="4" w:color="E2E8F0"/></w:pBdr><w:outlineLvl w:val="0"/></w:pPr>` +
	`<w:rPr><w:b/><w:color w:val="0F172A"/><w:sz w:val="34"/><w:szCs w:val="34"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/>` +
	`<w:pPr><w:keepNext/><w:spacing w:before="240" w:after="100"/><w:outlineLvl w:val="1"/></w:pPr>` +
	`<w:rPr><w:b/><w:color w:val="1E293B"/><w:sz w:val="28"/><w:szCs w:val="28"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/>` +
	`<w:pPr><w:keepNext/><w:spacing w:before="180" w:after="80"/><w:outlineLvl w:val="2"/></w:pPr>` +
	`<w:rPr><w:b/><w:color w:val="334155"/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading4"><w:name w:val="heading 4"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/>` +
	`<w:pPr><w:keepNext/><w:spacing w:before="140" w:after="60"/><w:outlineLvl w:val="3"/></w:pPr>` +
	`<w:rPr><w:b/><w:color w:val="475569"/><w:sz w:val="22"/><w:szCs w:val="22"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/>` +
	`<w:pPr><w:spacing w:before="0" w:after="70" w:line="300" w:lineRule="auto"/></w:pPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="Normal"/>` +
	`<w:pPr><w:ind w:left="260" w:right="120"/><w:spacing w:before="80" w:after="120"/><w:pBdr><w:left w:val="single" w:sz="18" w:space="10" w:color="3B82F6"/></w:pBdr><w:shd w:val="clear" w:color="auto" w:fill="F8FAFC"/></w:pPr>` +
	`<w:rPr><w:color w:val="475569"/><w:sz w:val="20"/><w:szCs w:val="20"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="CodeBlock"><w:name w:val="Code Block"/><w:basedOn w:val="Normal"/>` +
	`<w:pPr><w:spacing w:before="0" w:after="0" w:line="260" w:lineRule="auto"/></w:pPr>` +
	`<w:rPr><w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:eastAsia="Microsoft YaHei"/><w:color w:val="1E293B"/><w:sz w:val="18"/><w:szCs w:val="18"/></w:rPr></w:style>` +
	`</w:styles>`

const docxNumberingXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:abstractNum w:abstractNumId="0">` +
	`<w:multiLevelType w:val="hybridMultilevel"/>` +
	`<w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="•"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="420" w:hanging="240"/></w:pPr></w:lvl>` +
	`<w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="◦"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="780" w:hanging="240"/></w:pPr></w:lvl>` +
	`<w:lvl w:ilvl="2"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="▪"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="1140" w:hanging="240"/></w:pPr></w:lvl>` +
	`<w:lvl w:ilvl="3"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="•"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="1500" w:hanging="240"/></w:pPr></w:lvl>` +
	`<w:lvl w:ilvl="4"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="◦"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="1860" w:hanging="240"/></w:pPr></w:lvl>` +
	`</w:abstractNum>` +
	`<w:abstractNum w:abstractNumId="1">` +
	`<w:multiLevelType w:val="hybridMultilevel"/>` +
	`<w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="420" w:hanging="240"/></w:pPr></w:lvl>` +
	`<w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="lowerLetter"/><w:lvlText w:val="%2."/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="780" w:hanging="240"/></w:pPr></w:lvl>` +
	`<w:lvl w:ilvl="2"><w:start w:val="1"/><w:numFmt w:val="lowerRoman"/><w:lvlText w:val="%3."/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="1140" w:hanging="240"/></w:pPr></w:lvl>` +
	`<w:lvl w:ilvl="3"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%4."/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="1500" w:hanging="240"/></w:pPr></w:lvl>` +
	`<w:lvl w:ilvl="4"><w:start w:val="1"/><w:numFmt w:val="lowerLetter"/><w:lvlText w:val="%5."/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="1860" w:hanging="240"/></w:pPr></w:lvl>` +
	`</w:abstractNum>` +
	`<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>` +
	`<w:num w:numId="2"><w:abstractNumId w:val="1"/></w:num>` +
	`</w:numbering>`
