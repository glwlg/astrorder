package office

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// PptxExportRequest is accepted by POST /api/v1/office/export/pptx.
type PptxExportRequest struct {
	Title    string         `json:"title"`
	Subtitle string         `json:"subtitle"`
	Markdown string         `json:"markdown"`
	Diagrams []DiagramImage `json:"diagrams"`
	FileName string         `json:"file_name"`
}

type slideSpec struct {
	IsCover  bool
	Title    string
	Subtitle string
	Bullets  []slideBullet
	Table    *MDBlock
	Code     string
	CodeLang string
	ImagePNG []byte
	ImageW   int
	ImageH   int
	Notes    string
}

type slideBullet struct {
	Level int
	Text  string
	Bold  bool
}

type pptxMediaPart struct {
	ZipPath string
	Data    []byte
}

// ExportMarkdownToPptx converts Markdown (+ optional rasterized Mermaid PNGs)
// into a clean 16:9 OOXML PowerPoint (.pptx) presentation with zero app branding.
func ExportMarkdownToPptx(req PptxExportRequest) ([]byte, error) {
	blocks := ParseMarkdownBlocks(req.Markdown)
	diagramMap := make(map[int]DiagramImage, len(req.Diagrams))
	for _, d := range req.Diagrams {
		if strings.TrimSpace(d.PNGBase64) != "" {
			diagramMap[d.Index] = d
		}
	}

	slides := buildSlidesFromBlocks(req, blocks, diagramMap)
	if len(slides) == 0 {
		slides = []slideSpec{{IsCover: true, Title: "演示文稿"}}
	}

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	writePart := func(p string, data []byte) error {
		w, err := zw.Create(p)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}

	mediaParts := make([]pptxMediaPart, 0)
	slideXMLs := make([][]byte, len(slides))
	slideRelsXMLs := make([][]byte, len(slides))

	for i, s := range slides {
		imgRelID := ""
		imgTarget := ""
		if len(s.ImagePNG) > 0 {
			mediaIdx := len(mediaParts) + 1
			mediaName := fmt.Sprintf("diagram_%d.png", mediaIdx)
			mediaParts = append(mediaParts, pptxMediaPart{
				ZipPath: "ppt/media/" + mediaName,
				Data:    s.ImagePNG,
			})
			imgRelID = "rId2"
			imgTarget = "../media/" + mediaName
		}
		slideXMLs[i] = renderSlideXML(s, i+1, len(slides), imgRelID)
		slideRelsXMLs[i] = renderSlideRelsXML(imgRelID, imgTarget)
	}

	title := strings.TrimSpace(SanitizeXMLText(req.Title))
	if title == "" && len(slides) > 0 {
		title = slides[0].Title
	}

	if err := writePart("[Content_Types].xml", []byte(pptxContentTypesXML(len(slides), len(mediaParts) > 0))); err != nil {
		return nil, err
	}
	if err := writePart("_rels/.rels", []byte(pptxRootRelsXML)); err != nil {
		return nil, err
	}
	if err := writePart("docProps/core.xml", []byte(pptxCorePropsXML(title))); err != nil {
		return nil, err
	}
	if err := writePart("ppt/presentation.xml", []byte(pptxPresentationXML(len(slides)))); err != nil {
		return nil, err
	}
	if err := writePart("ppt/_rels/presentation.xml.rels", []byte(pptxPresentationRelsXML(len(slides)))); err != nil {
		return nil, err
	}
	if err := writePart("ppt/theme/theme1.xml", []byte(pptxThemeXML)); err != nil {
		return nil, err
	}
	if err := writePart("ppt/slideMasters/slideMaster1.xml", []byte(pptxSlideMasterXML)); err != nil {
		return nil, err
	}
	if err := writePart("ppt/slideMasters/_rels/slideMaster1.xml.rels", []byte(pptxSlideMasterRelsXML)); err != nil {
		return nil, err
	}
	if err := writePart("ppt/slideLayouts/slideLayout1.xml", []byte(pptxSlideLayoutXML)); err != nil {
		return nil, err
	}
	if err := writePart("ppt/slideLayouts/_rels/slideLayout1.xml.rels", []byte(pptxSlideLayoutRelsXML)); err != nil {
		return nil, err
	}
	for i := range slides {
		if err := writePart(fmt.Sprintf("ppt/slides/slide%d.xml", i+1), slideXMLs[i]); err != nil {
			return nil, err
		}
		if err := writePart(fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", i+1), slideRelsXMLs[i]); err != nil {
			return nil, err
		}
	}
	for _, mp := range mediaParts {
		if err := writePart(mp.ZipPath, mp.Data); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return zipBuf.Bytes(), nil
}

func buildSlidesFromBlocks(req PptxExportRequest, blocks []MDBlock, diagramMap map[int]DiagramImage) []slideSpec {
	coverTitle := strings.TrimSpace(SanitizeXMLText(req.Title))
	coverSub := strings.TrimSpace(SanitizeXMLText(req.Subtitle))
	startIdx := 0

	if len(blocks) > 0 && blocks[0].Kind == "heading" && blocks[0].Level == 1 {
		h1 := strings.TrimSpace(SanitizeXMLText(PlainTextFromSpans(blocks[0].Spans)))
		if h1 != "" {
			coverTitle = h1
			startIdx = 1
		}
	}
	if coverTitle == "" {
		coverTitle = "工作汇报"
	}

	slides := []slideSpec{
		{
			IsCover:  true,
			Title:    coverTitle,
			Subtitle: coverSub,
		},
	}

	const maxBulletsPerSlide = 7
	var cur *slideSpec
	flushCur := func() {
		if cur == nil {
			return
		}
		if len(cur.Bullets) > 0 || cur.Table != nil || len(cur.ImagePNG) > 0 || cur.Code != "" {
			slides = append(slides, *cur)
		}
		cur = nil
	}
	ensureCur := func(defaultTitle string) {
		if cur == nil {
			t := defaultTitle
			if t == "" {
				t = coverTitle
			}
			cur = &slideSpec{Title: t}
		}
	}

	lastSectionTitle := coverTitle

	for i := startIdx; i < len(blocks); i++ {
		blk := blocks[i]
		switch blk.Kind {
		case "hr":
			flushCur()

		case "heading":
			hText := strings.TrimSpace(SanitizeXMLText(PlainTextFromSpans(blk.Spans)))
			if hText == "" {
				continue
			}
			if blk.Level <= 2 {
				flushCur()
				lastSectionTitle = hText
				cur = &slideSpec{Title: hText}
			} else {
				ensureCur(lastSectionTitle)
				if len(cur.Bullets) >= maxBulletsPerSlide {
					flushCur()
					cur = &slideSpec{Title: lastSectionTitle}
				}
				cur.Bullets = append(cur.Bullets, slideBullet{Level: 0, Text: hText, Bold: true})
			}

		case "paragraph", "blockquote":
			text := strings.TrimSpace(SanitizeXMLText(PlainTextFromSpans(blk.Spans)))
			if text == "" {
				continue
			}
			ensureCur(lastSectionTitle)
			if len(cur.Bullets) >= maxBulletsPerSlide {
				flushCur()
				cur = &slideSpec{Title: lastSectionTitle + "（续）"}
			}
			cur.Bullets = append(cur.Bullets, slideBullet{Level: 0, Text: text})

		case "list_item":
			text := strings.TrimSpace(SanitizeXMLText(PlainTextFromSpans(blk.Spans)))
			if text == "" {
				continue
			}
			ensureCur(lastSectionTitle)
			if len(cur.Bullets) >= maxBulletsPerSlide {
				flushCur()
				cur = &slideSpec{Title: lastSectionTitle + "（续）"}
			}
			lvl := blk.Level
			if lvl > 2 {
				lvl = 2
			}
			cur.Bullets = append(cur.Bullets, slideBullet{Level: lvl, Text: text})

		case "table":
			if len(blk.Headers) == 0 {
				continue
			}
			tblCopy := blk
			// Put tables cleanly on their own slide when current slide already has content
			if cur != nil && (len(cur.Bullets) > 2 || cur.Table != nil || len(cur.ImagePNG) > 0) {
				flushCur()
			}
			ensureCur(lastSectionTitle)
			cur.Table = &tblCopy
			flushCur()

		case "mermaid":
			if img, ok := diagramMap[blk.DiagramIndex]; ok {
				if pngBytes, w, h, err := decodeDiagramPNG(img); err == nil {
					if cur != nil && (len(cur.Bullets) > 0 || cur.Table != nil || len(cur.ImagePNG) > 0) {
						flushCur()
					}
					ensureCur(lastSectionTitle)
					cur.ImagePNG = pngBytes
					cur.ImageW = w
					cur.ImageH = h
					flushCur()
					continue
				}
			}
			// Fallback to code slide
			if cur != nil && len(cur.Bullets) > 0 {
				flushCur()
			}
			ensureCur(lastSectionTitle)
			cur.Code = strings.TrimSpace(SanitizeXMLText(blk.Code))
			cur.CodeLang = "mermaid"
			flushCur()

		case "code":
			code := strings.TrimSpace(SanitizeXMLText(blk.Code))
			if code == "" {
				continue
			}
			if cur != nil && (len(cur.Bullets) > 1 || cur.Code != "") {
				flushCur()
			}
			ensureCur(lastSectionTitle)
			cur.Code = code
			cur.CodeLang = blk.Language
			flushCur()
		}
	}
	flushCur()

	return slides
}

// Slide dimensions (16:9): cx=12192000 (13.333 in), cy=6858000 (7.5 in)
func renderSlideXML(s slideSpec, slideNum, totalSlides int, imgRelID string) []byte {
	var shapes bytes.Buffer
	nextID := 2

	if s.IsCover {
		// Top subtle accent bar
		shapes.WriteString(pptxRectShape(nextID, "AccentBar", 0, 0, 12192000, 180000, "1D4ED8"))
		nextID++
		// Left vertical accent stripe
		shapes.WriteString(pptxRectShape(nextID, "LeftStripe", 860000, 2150000, 90000, 2200000, "2563EB"))
		nextID++
		// Main cover title
		shapes.WriteString(pptxTextShape(nextID, "CoverTitle", 1180000, 2120000, 10000000, 1600000, []pptxPara{
			{Text: s.Title, SizePt: 38, Bold: true, Color: "0F172A"},
		}))
		nextID++
		if strings.TrimSpace(s.Subtitle) != "" {
			shapes.WriteString(pptxTextShape(nextID, "CoverSubtitle", 1180000, 3820000, 9600000, 700000, []pptxPara{
				{Text: s.Subtitle, SizePt: 18, Color: "475569"},
			}))
			nextID++
		}
	} else {
		// Subtle top bar + slide header title + divider line
		shapes.WriteString(pptxRectShape(nextID, "TopStrip", 0, 0, 12192000, 75000, "2563EB"))
		nextID++
		shapes.WriteString(pptxTextShape(nextID, "SlideTitle", 680000, 320000, 10800000, 720000, []pptxPara{
			{Text: s.Title, SizePt: 26, Bold: true, Color: "0F172A"},
		}))
		nextID++
		shapes.WriteString(pptxRectShape(nextID, "HeaderRule", 680000, 1080000, 10832000, 18000, "E2E8F0"))
		nextID++

		contentTop := int64(1260000)
		contentHeight := int64(5040000)

		if len(s.Bullets) > 0 && s.Table == nil && imgRelID == "" && s.Code == "" {
			paras := make([]pptxPara, 0, len(s.Bullets))
			for _, bl := range s.Bullets {
				bulletPrefix := "•  "
				if bl.Level > 0 {
					bulletPrefix = "◦  "
				}
				if bl.Bold {
					bulletPrefix = ""
				}
				paras = append(paras, pptxPara{
					Text:    bulletPrefix + bl.Text,
					SizePt:  bulletFontSize(len(s.Bullets), bl.Level),
					Bold:    bl.Bold,
					Color:   "1E293B",
					IndentL: int64(bl.Level) * 320000,
					SpaceA:  10,
				})
			}
			shapes.WriteString(pptxTextShape(nextID, "SlideBody", 720000, contentTop, 10750000, contentHeight, paras))
			nextID++
		}

		if s.Table != nil {
			tableTop := contentTop
			if len(s.Bullets) > 0 {
				leadParas := make([]pptxPara, 0, len(s.Bullets))
				for _, bl := range s.Bullets {
					leadParas = append(leadParas, pptxPara{Text: "•  " + bl.Text, SizePt: 15, Color: "334155", SpaceA: 6})
				}
				shapes.WriteString(pptxTextShape(nextID, "TableIntro", 720000, contentTop, 10750000, 1050000, leadParas))
				nextID++
				tableTop += 1120000
				contentHeight -= 1120000
			}
			shapes.WriteString(pptxTableGraphicFrame(nextID, "SlideTable", 680000, tableTop, 10832000, contentHeight, *s.Table))
			nextID++
		}

		if imgRelID != "" {
			shapes.WriteString(pptxPictureShape(nextID, "Diagram", imgRelID, 680000, contentTop, 10832000, contentHeight, s.ImageW, s.ImageH))
			nextID++
		}

		if s.Code != "" {
			lines := strings.Split(s.Code, "\n")
			if len(lines) > 18 {
				lines = append(lines[:18], "…")
			}
			shapes.WriteString(pptxRectShape(nextID, "CodeBg", 680000, contentTop, 10832000, contentHeight, "F8FAFC"))
			nextID++
			paras := make([]pptxPara, 0, len(lines))
			for _, ln := range lines {
				paras = append(paras, pptxPara{
					Text:   ln,
					SizePt: 12,
					Color:  "1E293B",
					Font:   "Consolas",
					SpaceA: 2,
				})
			}
			shapes.WriteString(pptxTextShape(nextID, "CodeText", 860000, contentTop+140000, 10472000, contentHeight-280000, paras))
			nextID++
		}

		// Clean subtle slide number at bottom-right (zero branding)
		shapes.WriteString(pptxTextShape(nextID, "SlideNumber", 10700000, 6420000, 900000, 320000, []pptxPara{
			{Text: fmt.Sprintf("%d / %d", slideNum, totalSlides), SizePt: 11, Color: "94A3B8", Align: "r"},
		}))
	}

	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
		`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">` +
		`<p:cSld><p:bg><p:bgPr><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill><a:effectLst/></p:bgPr></p:bg>` +
		`<p:spTree>` +
		`<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
		`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>` +
		shapes.String() +
		`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`)
}

func bulletFontSize(count, level int) int {
	base := 18
	if count >= 6 {
		base = 16
	}
	if level > 0 {
		base -= 2
	}
	return base
}

type pptxPara struct {
	Text    string
	SizePt  int
	Bold    bool
	Color   string
	Font    string
	Align   string
	IndentL int64
	SpaceA  int
}

func pptxRectShape(id int, name string, x, y, cx, cy int64, fillHex string) string {
	return fmt.Sprintf(
		`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr>`+
			`<p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`+
			`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:ln><a:noFill/></a:ln></p:spPr>`+
			`<p:txBody><a:bodyPr/><a:lstStyle/><a:p/></p:txBody></p:sp>`,
		id, name, x, y, cx, cy, fillHex,
	)
}

func pptxTextShape(id int, name string, x, y, cx, cy int64, paras []pptxPara) string {
	var b bytes.Buffer
	b.WriteString(fmt.Sprintf(
		`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>`+
			`<p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`+
			`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/></p:spPr>`+
			`<p:txBody><a:bodyPr wrap="square" lIns="60000" tIns="40000" rIns="60000" bIns="40000"/><a:lstStyle/>`,
		id, name, x, y, cx, cy,
	))
	for _, p := range paras {
		algn := p.Align
		if algn == "" {
			algn = "l"
		}
		marAttr := ""
		if p.IndentL > 0 {
			marAttr = fmt.Sprintf(` marL="%d"`, p.IndentL)
		}
		b.WriteString(fmt.Sprintf(`<a:p><a:pPr algn="%s"%s>`, algn, marAttr))
		if p.SpaceA > 0 {
			b.WriteString(fmt.Sprintf(`<a:spcAft><a:spcPts val="%d"/></a:spcAft>`, p.SpaceA*100))
		}
		b.WriteString(`</a:pPr>`)
		font := p.Font
		if font == "" {
			font = "Microsoft YaHei"
		}
		color := p.Color
		if color == "" {
			color = "1E293B"
		}
		boldAttr := ""
		if p.Bold {
			boldAttr = ` b="1"`
		}
		sz := p.SizePt * 100
		if sz <= 0 {
			sz = 1800
		}
		b.WriteString(fmt.Sprintf(`<a:r><a:rPr lang="zh-CN" sz="%d"%s dirty="0"><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:latin typeface="%s"/><a:ea typeface="Microsoft YaHei"/></a:rPr><a:t>`, sz, boldAttr, color, font))
		_ = xml.EscapeText(&b, []byte(SanitizeXMLText(p.Text)))
		b.WriteString(`</a:t></a:r></a:p>`)
	}
	b.WriteString(`</p:txBody></p:sp>`)
	return b.String()
}

func pptxTableGraphicFrame(id int, name string, x, y, cx, cy int64, blk MDBlock) string {
	cols := len(blk.Headers)
	if cols == 0 {
		return ""
	}
	colW := cx / int64(cols)
	maxRows := len(blk.Rows)
	if maxRows > 10 {
		maxRows = 10
	}
	rowH := int64(420000)
	if int64(maxRows+1)*rowH > cy {
		rowH = cy / int64(maxRows+1)
		if rowH < 280000 {
			rowH = 280000
		}
	}

	var b bytes.Buffer
	b.WriteString(fmt.Sprintf(
		`<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="%d" name="%s"/><p:cNvGraphicFramePr><a:graphicFrameLocks noGrp="1"/></p:cNvGraphicFramePr><p:nvPr/></p:nvGraphicFramePr>`+
			`<p:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></p:xfrm>`+
			`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table"><a:tbl><a:tblPr firstRow="1" bandRow="1"/><a:tblGrid>`,
		id, name, x, y, cx, int64(maxRows+1)*rowH,
	))
	for i := 0; i < cols; i++ {
		b.WriteString(fmt.Sprintf(`<a:gridCol w="%d"/>`, colW))
	}
	b.WriteString(`</a:tblGrid>`)

	// Header row
	b.WriteString(fmt.Sprintf(`<a:tr h="%d">`, rowH))
	for _, h := range blk.Headers {
		writePptxTableCell(&b, PlainTextFromSpans(ParseInlineSpans(h)), true, "EEF2F7")
	}
	b.WriteString(`</a:tr>`)

	// Body rows
	for ri := 0; ri < maxRows; ri++ {
		fill := "FFFFFF"
		if ri%2 == 1 {
			fill = "F8FAFC"
		}
		b.WriteString(fmt.Sprintf(`<a:tr h="%d">`, rowH))
		row := blk.Rows[ri]
		for ci := 0; ci < cols; ci++ {
			cell := ""
			if ci < len(row) {
				cell = PlainTextFromSpans(ParseInlineSpans(row[ci]))
			}
			writePptxTableCell(&b, cell, false, fill)
		}
		b.WriteString(`</a:tr>`)
	}
	b.WriteString(`</a:tbl></a:graphicData></a:graphic></p:graphicFrame>`)
	return b.String()
}

func writePptxTableCell(b *bytes.Buffer, text string, isHeader bool, fillHex string) {
	bold := ""
	sz := 1300
	color := "1E293B"
	if isHeader {
		bold = ` b="1"`
		sz = 1400
		color = "0F172A"
	}
	b.WriteString(`<a:tc><a:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="zh-CN" sz="`)
	b.WriteString(fmt.Sprintf(`%d"%s dirty="0"><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:latin typeface="Microsoft YaHei"/><a:ea typeface="Microsoft YaHei"/></a:rPr><a:t>`, sz, bold, color))
	_ = xml.EscapeText(b, []byte(SanitizeXMLText(text)))
	b.WriteString(`</a:t></a:r></a:p></a:txBody><a:tcPr marL="90000" marR="90000" marT="50000" marB="50000" anchor="ctr">`)
	b.WriteString(`<a:lnL w="6350"><a:solidFill><a:srgbClr val="CBD5E1"/></a:solidFill></a:lnL>`)
	b.WriteString(`<a:lnR w="6350"><a:solidFill><a:srgbClr val="CBD5E1"/></a:solidFill></a:lnR>`)
	b.WriteString(`<a:lnT w="6350"><a:solidFill><a:srgbClr val="CBD5E1"/></a:solidFill></a:lnT>`)
	b.WriteString(`<a:lnB w="6350"><a:solidFill><a:srgbClr val="CBD5E1"/></a:solidFill></a:lnB>`)
	b.WriteString(fmt.Sprintf(`<a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:tcPr></a:tc>`, fillHex))
}

func pptxPictureShape(id int, name, relID string, boxX, boxY, boxW, boxH int64, pxW, pxH int) string {
	if pxW <= 0 {
		pxW = 800
	}
	if pxH <= 0 {
		pxH = 450
	}
	// Fit proportionally inside (boxW, boxH) and center
	cx := boxW
	cy := cx * int64(pxH) / int64(pxW)
	if cy > boxH {
		cy = boxH
		cx = cy * int64(pxW) / int64(pxH)
	}
	offX := boxX + (boxW-cx)/2
	offY := boxY + (boxH-cy)/2

	return fmt.Sprintf(
		`<p:pic><p:nvPicPr><p:cNvPr id="%d" name="%s"/><p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr><p:nvPr/></p:nvPicPr>`+
			`<p:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></p:blipFill>`+
			`<p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr></p:pic>`,
		id, name, relID, offX, offY, cx, cy,
	)
}

func renderSlideRelsXML(imgRelID, imgTarget string) []byte {
	extra := ""
	if imgRelID != "" && imgTarget != "" {
		extra = fmt.Sprintf(`<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="%s"/>`, imgRelID, imgTarget)
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>` +
		extra +
		`</Relationships>`)
}

func pptxContentTypesXML(slideCount int, hasPNG bool) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	if hasPNG {
		b.WriteString(`<Default Extension="png" ContentType="image/png"/>`)
	}
	b.WriteString(`<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>`)
	b.WriteString(`<Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/>`)
	b.WriteString(`<Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/>`)
	b.WriteString(`<Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>`)
	b.WriteString(`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>`)
	for i := 1; i <= slideCount; i++ {
		b.WriteString(fmt.Sprintf(`<Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, i))
	}
	b.WriteString(`</Types>`)
	return b.String()
}

const pptxRootRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
	`</Relationships>`

func pptxCorePropsXML(title string) string {
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

func pptxPresentationXML(slideCount int) string {
	var sldIds strings.Builder
	for i := 1; i <= slideCount; i++ {
		sldIds.WriteString(fmt.Sprintf(`<p:sldId id="%d" r:id="rId%d"/>`, 255+i, i+1))
	}
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
		`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">` +
		`<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>` +
		`<p:sldIdLst>` + sldIds.String() + `</p:sldIdLst>` +
		`<p:sldSz cx="12192000" cy="6858000"/>` +
		`<p:notesSz cx="6858000" cy="9144000"/>` +
		`</p:presentation>`
}

func pptxPresentationRelsXML(slideCount int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	b.WriteString(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/>`)
	for i := 1; i <= slideCount; i++ {
		b.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, i+1, i))
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

const pptxSlideMasterXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
	`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
	`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">` +
	`<p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
	`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>` +
	`</p:spTree></p:cSld>` +
	`<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>` +
	`<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst>` +
	`</p:sldMaster>`

const pptxSlideMasterRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/>` +
	`</Relationships>`

const pptxSlideLayoutXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
	`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
	`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="blank" preserve="1">` +
	`<p:cSld name="Blank"><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
	`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>` +
	`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`

const pptxSlideLayoutRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/>` +
	`</Relationships>`

const pptxThemeXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="CleanOffice">` +
	`<a:themeElements><a:clrScheme name="CleanOffice">` +
	`<a:dk1><a:srgbClr val="0F172A"/></a:dk1><a:lt1><a:srgbClr val="FFFFFF"/></a:lt1>` +
	`<a:dk2><a:srgbClr val="334155"/></a:dk2><a:lt2><a:srgbClr val="F8FAFC"/></a:lt2>` +
	`<a:accent1><a:srgbClr val="2563EB"/></a:accent1><a:accent2><a:srgbClr val="0F766E"/></a:accent2>` +
	`<a:accent3><a:srgbClr val="4F46E5"/></a:accent3><a:accent4><a:srgbClr val="D97706"/></a:accent4>` +
	`<a:accent5><a:srgbClr val="0284C7"/></a:accent5><a:accent6><a:srgbClr val="059669"/></a:accent6>` +
	`<a:hlink><a:srgbClr val="2563EB"/></a:hlink><a:folHlink><a:srgbClr val="7C3AED"/></a:folHlink>` +
	`</a:clrScheme><a:fontScheme name="CleanOffice">` +
	`<a:majorFont><a:latin typeface="Microsoft YaHei"/><a:ea typeface="Microsoft YaHei"/><a:cs typeface="Microsoft YaHei"/></a:majorFont>` +
	`<a:minorFont><a:latin typeface="Microsoft YaHei"/><a:ea typeface="Microsoft YaHei"/><a:cs typeface="Microsoft YaHei"/></a:minorFont>` +
	`</a:fontScheme><a:fmtScheme name="CleanOffice">` +
	`<a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst>` +
	`<a:lnStyleLst><a:ln w="9525"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="19050"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="25400"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst>` +
	`<a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst>` +
	`<a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst>` +
	`</a:fmtScheme></a:themeElements></a:theme>`
