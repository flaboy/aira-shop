package importer

import (
	"encoding/csv"
	"io"
	"strings"

	"github.com/xuri/excelize/v2"
)

func (e *EntityTypes[T]) GuessFormatType(reader io.Reader, t FileType) (*Config, error) {
	if t == FileTypeExcel {
		return e.guessFormatTypeExcel(reader)
	} else if t == FileTypeCSV {
		return e.guessFormatTypeCSV(reader)
	}
	return nil, nil
}

func (e *EntityTypes[T]) guessFormatTypeExcel(reader io.Reader) (*Config, error) {
	excelFile, err := excelize.OpenReader(reader)
	if err != nil {
		return nil, err
	}
	defer excelFile.Close()

	// 获取所有工作表名称
	sheetNames := excelFile.GetSheetList()

	// 遍历每个预分析的格式
	for _, format := range e.Formats {

		// 遍历每个工作表
		for _, sheetName := range sheetNames {
			// 在每个工作表中检查前10行作为可能的头部行
			for headerRow := 1; headerRow <= 10; headerRow++ {
				if mapping, match := e.isFormatMatchWithAnalysisExcel(excelFile, sheetName, headerRow, format.Name); match {
					return &Config{
						Type:       string(FileTypeExcel),
						FormatName: format.Name,
						Excel: &ImportExcelMapping{
							SheetName: sheetName,
							HeaderRow: headerRow,
							Mapping:   mapping,
						},
					}, nil
				}
			}
		}
	}

	return nil, nil
}

// isFormatMatchWithAnalysisExcel 使用特定格式检查Excel格式是否匹配
// 返回字段映射和是否匹配
func (e *EntityTypes[T]) isFormatMatchWithAnalysisExcel(excelFile *excelize.File, sheetName string, headerRow int, formatName string) (map[string]string, bool) {
	// 找到对应的格式定义
	var targetFormat *importType
	for _, format := range e.Formats {
		if format.Name == formatName {
			targetFormat = &format
			break
		}
	}

	if targetFormat == nil {
		return nil, false
	}

	// 获取指定行的所有列值
	rows, err := excelFile.GetRows(sheetName)
	if err != nil || len(rows) < headerRow {
		return nil, false
	}

	headerRowData := rows[headerRow-1] // Excel行号从1开始，数组索引从0开始
	if len(headerRowData) == 0 {
		return nil, false
	}

	// 清理表头数据（去除星号前缀、转小写、去空格）
	cleanHeaders := make([]string, len(headerRowData))
	for i, header := range headerRowData {
		cleanHeader := strings.ToLower(strings.TrimSpace(header))
		// 去除BOM字符（如果存在）
		cleanHeader = strings.TrimPrefix(cleanHeader, "\ufeff")
		// 去除星号前缀
		cleanHeader = strings.TrimPrefix(cleanHeader, "*")
		cleanHeaders[i] = cleanHeader
	}

	// 检查格式映射中的字段是否在表头中
	mapping := make(map[string]string)
	matchCount := 0
	requiredMatchCount := 0
	requiredFieldCount := 0

	// 获取基础字段信息（用于判断是否必需和类型）
	fieldInfoMap := make(map[string]FieldInfo)
	for _, fieldInfo := range e.Fields {
		fieldInfoMap[fieldInfo.FieldName] = fieldInfo
	}

	for fieldName, expectedValue := range targetFormat.Mapper {
		// 使用映射中定义的期望值，而不是字段名
		expectedHeader := strings.ToLower(expectedValue)

		// 从基础字段信息中获取字段详情
		fieldInfo, exists := fieldInfoMap[fieldName]
		if !exists {
			continue
		}

		if fieldInfo.IsRequired {
			requiredFieldCount++
		}

		if colIndex := e.findColumnIndex(cleanHeaders, expectedHeader); colIndex != -1 {
			matchCount++
			if fieldInfo.IsRequired {
				requiredMatchCount++
			}
			// 将列索引转换为Excel列名 (A, B, C...)
			colName := e.getExcelColumnName(colIndex)
			// 使用字段名作为映射键，而不是期望值，这样可以保持嵌套结构
			mapping[fieldName] = colName
		}
	}

	// 统计信息基于格式映射，而不是基础字段
	totalMappingFields := len(targetFormat.Mapper)

	// 匹配策略：
	// 1. 如果有必需字段，所有必需字段都必须匹配
	// 2. 总体匹配度超过60%
	if requiredFieldCount > 0 && requiredMatchCount < requiredFieldCount {
		return nil, false
	}

	if totalMappingFields > 0 && float64(matchCount)/float64(totalMappingFields) > 0.6 {
		return mapping, true
	}

	return nil, false
}

// findColumnIndex 在表头切片中查找指定字符串的索引
func (e *EntityTypes[T]) findColumnIndex(headers []string, target string) int {
	for i, header := range headers {
		if header == target {
			return i
		}
	}
	return -1
}

// getExcelColumnName 将列索引转换为Excel列名 (0->A, 1->B, 25->Z, 26->AA, ...)
func (e *EntityTypes[T]) getExcelColumnName(index int) string {
	result := ""
	for index >= 0 {
		result = string(rune('A'+index%26)) + result
		index = index/26 - 1
	}
	return result
}

func (e *EntityTypes[T]) guessFormatTypeCSV(reader io.Reader) (*Config, error) {
	csvReader := csv.NewReader(reader)

	// 读取第一行作为表头
	headers, err := csvReader.Read()
	if err != nil {
		return nil, err
	}

	if len(headers) == 0 {
		return nil, nil
	}

	// 清理表头数据（去除星号前缀、转小写、去空格）
	cleanHeaders := make([]string, len(headers))
	for i, header := range headers {
		cleanHeader := strings.ToLower(strings.TrimSpace(header))
		// 去除BOM字符（如果存在）
		cleanHeader = strings.TrimPrefix(cleanHeader, "\ufeff")
		// 去除星号前缀
		cleanHeader = strings.TrimPrefix(cleanHeader, "*")
		cleanHeaders[i] = cleanHeader
	}

	// 遍历每个预分析的格式
	for _, format := range e.Formats {
		if mapping, match := e.isFormatMatchWithAnalysisCSV(cleanHeaders, format.Name); match {
			return &Config{
				Type:       string(FileTypeCSV),
				FormatName: format.Name,
				CSV:        &mapping,
			}, nil
		}
	}

	return nil, nil
}

// isFormatMatchWithAnalysisCSV 使用预分析结果检查CSV格式是否匹配
// 返回字段映射和是否匹配
func (e *EntityTypes[T]) isFormatMatchWithAnalysisCSV(cleanHeaders []string, formatName string) (ImportCSVMapping, bool) {

	// 找到对应的格式定义
	var targetFormat *importType
	for _, format := range e.Formats {
		if format.Name == formatName {
			targetFormat = &format
			break
		}
	}

	if targetFormat == nil {

		return nil, false
	}

	// 2. 打印要求的字段和匹配情况，每行一个

	// 检查格式映射中的字段是否在表头中
	mapping := make(ImportCSVMapping)
	matchCount := 0
	requiredMatchCount := 0
	requiredFieldCount := 0

	// 获取基础字段信息（用于判断是否必需和类型）
	fieldInfoMap := make(map[string]FieldInfo)
	for _, fieldInfo := range e.Fields {
		fieldInfoMap[fieldInfo.FieldName] = fieldInfo
	}

	for fieldName, expectedValue := range targetFormat.Mapper {
		// 使用映射中定义的期望值，而不是字段名
		expectedHeader := strings.ToLower(expectedValue)

		// 从基础字段信息中获取字段详情
		fieldInfo, exists := fieldInfoMap[fieldName]
		if !exists {

			continue
		}

		if fieldInfo.IsRequired {
			requiredFieldCount++
		}

		// 确定字段的层次路径显示
		isNestedField := strings.Contains(fieldName, ".")

		if colIndex := e.findColumnIndex(cleanHeaders, expectedHeader); colIndex != -1 {
			matchCount++
			if fieldInfo.IsRequired {
				requiredMatchCount++
			}
			// CSV中直接使用列索引
			// 使用字段名作为映射键，而不是期望值，这样可以保持嵌套结构
			mapping[fieldName] = uint(colIndex)

			if isNestedField {

			} else {

			}
		} else {
			if isNestedField {

			} else {

			}
		}
	}

	// 统计信息基于格式映射，而不是基础字段
	totalMappingFields := len(targetFormat.Mapper)

	if totalMappingFields > 0 {

	}

	// 匹配策略：
	// 1. 如果有必需字段，所有必需字段都必须匹配
	// 2. 总体匹配度超过60%
	if requiredFieldCount > 0 && requiredMatchCount < requiredFieldCount {

		return nil, false
	}

	if totalMappingFields > 0 && float64(matchCount)/float64(totalMappingFields) > 0.6 {

		return mapping, true
	}

	return nil, false
}
