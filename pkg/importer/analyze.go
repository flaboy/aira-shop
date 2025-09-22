package importer

import (
	"reflect"
	"strings"

	"github.com/flaboy/aira-shop/pkg/importer/types"
)

// analyzeFormat 分析格式结构，提取所有字段信息
func (e *EntityTypes[T]) analyzeFormat(target any) []FieldInfo {
	Fields := []FieldInfo{}

	// 检查是否是 MappingDefine (map[string]string) 类型
	if mappingDefine, ok := target.(types.MappingDefine); ok {
		// 如果是映射定义，将其转换为字段信息
		for fieldPath, expectedValue := range mappingDefine {
			Fields = append(Fields, FieldInfo{
				FieldName:     fieldPath,
				ExpectedValue: expectedValue,
				IsRequired:    false, // 映射定义中的字段默认不是必需的
				IsPrimaryKey:  false, // 映射定义中的字段默认不是主键
				Regexp:        "",
				Type:          CellTypeString,
				Children:      []FieldInfo{},
			})
		}
		return Fields
	}

	// 使用反射分析结构体
	e.analyzeStruct(reflect.ValueOf(target), reflect.TypeOf(target), "", &Fields)
	return Fields
}

// analyzeStruct 递归分析结构体字段
func (e *EntityTypes[T]) analyzeStruct(value reflect.Value, valueType reflect.Type, prefix string, fields *[]FieldInfo) {
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		fieldType := valueType.Field(i)

		fieldName := fieldType.Name
		if prefix != "" {
			fieldName = prefix + "." + fieldName
		}

		if field.Kind() == reflect.String {
			// 使用字段名称作为期望值（小写）而不是依赖实例值
			expectedValue := strings.ToLower(fieldType.Name)

			// 检查是否是必需字段
			isRequired := false
			isPrimaryKey := false
			regexp := ""
			cellType := CellTypeString // 默认类型为字符串

			if tag := fieldType.Tag.Get("import"); tag != "" {
				isRequired = strings.Contains(tag, "required")
				isPrimaryKey = strings.Contains(tag, "primarykey")

				// 解析 format 标签
				if formatStart := strings.Index(tag, "format:"); formatStart != -1 {
					formatStr := tag[formatStart+7:] // 跳过 "format:"
					if commaIndex := strings.Index(formatStr, ","); commaIndex != -1 {
						formatStr = formatStr[:commaIndex]
					}

					switch formatStr {
					case "number":
						cellType = CellTypeNumber
					case "date":
						cellType = CellTypeDate
					case "bool":
						cellType = CellTypeBool
					case "url":
						cellType = CellTypeURL
					default:
						cellType = CellTypeString
					}
				}

				// 解析 regex 标签
				if regexStart := strings.Index(tag, "regex:"); regexStart != -1 {
					regexStr := tag[regexStart+6:] // 跳过 "regex:"
					if commaIndex := strings.Index(regexStr, ","); commaIndex != -1 {
						regexStr = regexStr[:commaIndex]
					}
					regexp = regexStr
				}
			}

			*fields = append(*fields, FieldInfo{
				FieldName:     fieldName,
				ExpectedValue: expectedValue,
				IsRequired:    isRequired,
				IsPrimaryKey:  isPrimaryKey,
				Regexp:        regexp,
				Type:          cellType,
				Children:      []FieldInfo{}, // 叶子节点没有子字段
			})
		} else if field.Kind() == reflect.Struct {
			// 处理嵌套结构体 - 创建一个父字段信息
			childFields := []FieldInfo{}
			e.analyzeStruct(field, field.Type(), "", &childFields) // 不传递prefix，让子字段保持原始名称

			if len(childFields) > 0 {
				// 创建父级字段信息
				parentField := FieldInfo{
					FieldName:     fieldName,
					ExpectedValue: "", // 父级字段通常没有直接的列映射
					IsRequired:    false,
					IsPrimaryKey:  false,
					Regexp:        "",
					Type:          CellTypeString,
					Children:      childFields,
				}

				*fields = append(*fields, parentField)

				// 同时将子字段也添加到顶级字段列表中，以便于匹配
				// 这样既保持了层次结构，又能进行扁平化匹配
				for _, childField := range childFields {
					flattenedField := FieldInfo{
						FieldName:     fieldName + "." + childField.FieldName,
						ExpectedValue: childField.ExpectedValue,
						IsRequired:    childField.IsRequired,
						IsPrimaryKey:  childField.IsPrimaryKey,
						Regexp:        childField.Regexp,
						Type:          childField.Type,
						Children:      []FieldInfo{},
					}
					*fields = append(*fields, flattenedField)
				}
			}
		} else if field.Kind() == reflect.Slice {
			// 处理数组类型，需要分析数组元素的类型
			elemType := field.Type().Elem()

			if elemType.Kind() == reflect.Struct {
				// 如果数组元素是结构体，创建一个该结构体类型的零值进行分析
				elemValue := reflect.New(elemType).Elem()

				childFields := []FieldInfo{}
				e.analyzeStruct(elemValue, elemType, "", &childFields)

				if len(childFields) > 0 {
					// 创建父级字段信息（代表数组本身）
					parentField := FieldInfo{
						FieldName:     fieldName,
						ExpectedValue: "", // 数组字段通常没有直接的列映射
						IsRequired:    false,
						IsPrimaryKey:  false,
						Regexp:        "",
						Type:          CellTypeString,
						Children:      childFields,
					}

					*fields = append(*fields, parentField)

					// 将数组元素的子字段添加到顶级字段列表中，以便于匹配
					// 使用 "ArrayFieldName.ChildFieldName" 的格式
					for _, childField := range childFields {
						flattenedField := FieldInfo{
							FieldName:     fieldName + "." + childField.FieldName,
							ExpectedValue: childField.ExpectedValue,
							IsRequired:    childField.IsRequired,
							IsPrimaryKey:  childField.IsPrimaryKey,
							Regexp:        childField.Regexp,
							Type:          childField.Type,
							Children:      []FieldInfo{},
						}
						*fields = append(*fields, flattenedField)
					}
				}
			}
		}
	}
}
