package docparse

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// xlsxToMarkdown 把 xlsx 工作簿解析为 markdown：每个非空工作表一节
// （`## <工作表名>` 标题），行集转 markdown 表格（首行为表头）。纯数据
// 面工作簿没有天然的标题层级，工作表名即切分的标题链入口（heading_path
// 的来源）；空表跳过，全空工作簿产空串（由内容识别处理按最短阈值拒绝）。
// 单元格经 mdCell 收敛（竖线转义、换行折叠），表格列数按表头对齐补齐。
func xlsxToMarkdown(body []byte) (string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("docparse: open xlsx container: %w", err)
	}
	defer f.Close()
	var b strings.Builder
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			return "", fmt.Errorf("docparse: read xlsx sheet %q: %w", sheet, err)
		}
		rows = nonEmptyRows(rows)
		if len(rows) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("## " + mdHeadingText(sheet) + "\n\n")
		b.WriteString(mdTable(rows))
	}
	return b.String(), nil
}

// nonEmptyRows 去除全空行（excelize 会按引用范围补尾部的空行）。
func nonEmptyRows(rows [][]string) [][]string {
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		if rowEmpty(row) {
			continue
		}
		out = append(out, row)
	}
	return out
}

// rowEmpty 报告一行是否全空。
func rowEmpty(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

// mdTable 把行集渲染为 markdown 表格：首行为表头、列数按全表最大列数
// 对齐（短行补空单元格），分隔行不带对齐标记（解析不推断列对齐）。
func mdTable(rows [][]string) string {
	cols := 0
	for _, row := range rows {
		if len(row) > cols {
			cols = len(row)
		}
	}
	var b strings.Builder
	for i, row := range rows {
		b.WriteString("|")
		for c := 0; c < cols; c++ {
			cell := ""
			if c < len(row) {
				cell = row[c]
			}
			b.WriteString(" " + mdCell(cell) + " |")
		}
		b.WriteString("\n")
		if i == 0 {
			b.WriteString("|")
			for c := 0; c < cols; c++ {
				b.WriteString(" --- |")
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
