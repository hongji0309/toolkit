package excel

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

type user struct {
	ID        int64
	Name      string
	Status    int
	CreatedAt time.Time
	Remark    *string
}

var userCols = []Column[*user]{
	{Header: "用户ID", Width: 12, Value: func(u *user) any { return u.ID }},
	{Header: "姓名", Value: func(u *user) any { return u.Name }},
	{Header: "状态", Value: func(u *user) any {
		if u.Status == 1 {
			return "启用"
		}
		return "禁用"
	}},
	{Header: "注册时间", Value: func(u *user) any { return u.CreatedAt }},
	{Header: "备注", Value: func(u *user) any { return u.Remark }},
}

// readRows 读回导出结果，返回每个 sheet 的所有行
func readRows(t *testing.T, data []byte) map[string][][]string {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	res := map[string][][]string{}
	for _, name := range f.GetSheetList() {
		rows, err := f.GetRows(name)
		if err != nil {
			t.Fatalf("rows: %v", err)
		}
		res[name] = rows
	}
	return res
}

func TestExport(t *testing.T) {
	remark := "VIP"
	users := []*user{
		{ID: 1, Name: "小明", Status: 1, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local), Remark: &remark},
		{ID: 2, Name: "小王", Status: 0},
	}
	var buf bytes.Buffer
	if err := Export(&buf, userCols, users, WithSheet("用户")); err != nil {
		t.Fatal(err)
	}
	got := readRows(t, buf.Bytes())
	want := map[string][][]string{"用户": {
		{"用户ID", "姓名", "状态", "注册时间", "备注"},
		{"1", "小明", "启用", "2026-01-02 03:04:05", "VIP"},
		{"2", "小王", "禁用"}, // 零值时间和 nil 指针为空单元格
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExportEmptyWritesHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := Export(&buf, userCols, nil); err != nil {
		t.Fatal(err)
	}
	got := readRows(t, buf.Bytes())["Sheet1"]
	if len(got) != 1 || got[0][0] != "用户ID" {
		t.Fatalf("got %v, want header only", got)
	}
}

func TestExportPaged(t *testing.T) {
	const size, total = 2, 5
	var pages []int
	fetch := func(_ context.Context, page int) ([]*user, error) {
		pages = append(pages, page)
		var rows []*user
		for id := (page-1)*size + 1; id <= min(page*size, total); id++ {
			rows = append(rows, &user{ID: int64(id)})
		}
		return rows, nil
	}
	var buf bytes.Buffer
	if err := ExportPaged(context.Background(), &buf, userCols, fetch); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pages, []int{1, 2, 3, 4}) { // 第 4 页为空，导出结束
		t.Fatalf("pages %v", pages)
	}
	rows := readRows(t, buf.Bytes())["Sheet1"]
	if len(rows) != 6 || rows[5][0] != "5" {
		t.Fatalf("rows %v", rows)
	}
}

func TestExportPagedFetchError(t *testing.T) {
	boom := errors.New("db down")
	fetch := func(_ context.Context, page int) ([]*user, error) {
		if page == 2 {
			return nil, boom
		}
		return make([]*user, 2), nil
	}
	var buf bytes.Buffer
	err := ExportPaged(context.Background(), &buf, []Column[*user]{
		{Header: "ID", Value: func(u *user) any { return u }},
	}, fetch)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if buf.Len() != 0 {
		t.Fatal("出错时不应写入任何内容")
	}
}

func TestExportPagedCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fetch := func(context.Context, int) ([]*user, error) {
		cancel()
		return make([]*user, 1), nil
	}
	err := ExportPaged(ctx, &bytes.Buffer{}, []Column[*user]{
		{Header: "ID", Value: func(u *user) any { return nil }},
	}, fetch)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestExportMaxRows(t *testing.T) {
	users := make([]*user, 3)
	for i := range users {
		users[i] = &user{}
	}
	err := Export(&bytes.Buffer{}, userCols, users, WithMaxRows(2))
	if !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("err = %v", err)
	}
}

func TestExportInvalidColumns(t *testing.T) {
	if err := Export[*user](&bytes.Buffer{}, nil, nil); err == nil {
		t.Fatal("没有列时应返回错误")
	}
	if err := Export(&bytes.Buffer{}, []Column[*user]{{Header: "ID"}}, nil); err == nil {
		t.Fatal("Value 为空时应返回错误")
	}
}

func TestSheetName(t *testing.T) {
	long := "一二三四五六七八九十一二三四五六七八九十一二三四五六七八九十一二"
	cases := []struct {
		base  string
		index int
		want  string
	}{
		{"用户", 1, "用户"},
		{"用户", 2, "用户_2"},
		{long, 1, long[:31*3]},
		{long, 12, long[:28*3] + "_12"},
	}
	for _, c := range cases {
		if got := sheetName(c.base, c.index); got != c.want {
			t.Errorf("sheetName(%q, %d) = %q, want %q", c.base, c.index, got, c.want)
		}
	}
}

func TestDownloadWriter(t *testing.T) {
	w := httptest.NewRecorder()
	err := ExportPaged(context.Background(), DownloadWriter(w, "用户.xlsx"), userCols,
		func(context.Context, int) ([]*user, error) { return nil, errors.New("boom") })
	if err == nil || w.Header().Get("Content-Disposition") != "" || w.Body.Len() != 0 {
		t.Fatalf("失败时不应设置下载头: err=%v header=%v", err, w.Header())
	}

	w = httptest.NewRecorder()
	if err := Export(DownloadWriter(w, "用户.xlsx"), userCols, []*user{{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	if w.Header().Get("Content-Disposition") == "" || readRows(t, w.Body.Bytes())["Sheet1"][1][0] != "1" {
		t.Fatalf("成功时应设置下载头并输出文件: header=%v", w.Header())
	}
}

func TestSetDownloadHeader(t *testing.T) {
	w := httptest.NewRecorder()
	SetDownloadHeader(w, "用户列表.xlsx")
	if got := w.Header().Get("Content-Disposition"); got != "attachment; filename*=utf-8''%E7%94%A8%E6%88%B7%E5%88%97%E8%A1%A8.xlsx" {
		t.Fatalf("Content-Disposition = %q", got)
	}
}
