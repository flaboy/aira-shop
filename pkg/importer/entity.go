package importer

import "github.com/flaboy/aira/aira-shop/pkg/importer/types"

type FileType string

const (
	FileTypeExcel FileType = "excel"
	FileTypeCSV   FileType = "csv"
)

type CellType string

const (
	CellTypeString CellType = "string"
	CellTypeNumber CellType = "number"
	CellTypeDate   CellType = "date"
	CellTypeBool   CellType = "bool"
	CellTypeURL    CellType = "url"
)

// FieldInfo 存储字段的分析信息
type FieldInfo struct {
	FieldName     string // 字段名称
	ExpectedValue string // 期望的列名值
	IsRequired    bool   // 是否必需字段
	IsPrimaryKey  bool   // 是否是主键字段
	Regexp        string // 正则表达式验证（如果有）
	Type          CellType
	Children      []FieldInfo // 子字段信息（如果有嵌套结构体）
}

type Config struct {
	Type       string              `json:"type"`
	FormatName string              `json:"format_name"`
	Excel      *ImportExcelMapping `json:"excel,omitempty"`
	CSV        *ImportCSVMapping   `json:"csv,omitempty"`
}

type EntityTypes[T any] struct {
	Target  T
	Formats []importType
	Fields  []FieldInfo
}

type ImportExcelMapping struct {
	SheetName string            `json:"sheet_name"`
	HeaderRow int               `json:"header_row"`
	Mapping   map[string]string `json:"mapping"` // PropName to ColumnName, Exp: order_number -> A
}

type ImportCSVMapping map[string]uint

func (e *EntityTypes[T]) parse() {
	e.Fields = e.analyzeFormat(e.Target)
}

func RegisterFormatType[T any](target T) *EntityTypes[T] {
	formatType := &EntityTypes[T]{
		Target: target,
	}
	formatType.parse()
	return formatType
}

func (e *EntityTypes[T]) AddFormat(name string, target types.MappingDefine) {
	if e.Formats == nil {
		e.Formats = []importType{}
	}
	e.Formats = append(e.Formats, importType{
		Name:   name,
		Mapper: target,
	})
}

// GetPrimaryKeyField 获取主键字段名
func (e *EntityTypes[T]) GetPrimaryKeyField() string {
	for _, field := range e.Fields {
		if field.IsPrimaryKey {
			return field.FieldName
		}
	}
	return ""
}
