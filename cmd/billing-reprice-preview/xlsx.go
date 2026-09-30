package main

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"os"
	"strings"
)

type sheet struct { Name string; Rows [][]string }
type workbook struct { Sheets []sheet }

func writeXLSX(path string, book workbook) error {
	if err := ensureOutputDir(path); err != nil { return err }
	file, err := os.Create(path)
	if err != nil { return err }
	defer file.Close()
	z := zip.NewWriter(file)
	files := map[string]string{
		"[Content_Types].xml": contentTypes(len(book.Sheets)),
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml": workbookXML(book),
		"xl/_rels/workbook.xml.rels": workbookRels(len(book.Sheets)),
		"xl/styles.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts><fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills><borders count="1"><border/></borders><cellStyleXfs count="1"><xf/></cellStyleXfs><cellXfs count="2"><xf xfId="0"/><xf xfId="0" fontId="1" applyFont="1"/></cellXfs></styleSheet>`,
	}
	for i, current := range book.Sheets { files[fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)] = sheetXML(current) }
	for name, body := range files {
		writer, err := z.Create(name); if err != nil { return err }
		if _, err = writer.Write([]byte(body)); err != nil { return err }
	}
	return z.Close()
}

func contentTypes(count int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	for i := 1; i <= count; i++ { fmt.Fprintf(&b, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i) }
	b.WriteString(`</Types>`); return b.String()
}

func workbookXML(book workbook) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	for i, s := range book.Sheets { fmt.Fprintf(&b, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, escape(s.Name), i+1, i+1) }
	b.WriteString(`</sheets></workbook>`); return b.String()
}

func workbookRels(count int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 1; i <= count; i++ { fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i, i) }
	fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`, count+1)
	return b.String()
}

func sheetXML(s sheet) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews><sheetData>`)
	for rowIndex, row := range s.Rows {
		fmt.Fprintf(&b, `<row r="%d">`, rowIndex+1)
		for colIndex, value := range row {
			style := 0; if rowIndex == 0 { style = 1 }
			fmt.Fprintf(&b, `<c r="%s%d" t="inlineStr" s="%d"><is><t xml:space="preserve">%s</t></is></c>`, columnName(colIndex+1), rowIndex+1, style, escape(value))
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData><autoFilter ref="A1:XFD1"/></worksheet>`); return b.String()
}

func columnName(n int) string { var out string; for n > 0 { n--; out = string(rune('A'+n%26))+out; n/=26 }; return out }
func escape(value string) string { var b strings.Builder; _ = xml.EscapeText(&b, []byte(value)); return b.String() }
