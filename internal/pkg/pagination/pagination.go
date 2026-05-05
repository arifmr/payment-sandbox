package pagination

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

const (
	defaultPage     = 1
	defaultPageSize = 20
	maxPageSize     = 100
)

type Params struct {
	Page     int
	PageSize int
}

func (p Params) Offset() int { return (p.Page - 1) * p.PageSize }
func (p Params) Limit() int  { return p.PageSize }

func FromQuery(c *gin.Context) Params {
	p := Params{Page: defaultPage, PageSize: defaultPageSize}
	if v := c.Query("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			p.Page = n
		}
	}
	if v := c.Query("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > maxPageSize {
				n = maxPageSize
			}
			p.PageSize = n
		}
	}
	return p
}

type Meta struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

// Response is a non-generic wire shape so OpenAPI tooling (e.g. swag) can parse it.
// Callers should use Wrap to populate Data with a typed slice.
type Response struct {
	Data       any  `json:"data"`
	Pagination Meta `json:"pagination"`
}

func Wrap[T any](data []T, p Params, total int64) Response {
	if data == nil {
		data = []T{}
	}
	return Response{
		Data: data,
		Pagination: Meta{
			Page:     p.Page,
			PageSize: p.PageSize,
			Total:    total,
		},
	}
}
