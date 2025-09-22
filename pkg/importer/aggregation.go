package importer

import (
	"encoding/csv"
	"io"
	"reflect"
	"strings"
)

// // OpenWithAggregation 在现有的 Open 方法基础上添加聚合选项
// func (e *EntityTypes[T]) OpenWithAggregation(fd io.Reader, config *Config, aggregateBy string) func(yield func(T) bool) {
// 	if config.Type == string(FileTypeExcel) && config.Excel != nil {
// 		return e.openExcelWithAggregation(fd, config.Excel, aggregateBy)
// 	} else if config.Type == string(FileTypeCSV) && config.CSV != nil {
// 		return e.openCSVWithAggregation(fd, config.CSV, aggregateBy)
// 	}
// 	return func(yield func(T) bool) {
// 		// 空迭代器
// 	}
// }

// openExcelWithAggregation Excel数据聚合处理
func (e *EntityTypes[T]) openExcelWithAggregation(fd io.Reader, config *ImportExcelMapping, aggregateBy string) func(yield func(T) bool) {
	return func(yield func(T) bool) {
		// 首先收集所有数据
		allItems := make([]T, 0)

		// 使用现有的 openExcel 方法收集数据
		for item := range e.openExcel(fd, config) {
			allItems = append(allItems, item)
		}

		// 进行聚合处理
		aggregatedItems := e.aggregateItems(allItems, aggregateBy)

		// 输出聚合后的结果
		for _, item := range aggregatedItems {
			if !yield(item) {
				return
			}
		}
	}
}

// openCSVWithAggregation CSV数据聚合处理
func (e *EntityTypes[T]) openCSVWithAggregation(fd io.Reader, config *ImportCSVMapping, aggregateBy string) func(yield func(T) bool) {
	return func(yield func(T) bool) {
		// 首先收集所有数据，使用聚合感知的CSV处理
		allItems := make([]T, 0)

		// 使用聚合感知的 CSV 处理方法收集数据
		for item := range e.openCSVForAggregation(fd, config, aggregateBy) {
			allItems = append(allItems, item)
		}

		// 进行聚合处理
		aggregatedItems := e.aggregateItems(allItems, aggregateBy)

		// 输出聚合后的结果
		for _, item := range aggregatedItems {
			if !yield(item) {
				return
			}
		}
	}
}

// openCSVForAggregation CSV数据聚合感知处理，使用聚合验证
func (e *EntityTypes[T]) openCSVForAggregation(fd io.Reader, config *ImportCSVMapping, aggregateBy string) func(yield func(T) bool) {
	return func(yield func(T) bool) {
		csvReader := csv.NewReader(fd)
		csvReader.FieldsPerRecord = -1 // 允许可变数量的字段

		// 跳过表头行
		_, err := csvReader.Read()
		if err != nil {
			return
		}

		rowNumber := 1 // 跟踪行号用于错误报告
		emptyRowCount := 0
		const maxEmptyRows = 10

		// 逐行读取数据
		for {
			row, err := csvReader.Read()
			if err != nil {
				return // 读取完毕或出现错误
			}

			rowNumber++

			// 检查是否为空行
			if e.isEmptyRow(row) {
				emptyRowCount++
				if emptyRowCount >= maxEmptyRows {
					break
				}
				continue
			}

			// 重置空行计数器
			emptyRowCount = 0

			// 创建一个新的目标结构体实例
			var item T
			if err := e.mapCSVRowToStruct(row, *config, &item); err != nil {
				continue // 映射失败，跳过这行
			}

			// 使用聚合感知的验证（对次行跳过必填字段验证）
			_, isValid := e.ValidateRow(&item)
			if !isValid {
				continue
			}

			// 调用 yield 函数，如果返回 false 则停止迭代
			if !yield(item) {
				return
			}
		}
	}
}

// aggregateItems 聚合具有相同主键的数据项
func (e *EntityTypes[T]) aggregateItems(items []T, aggregateBy string) []T {
	if aggregateBy == "" {
		return items // 如果没有指定聚合字段，直接返回
	}

	// 使用 map 存储聚合后的数据，key 为聚合字段的值
	aggregateMap := make(map[string]*T)
	// 记录主键的顺序，确保结果保持原始顺序
	keyOrder := make([]string, 0)
	var lastMainRecord *T // 用于存储最近的主记录

	for _, item := range items {
		// 获取聚合字段的值
		aggregateValue := e.GetFieldValue(&item, aggregateBy)

		// 调试：输出每个项目的聚合值和嵌套字段检查结果
		hasNested := e.HasNonEmptyNestedFields(&item)

		if aggregateValue != "" {
			// 有主键值的记录 - 这是主记录
			if existing, exists := aggregateMap[aggregateValue]; exists {
				// 合并到现有项目
				e.mergeItems(existing, &item)
			} else {
				// 创建新项目
				newItem := item
				aggregateMap[aggregateValue] = &newItem
				keyOrder = append(keyOrder, aggregateValue) // 记录主键顺序
				lastMainRecord = &newItem                   // 更新最近的主记录
			}
		} else {
			// 主键为空的记录 - 检查是否有嵌套数据需要合并到最近的主记录
			if lastMainRecord != nil && hasNested {
				// 合并到最近的主记录
				e.mergeItems(lastMainRecord, &item)
			}
			// 如果没有嵌套数据或没有主记录，跳过此记录
		}
	}

	// 按照原始顺序转换 map 为数组
	result := make([]T, 0, len(aggregateMap))
	for _, key := range keyOrder {
		if item, exists := aggregateMap[key]; exists {
			result = append(result, *item)
		}
	}

	return result
}

// GetFieldValue 获取指定字段的值
func (e *EntityTypes[T]) GetFieldValue(target *T, fieldName string) string {
	targetValue := reflect.ValueOf(target).Elem()
	return e.getFieldValueRecursive(targetValue, fieldName)
}

// getFieldValueRecursive 递归获取字段值
func (e *EntityTypes[T]) getFieldValueRecursive(value reflect.Value, fieldName string) string {
	parts := strings.Split(fieldName, ".")
	currentValue := value

	for _, part := range parts {
		field := currentValue.FieldByName(part)
		if !field.IsValid() {
			return ""
		}

		if field.Kind() == reflect.String {
			return field.String()
		} else if field.Kind() == reflect.Struct {
			currentValue = field
		} else {
			return ""
		}
	}

	return ""
}

// mergeItems 合并两个项目，主要是合并切片字段
func (e *EntityTypes[T]) mergeItems(target *T, source *T) {
	targetValue := reflect.ValueOf(target).Elem()
	sourceValue := reflect.ValueOf(source).Elem()

	e.mergeStructFields(targetValue, sourceValue)
}

// mergeStructFields 递归合并结构体字段
func (e *EntityTypes[T]) mergeStructFields(target reflect.Value, source reflect.Value) {
	for i := 0; i < target.NumField(); i++ {
		targetField := target.Field(i)
		sourceField := source.Field(i)

		if !targetField.CanSet() {
			continue
		}

		switch targetField.Kind() {
		case reflect.Slice:
			// 合并切片字段
			if sourceField.Len() > 0 {
				for j := 0; j < sourceField.Len(); j++ {
					elem := sourceField.Index(j)
					targetField.Set(reflect.Append(targetField, elem))
				}
			}
		case reflect.Struct:
			// 递归合并嵌套结构体
			e.mergeStructFields(targetField, sourceField)
		case reflect.String:
			// 对于字符串字段，如果目标为空则使用源值
			if targetField.String() == "" && sourceField.String() != "" {
				targetField.SetString(sourceField.String())
			}
		default:
			// 其他类型字段，如果目标为零值则使用源值
			if targetField.IsZero() && !sourceField.IsZero() {
				targetField.Set(sourceField)
			}
		}
	}
}

// HasNonEmptyNestedFields 检查记录是否有非空的嵌套字段（如切片）
func (e *EntityTypes[T]) HasNonEmptyNestedFields(target *T) bool {
	targetValue := reflect.ValueOf(target).Elem()
	return e.checkNonEmptyNestedFields(targetValue)
}

// checkNonEmptyNestedFields 递归检查是否有非空的嵌套字段
func (e *EntityTypes[T]) checkNonEmptyNestedFields(value reflect.Value) bool {
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)

		switch field.Kind() {
		case reflect.Slice:
			// 如果切片不为空，说明有嵌套数据
			if field.Len() > 0 {
				return true
			}
		case reflect.Struct:
			// 递归检查嵌套结构体
			if e.checkNonEmptyNestedFields(field) {
				return true
			}
		}
	}
	return false
}
