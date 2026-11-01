package excel

// options 导出配置
type options struct {
	sheet      string
	maxRows    int
	timeLayout string
}

// Option 导出配置项
type Option func(*options)

// WithSheet 设置 sheet 名称，默认 Sheet1
func WithSheet(name string) Option {
	return func(o *options) { o.sheet = name }
}

// WithMaxRows 设置最多导出的数据行数，超过时返回 ErrTooManyRows，默认不限制
func WithMaxRows(n int) Option {
	return func(o *options) { o.maxRows = n }
}

// WithTimeLayout 设置 time.Time 的输出格式，默认 2006-01-02 15:04:05
func WithTimeLayout(layout string) Option {
	return func(o *options) { o.timeLayout = layout }
}

func newOptions(opts []Option) options {
	o := options{
		sheet:      "Sheet1",
		timeLayout: "2006-01-02 15:04:05",
	}
	for _, opt := range opts {
		opt(&o)
	}
	if o.sheet == "" {
		o.sheet = "Sheet1"
	}
	return o
}
