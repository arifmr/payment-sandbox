package pagination

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

// ctxWithQuery builds a gin context carrying the given raw query string.
func ctxWithQuery(rawQuery string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?"+rawQuery, nil)
	return c
}

func TestFromQuery(t *testing.T) {
	cases := []struct {
		name         string
		query        string
		wantPage     int
		wantPageSize int
	}{
		{"defaults when absent", "", defaultPage, defaultPageSize},
		{"explicit values", "page=3&page_size=50", 3, 50},
		{"page size capped at max", "page_size=1000", defaultPage, maxPageSize},
		{"page size exactly at max", "page_size=100", defaultPage, maxPageSize},
		{"zero page falls back", "page=0", defaultPage, defaultPageSize},
		{"negative page falls back", "page=-2", defaultPage, defaultPageSize},
		{"zero page size falls back", "page_size=0", defaultPage, defaultPageSize},
		{"non-numeric falls back", "page=abc&page_size=xyz", defaultPage, defaultPageSize},
		{"empty values fall back", "page=&page_size=", defaultPage, defaultPageSize},
		{"page kept when only size is bad", "page=4&page_size=nope", 4, defaultPageSize},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := FromQuery(ctxWithQuery(tc.query))
			if p.Page != tc.wantPage {
				t.Errorf("Page = %d, want %d", p.Page, tc.wantPage)
			}
			if p.PageSize != tc.wantPageSize {
				t.Errorf("PageSize = %d, want %d", p.PageSize, tc.wantPageSize)
			}
		})
	}
}

func TestParams_OffsetAndLimit(t *testing.T) {
	cases := []struct {
		page, pageSize int
		wantOffset     int
	}{
		{1, 20, 0},
		{2, 20, 20},
		{3, 15, 30},
		{5, 100, 400},
	}
	for _, tc := range cases {
		p := Params{Page: tc.page, PageSize: tc.pageSize}
		if got := p.Offset(); got != tc.wantOffset {
			t.Errorf("Params{%d,%d}.Offset() = %d, want %d", tc.page, tc.pageSize, got, tc.wantOffset)
		}
		if got := p.Limit(); got != tc.pageSize {
			t.Errorf("Params{%d,%d}.Limit() = %d, want %d", tc.page, tc.pageSize, got, tc.pageSize)
		}
	}
}

func TestWrap_ShapeAndMeta(t *testing.T) {
	resp := Wrap([]string{"a", "b"}, Params{Page: 2, PageSize: 20}, 42)

	if resp.Pagination.Page != 2 || resp.Pagination.PageSize != 20 || resp.Pagination.Total != 42 {
		t.Fatalf("unexpected meta: %+v", resp.Pagination)
	}

	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Data       []string `json:"data"`
		Pagination struct {
			Page     int   `json:"page"`
			PageSize int   `json:"page_size"`
			Total    int64 `json:"total"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Data) != 2 || decoded.Data[0] != "a" {
		t.Errorf("data round-trip failed: %+v", decoded.Data)
	}
	if decoded.Pagination.Total != 42 {
		t.Errorf("total = %d, want 42", decoded.Pagination.Total)
	}
}

// A nil slice must serialize as [] so clients never have to handle a null list.
func TestWrap_NilDataBecomesEmptyArray(t *testing.T) {
	resp := Wrap[string](nil, Params{Page: 1, PageSize: 20}, 0)

	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := string(decoded["data"]); got != "[]" {
		t.Errorf(`data = %s, want []`, got)
	}
}
