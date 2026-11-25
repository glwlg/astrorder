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
	"sort"
	"strconv"
	"strings"
)

const maxPptxBytes = 48 << 20 // 48 MiB cap

type PptxParagraph struct {
	Text  string `json:"text"`
	Level int    `json:"level,omitempty"`
	Bold  bool   `json:"bold,omitempty"`
}

type PptxTable struct {
	Rows [][]string `json:"rows"`
}

type PptxSlidePreview struct {
	Index      int             `json:"index"` // 1-based
	Title      string          `json:"title"`
	Paragraphs []PptxParagraph `json:"paragraphs"`
	Tables     []PptxTable     `json:"tables,omitempty"`
	Images     []string        `json:"images,omitempty"`
	Notes      string          `json:"notes,omitempty"`
}

type PptxStats struct {
	SlideCount int `json:"slide_count"`
	Words      int `json:"words"`
	Tables     int `json:"tables"`
	Images     int `json:"images"`
}

type PptxPreviewResponse struct {
	Path       string             `json:"path"`
	Name       string             `json:"name"`
	Title      string             `json:"title"`
	SlideCount int                `json:"slide_count"`
	Slides     []PptxSlidePreview `json:"slides"`
	Stats      PptxStats          `json:"stats"`
}

// ParsePptxBytes parses an OOXML .pptx binary into structured slides, tables,
// embedded images (within memory budget), and speaker notes.
func ParsePptxBytes(name, filePath string, data []byte) (*PptxPreviewResponse, error) {
	if len(data) == 0 {
		return nil, errors.New("PPT 文档为空")
	}
	if len(data) > maxPptxBytes {
		return nil, fmt.Errorf("PPT 文档超过预览上限 (%d MB)", maxPptxBytes>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("无法解析 .pptx 压缩包: %w", err)
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
			return nil, errors.New("zip entry exceeds safety limit")
		}
		return b, nil
	}

	slidePaths := resolvePptxSlideOrder(files, readEntry)
	if len(slidePaths) == 0 {
		return nil, errors.New("PPT 中未找到任何幻灯片")
	}

	imageBudgetUsed := 0
	slides := make([]PptxSlidePreview, 0, len(slidePaths))
	var stats PptxStats
	docTitle := ""
	if coreBytes, err := readEntry("docProps/core.xml"); err == nil {
		docTitle = parseCoreTitle(coreBytes)
	}

	for idx, sp := range slidePaths {
		slideBytes, err := readEntry(sp)
		if err != nil {
			continue
		}
		// Read slide relationships for images and notesSlide
		relsPath := path.Join(path.Dir(sp), "_rels", path.Base(sp)+".rels")
		rels := map[string]docxRelInfo{}
		if rb, err := readEntry(relsPath); err == nil {
			rels = parsePptxRels(rb)
		}

		slide := parseSinglePptxSlide(idx+1, slideBytes, sp, rels, readEntry, &imageBudgetUsed)
		// Parse linked notes slide if present
		for _, r := range rels {
			if strings.Contains(strings.ToLower(r.Type), "notesslide") {
				notesTarget := resolveRelativeZipPath(path.Dir(sp), r.Target)
				if nb, err := readEntry(notesTarget); err == nil {
					slide.Notes = extractPlainPptxText(nb)
				}
				break
			}
		}

		if docTitle == "" && slide.Title != "" {
			docTitle = slide.Title
		}
		stats.SlideCount++
		stats.Tables += len(slide.Tables)
		stats.Images += len(slide.Images)
		stats.Words += countMixedWords(slide.Title)
		for _, p := range slide.Paragraphs {
			stats.Words += countMixedWords(p.Text)
		}
		slides = append(slides, slide)
	}

	if docTitle == "" {
		docTitle = strings.TrimSuffix(name, path.Ext(name))
	}

	return &PptxPreviewResponse{
		Path:       filePath,
		Name:       name,
		Title:      docTitle,
		SlideCount: len(slides),
		Slides:     slides,
		Stats:      stats,
	}, nil
}

func resolvePptxSlideOrder(files map[string]*zip.File, readEntry func(string) ([]byte, error)) []string {
	presBytes, err := readEntry("ppt/presentation.xml")
	if err == nil {
		relsMap := map[string]string{}
		if rb, err := readEntry("ppt/_rels/presentation.xml.rels"); err == nil {
			relsMap = parseSimpleRels(rb)
		}
		dec := xml.NewDecoder(bytes.NewReader(presBytes))
		var ordered []string
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sldId" {
				relID := xmlRelIDAttr(se.Attr)
				if target := relsMap[relID]; target != "" {
					ordered = append(ordered, resolveRelativeZipPath("ppt", target))
				}
			}
		}
		if len(ordered) > 0 {
			return ordered
		}
	}

	// Fallback: sort ppt/slides/slideN.xml numerically
	type numbered struct {
		num  int
		path string
	}
	var fallback []numbered
	for k := range files {
		if strings.HasPrefix(k, "ppt/slides/slide") && strings.HasSuffix(k, ".xml") {
			base := strings.TrimSuffix(strings.TrimPrefix(k, "ppt/slides/slide"), ".xml")
			n, _ := strconv.Atoi(base)
			fallback = append(fallback, numbered{num: n, path: k})
		}
	}
	sort.Slice(fallback, func(i, j int) bool { return fallback[i].num < fallback[j].num })
	out := make([]string, 0, len(fallback))
	for _, item := range fallback {
		out = append(out, item.path)
	}
	return out
}

func xmlRelIDAttr(attrs []xml.Attr) string {
	fallback := ""
	for _, a := range attrs {
		if a.Name.Local != "id" {
			continue
		}
		if strings.Contains(a.Name.Space, "relationships") {
			return a.Value
		}
		if strings.HasPrefix(a.Value, "rId") {
			fallback = a.Value
		}
	}
	return fallback
}

func resolveRelativeZipPath(baseDir, target string) string {
	clean := strings.ReplaceAll(target, "\\", "/")
	if strings.HasPrefix(clean, "/") {
		return strings.TrimPrefix(path.Clean(clean), "/")
	}
	return strings.TrimPrefix(path.Clean(path.Join(baseDir, clean)), "/")
}

func parsePptxRels(data []byte) map[string]docxRelInfo {
	var parsed struct {
		Rels []struct {
			ID         string `xml:"Id,attr"`
			Type       string `xml:"Type,attr"`
			Target     string `xml:"Target,attr"`
			TargetMode string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	out := make(map[string]docxRelInfo)
	if xml.Unmarshal(data, &parsed) != nil {
		return out
	}
	for _, r := range parsed.Rels {
		out[r.ID] = docxRelInfo{Type: r.Type, Target: r.Target, TargetMode: r.TargetMode}
	}
	return out
}

func parseSinglePptxSlide(
	index int,
	data []byte,
	slidePath string,
	rels map[string]docxRelInfo,
	readEntry func(string) ([]byte, error),
	imageBudgetUsed *int,
) PptxSlidePreview {
	slide := PptxSlidePreview{
		Index:      index,
		Paragraphs: make([]PptxParagraph, 0),
	}

	dec := xml.NewDecoder(bytes.NewReader(data))
	var (
		inTable     bool
		inShape     bool
		shapeIsTitle bool
		curTable    [][]string
		curRow      []string
		inCell      bool
		cellBuf     strings.Builder
		curParaBuf  strings.Builder
		curParaLvl  int
		curParaBold bool
		inPara      bool
	)

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "sp":
				inShape = true
				shapeIsTitle = false
			case "ph":
				if inShape {
					phType := strings.ToLower(xmlAttr(t.Attr, "type"))
					if phType == "title" || phType == "ctrtitle" {
						shapeIsTitle = true
					}
				}
			case "cNvPr":
				if inShape {
					nm := strings.ToLower(xmlAttr(t.Attr, "name"))
					if strings.Contains(nm, "title") && !strings.Contains(nm, "subtitle") {
						shapeIsTitle = true
					}
				}
			case "tbl":
				inTable = true
				curTable = nil
			case "tr":
				if inTable {
					curRow = nil
				}
			case "tc":
				if inTable {
					inCell = true
					cellBuf.Reset()
				}
			case "p":
				if !inTable {
					inPara = true
					curParaBuf.Reset()
					curParaLvl = 0
					curParaBold = false
				}
			case "pPr":
				if inPara {
					if lvl, err := strconv.Atoi(xmlAttr(t.Attr, "lvl")); err == nil && lvl > 0 {
						curParaLvl = lvl
					}
				}
			case "rPr":
				if inPara {
					if xmlAttr(t.Attr, "b") == "1" {
						curParaBold = true
					}
				}
			case "t":
				var s string
				if dec.DecodeElement(&s, &t) == nil {
					if inCell {
						cellBuf.WriteString(s)
					} else if inPara {
						curParaBuf.WriteString(s)
					}
				}
			case "blip":
				if relID := xmlAttr(t.Attr, "embed"); relID != "" {
					if dataURI, ok := loadPptxMedia(relID, slidePath, rels, readEntry, imageBudgetUsed); ok {
						slide.Images = append(slide.Images, dataURI)
					}
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "sp":
				inShape = false
				shapeIsTitle = false
			case "tc":
				if inCell {
					curRow = append(curRow, strings.TrimSpace(cellBuf.String()))
					inCell = false
				}
			case "tr":
				if inTable && len(curRow) > 0 {
					curTable = append(curTable, curRow)
				}
			case "tbl":
				if inTable && len(curTable) > 0 {
					slide.Tables = append(slide.Tables, PptxTable{Rows: curTable})
				}
				inTable = false
			case "p":
				if inPara {
					text := strings.TrimSpace(curParaBuf.String())
					inPara = false
					if text != "" {
						// Skip bare slide number placeholders like "2 / 8"
						if isLikelySlideNumber(text, index) {
							continue
						}
						if slide.Title == "" && (shapeIsTitle || len(slide.Paragraphs) == 0) {
							slide.Title = text
						} else {
							slide.Paragraphs = append(slide.Paragraphs, PptxParagraph{
								Text:  text,
								Level: curParaLvl,
								Bold:  curParaBold,
							})
						}
					}
				}
			}
		}
	}

	if slide.Title == "" {
		slide.Title = fmt.Sprintf("第 %d 页", index)
	}
	return slide
}

func isLikelySlideNumber(text string, idx int) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == strconv.Itoa(idx) {
		return true
	}
	if strings.HasPrefix(trimmed, fmt.Sprintf("%d / ", idx)) || strings.HasPrefix(trimmed, fmt.Sprintf("%d/", idx)) {
		return len(trimmed) <= 12
	}
	return false
}

func loadPptxMedia(
	relID, slidePath string,
	rels map[string]docxRelInfo,
	readEntry func(string) ([]byte, error),
	imageBudgetUsed *int,
) (string, bool) {
	info, ok := rels[relID]
	if !ok || strings.EqualFold(info.TargetMode, "External") {
		return "", false
	}
	target := resolveRelativeZipPath(path.Dir(slidePath), info.Target)
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
	raw, err := readEntry(target)
	if err != nil || len(raw) == 0 {
		return "", false
	}
	if *imageBudgetUsed+len(raw) > maxEmbeddedImageBudget {
		return "", false
	}
	*imageBudgetUsed += len(raw)
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(raw), true
}

func extractPlainPptxText(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var lines []string
	var cur strings.Builder
	inP := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "p" {
				inP = true
				cur.Reset()
			} else if t.Name.Local == "t" && inP {
				var s string
				if dec.DecodeElement(&s, &t) == nil {
					cur.WriteString(s)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "p" && inP {
				inP = false
				text := strings.TrimSpace(cur.String())
				if text != "" {
					lines = append(lines, text)
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}
