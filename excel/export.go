package excel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// ErrTooManyRows 数据行数超过 WithMaxRows 设置的上限
var ErrTooManyRows = errors.New("excel: too many rows")

// maxSheetRows 单个 sheet 去掉表头后最多能写的数据行数，超过后自动续表
const maxSheetRows = excelize.TotalRows - 1

// Export 把 rows 导出为 xlsx 写入 w，适合数据已经查好的场景
func Export[T any](w io.Writer, cols []Column[T], rows []T, opts ...Option) error {
	return ExportPaged(context.Background(), w, cols, func(_ context.Context, page int) ([]T, error) {
		if page > 1 {
			return nil, nil
		}
		return rows, nil
	}, opts...)
}

// ExportPaged 从第 1 页开始调用 fetch 分页获取数据并流式写入，fetch 返回空列表时结束
// 每页条数由 fetch 自己决定，通常直接复用列表接口的分页查询
// 出错时不会向 w 写入任何内容，HTTP 场景下仍可返回错误响应
func ExportPaged[T any](ctx context.Context, w io.Writer, cols []Column[T],
	fetch func(ctx context.Context, page int) ([]T, error), opts ...Option) error {
	o := newOptions(opts)
	if len(cols) == 0 {
		return errors.New("excel: no columns")
	}
	for i, c := range cols {
		if c.Value == nil {
			return fmt.Errorf("excel: column %d %q has no Value", i, c.Header)
		}
	}

	f := excelize.NewFile()
	defer f.Close() // 同时清理流式写入产生的临时文件

	sw, err := newSheetWriter(f, o, cols)
	if err != nil {
		return err
	}

	total := 0
	values := make([]any, len(cols))
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := fetch(ctx, page)
		if err != nil {
			return fmt.Errorf("excel: fetch page %d: %w", page, err)
		}
		if len(rows) == 0 {
			break
		}
		if o.maxRows > 0 && total+len(rows) > o.maxRows {
			return fmt.Errorf("%w: more than %d", ErrTooManyRows, o.maxRows)
		}
		for _, row := range rows {
			for i, c := range cols {
				values[i] = normalize(c.Value(row), o.timeLayout)
			}
			if err := sw.writeRow(values); err != nil {
				return err
			}
		}
		total += len(rows)
	}

	if err := sw.flush(); err != nil {
		return err
	}
	if _, err := f.WriteTo(w); err != nil {
		return fmt.Errorf("excel: write: %w", err)
	}
	return nil
}

// SetDownloadHeader 设置 xlsx 下载响应头，filename 支持中文
func SetDownloadHeader(w http.ResponseWriter, filename string) {
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
}

// DownloadWriter 返回一个第一次写入时才设置下载响应头的 Writer，配合 Export、ExportPaged 直接写入 HTTP 响应
// 导出失败时不会写入，响应头保持不变，仍可正常返回错误响应
func DownloadWriter(w http.ResponseWriter, filename string) io.Writer {
	return &downloadWriter{w: w, filename: filename}
}

type downloadWriter struct {
	w        http.ResponseWriter
	filename string
	started  bool
}

func (d *downloadWriter) Write(p []byte) (int, error) {
	if !d.started {
		d.started = true
		SetDownloadHeader(d.w, d.filename)
	}
	return d.w.Write(p)
}

// sheetWriter 负责写表头、数据行，以及单个 sheet 写满后续表
type sheetWriter[T any] struct {
	f           *excelize.File
	cols        []Column[T]
	base        string
	headerStyle int
	sw          *excelize.StreamWriter
	index       int // 当前是第几个 sheet，从 1 开始
	row         int // 当前 sheet 已写入的数据行数
}

func newSheetWriter[T any](f *excelize.File, o options, cols []Column[T]) (*sheetWriter[T], error) {
	style, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#F2F2F2"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	if err != nil {
		return nil, fmt.Errorf("excel: new style: %w", err)
	}
	s := &sheetWriter[T]{f: f, cols: cols, base: o.sheet, headerStyle: style}
	if err := s.nextSheet(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *sheetWriter[T]) nextSheet() error {
	if s.sw != nil {
		if err := s.sw.Flush(); err != nil {
			return fmt.Errorf("excel: flush: %w", err)
		}
	}
	s.index++
	name := sheetName(s.base, s.index)
	if s.index == 1 {
		if err := s.f.SetSheetName("Sheet1", name); err != nil {
			return fmt.Errorf("excel: sheet name %q: %w", name, err)
		}
	} else if _, err := s.f.NewSheet(name); err != nil {
		return fmt.Errorf("excel: new sheet %q: %w", name, err)
	}

	sw, err := s.f.NewStreamWriter(name)
	if err != nil {
		return fmt.Errorf("excel: stream writer: %w", err)
	}
	// 列宽和冻结窗格必须在写入任何行之前设置
	for i, c := range s.cols {
		width := c.Width
		if width <= 0 {
			width = estimateWidth(c.Header)
		}
		if err := sw.SetColWidth(i+1, i+1, width); err != nil {
			return fmt.Errorf("excel: col width: %w", err)
		}
	}
	if err := sw.SetPanes(&excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return fmt.Errorf("excel: freeze header: %w", err)
	}
	header := make([]any, len(s.cols))
	for i, c := range s.cols {
		header[i] = excelize.Cell{StyleID: s.headerStyle, Value: c.Header}
	}
	if err := sw.SetRow("A1", header); err != nil {
		return fmt.Errorf("excel: header: %w", err)
	}
	s.sw, s.row = sw, 0
	return nil
}

func (s *sheetWriter[T]) writeRow(values []any) error {
	if s.row == maxSheetRows {
		if err := s.nextSheet(); err != nil {
			return err
		}
	}
	s.row++
	cell, _ := excelize.CoordinatesToCellName(1, s.row+1)
	if err := s.sw.SetRow(cell, values); err != nil {
		return fmt.Errorf("excel: sheet %d row %d: %w", s.index, s.row, err)
	}
	return nil
}

func (s *sheetWriter[T]) flush() error {
	if err := s.sw.Flush(); err != nil {
		return fmt.Errorf("excel: flush: %w", err)
	}
	return nil
}

// sheetName 第 1 个 sheet 用原名，续表加 _2、_3 后缀，总长度不超过 Excel 限制的 31 个字符
func sheetName(base string, index int) string {
	suffix := ""
	if index > 1 {
		suffix = fmt.Sprintf("_%d", index)
	}
	r := []rune(base)
	if limit := excelize.MaxSheetNameLength - len(suffix); len(r) > limit {
		r = r[:limit]
	}
	return string(r) + suffix
}

// estimateWidth 按表头显示宽度估算列宽，中文按 2 个字符宽度计算
func estimateWidth(header string) float64 {
	w := 0
	for _, r := range header {
		if utf8.RuneLen(r) > 1 {
			w += 2
		} else {
			w++
		}
	}
	return float64(max(w+4, 10))
}

// normalize 把 Value 的返回值转换成适合写入单元格的值
// nil 指针和零值时间输出空单元格，时间按 layout 格式化为文本
func normalize(v any, layout string) any {
	switch t := v.(type) {
	case nil:
		return nil
	case time.Time:
		if t.IsZero() {
			return nil
		}
		return t.Format(layout)
	case *time.Time:
		if t == nil || t.IsZero() {
			return nil
		}
		return t.Format(layout)
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		return normalize(rv.Elem().Interface(), layout)
	}
	return v
}
