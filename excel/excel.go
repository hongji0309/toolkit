// Package excel 基于 excelize 的 xlsx 工具，导出按行流式写入，内存占用不随数据量增长
package excel

// Column 列定义
type Column[T any] struct {
	// Header 表头
	Header string
	// Width 列宽，小于等于 0 时按表头长度估算
	Width float64
	// Value 取出该行这一列的值，返回 nil 时单元格为空
	Value func(row T) any
}
