package importer

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/flaboy/aira/aira-shop/pkg/importer/types"

	"github.com/xuri/excelize/v2"
)

// ImportReader 接口定义，支持计数和行迭代
type ImportReader[T any] interface {
	Count() (int, error)
	Rows() func(yield func(T) bool)
}

// csvImportReader CSV文件的ImportReader实现
type csvImportReader[T any] struct {
	file            *os.File
	config          *ImportCSVMapping
	entityTypes     *EntityTypes[T]
	primaryKeyField string
}

// excelImportReader Excel文件的ImportReader实现
type excelImportReader[T any] struct {
	file            *os.File
	config          *ImportExcelMapping
	entityTypes     *EntityTypes[T]
	primaryKeyField string
}

// emptyImportReader 空的ImportReader实现
type emptyImportReader[T any] struct{}

func (r *emptyImportReader[T]) Count() (int, error) {
	return 0, nil
}

func (r *emptyImportReader[T]) Rows() func(yield func(T) bool) {
	return func(yield func(T) bool) {
		// 空迭代器
	}
}

func (e *EntityTypes[T]) Open(file *os.File, config *Config) ImportReader[T] {
	// 自动检测主键字段
	primaryKeyField := e.GetPrimaryKeyField()

	if config.Type == string(FileTypeExcel) && config.Excel != nil {
		return &excelImportReader[T]{
			file:            file,
			config:          config.Excel,
			entityTypes:     e,
			primaryKeyField: primaryKeyField,
		}
	} else if config.Type == string(FileTypeCSV) && config.CSV != nil {
		return &csvImportReader[T]{
			file:            file,
			config:          config.CSV,
			entityTypes:     e,
			primaryKeyField: primaryKeyField,
		}
	}

	// 返回空的实现
	return &emptyImportReader[T]{}
}

func (e *EntityTypes[T]) openExcel(fd io.Reader, config *ImportExcelMapping) func(yield func(T) bool) {
	return func(yield func(T) bool) {
		excelFile, err := excelize.OpenReader(fd)
		if err != nil {
			return
		}
		defer excelFile.Close()

		// 获取指定工作表的所有行
		rows, err := excelFile.GetRows(config.SheetName)
		if err != nil || len(rows) <= config.HeaderRow {
			return
		}

		// 跳过表头行，从数据行开始迭代
		emptyRowCount := 0
		const maxEmptyRows = 10

		for i := config.HeaderRow; i < len(rows); i++ {
			row := rows[i]

			// 检查是否为空行
			if e.isEmptyRow(row) {
				emptyRowCount++
				if emptyRowCount >= maxEmptyRows {
					fmt.Printf("Info: Encountered %d consecutive empty rows at row %d, stopping processing\n", maxEmptyRows, i+1)
					break
				}
				continue
			}

			// 重置空行计数器
			emptyRowCount = 0

			// 创建一个新的目标结构体实例
			var item T
			if err := e.mapRowToStruct(row, config.Mapping, &item); err != nil {
				fmt.Printf("Warning: Failed to map row %d: %v\n", i+1, err)
				continue // 映射失败，跳过这行
			}

			// 验证整行数据（包括必需字段、正则和类型验证）
			errors, isValid := e.ValidateRow(&item)

			if !isValid {
				fmt.Printf("Warning: Row %d validation failed, skipping\n", i+1)
				for _, err := range errors {
					fmt.Printf("  - %v\n", err)
				}
				continue
			}

			// 调用 yield 函数，如果返回 false 则停止迭代
			if !yield(item) {
				return
			}
		}
	}
}

func (e *EntityTypes[T]) openCSV(fd io.Reader, config *ImportCSVMapping) func(yield func(T) bool) {
	return func(yield func(T) bool) {
		csvReader := csv.NewReader(fd)

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
					fmt.Printf("Info: CSV encountered %d consecutive empty rows at row %d, stopping processing\n", maxEmptyRows, rowNumber)
					break
				}
				continue
			}

			// 重置空行计数器
			emptyRowCount = 0

			// 创建一个新的目标结构体实例
			var item T
			if err := e.mapCSVRowToStruct(row, *config, &item); err != nil {
				fmt.Printf("Warning: Failed to map CSV row %d: %v\n", rowNumber, err)
				continue // 映射失败，跳过这行
			}

			// 验证整行数据（包括必需字段、正则和类型验证）
			errors, isValid := e.ValidateRow(&item)

			if !isValid {
				fmt.Printf("Warning: CSV row %d validation failed, skipping\n", rowNumber)
				for _, err := range errors {
					fmt.Printf("  - %v\n", err)
				}
				continue
			}

			// 调用 yield 函数，如果返回 false 则停止迭代
			if !yield(item) {
				return
			}
		}
	}
}

// mapRowToStruct 将 Excel 行数据映射到结构体
func (e *EntityTypes[T]) mapRowToStruct(row []string, mapping map[string]string, target *T) error {
	// 使用反射设置结构体字段值
	targetValue := reflect.ValueOf(target).Elem()
	targetType := targetValue.Type()

	return e.setStructFields(targetValue, targetType, row, mapping, "")
}

// mapCSVRowToStruct 将 CSV 行数据映射到结构体
func (e *EntityTypes[T]) mapCSVRowToStruct(row []string, mapping ImportCSVMapping, target *T) error {
	// 使用反射设置结构体字段值
	targetValue := reflect.ValueOf(target).Elem()
	targetType := targetValue.Type()

	return e.setStructFieldsCSV(targetValue, targetType, row, mapping, "")
}

// findFieldInfo 在字段信息列表中查找指定字段
func (e *EntityTypes[T]) findFieldInfo(fieldName string) *FieldInfo {
	for _, fieldInfo := range e.Fields {
		if fieldInfo.FieldName == fieldName {
			return &fieldInfo
		}
	}
	return nil
}

// ValidateRow 验证整行数据的所有字段（聚合模式）
// 返回验证失败的错误列表和验证是否通过
// 如果是次行（主键为空但有嵌套数据），将忽略必填字段验证
func (e *EntityTypes[T]) ValidateRow(target *T) ([]error, bool) {
	targetValue := reflect.ValueOf(target).Elem()
	targetType := targetValue.Type()

	// 获取主键字段
	primaryKeyField := e.GetPrimaryKeyField()

	// 检查是否是次行记录（主键为空但有嵌套数据）
	var isSecondaryRow bool
	if primaryKeyField != "" {
		aggregateValue := e.GetFieldValue(target, primaryKeyField)
		isSecondaryRow = aggregateValue == "" && e.HasNonEmptyNestedFields(target)
	}

	var failedErrors []error
	isValid := e.validateRowFields(targetValue, targetType, "", &failedErrors, isSecondaryRow)
	return failedErrors, isValid
}

// validateRowFields 递归验证行字段（聚合模式，支持次行记录）
func (e *EntityTypes[T]) validateRowFields(value reflect.Value, valueType reflect.Type, prefix string, failedErrors *[]error, isSecondaryRow bool) bool {
	isValid := true

	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		fieldType := valueType.Field(i)

		fieldName := fieldType.Name
		if prefix != "" {
			fieldName = prefix + "." + fieldName
		}

		if field.Kind() == reflect.String {
			// 查找字段信息
			if fieldInfo := e.findFieldInfo(fieldName); fieldInfo != nil {
				fieldValue := field.String()

				// 对于次行记录，跳过必填字段验证
				if !isSecondaryRow {
					// 主行记录：进行完整验证
					// 1. 检查必需字段
					if fieldInfo.IsRequired && fieldValue == "" {
						*failedErrors = append(*failedErrors, fmt.Errorf("field '%s': required field is empty", fieldName))
						isValid = false
						continue
					}
				}

				// 2. 如果字段有值，进行正则验证（主行和次行都需要）
				if fieldValue != "" && fieldInfo.Regexp != "" {
					matched, err := regexp.MatchString(fieldInfo.Regexp, fieldValue)
					if err != nil {
						*failedErrors = append(*failedErrors, fmt.Errorf("field '%s': regex validation error: %v", fieldName, err))
						isValid = false
					} else if !matched {
						*failedErrors = append(*failedErrors, fmt.Errorf("field '%s': value '%s' does not match required pattern '%s'", fieldName, fieldValue, fieldInfo.Regexp))
						isValid = false
					}
				}

				// 3. 如果字段有值，进行类型验证（主行和次行都需要）
				if fieldValue != "" {
					if err := e.validateFieldType(fieldValue, *fieldInfo); err != nil {
						*failedErrors = append(*failedErrors, fmt.Errorf("field '%s': %v", fieldName, err))
						isValid = false
					}
				}
			}
		} else if field.Kind() == reflect.Struct {
			// 递归检查嵌套结构体
			if !e.validateRowFields(field, field.Type(), fieldName, failedErrors, isSecondaryRow) {
				isValid = false
			}
		} else if field.Kind() == reflect.Slice {
			// 对于切片，检查每个元素
			for j := 0; j < field.Len(); j++ {
				elem := field.Index(j)
				if elem.Kind() == reflect.Struct {
					sliceFieldName := fmt.Sprintf("%s[%d]", fieldName, j)
					if !e.validateRowFields(elem, elem.Type(), sliceFieldName, failedErrors, isSecondaryRow) {
						isValid = false
					}
				}
			}
		}
	}
	return isValid
}

// validateFieldType 验证字段类型
func (e *EntityTypes[T]) validateFieldType(value string, fieldInfo FieldInfo) error {
	switch fieldInfo.Type {
	case CellTypeString:
		return nil
	case CellTypeNumber:
		if _, err := convertToFloat64(value); err != nil {
			return err
		}
		return nil
	case CellTypeDate:
		// 尝试解析常见的日期格式
		dateFormats := []string{
			"01/02/06",      //14
			"02-Jan-06",     //15
			"02-Jan",        //16
			"Jan-06",        //17
			"3:04 PM",       //18
			"3:04:05 PM",    //19
			"3:04",          //20
			"3:04:05",       //21
			"01/02/06 3:04", //22
			"01.02.2006",    //23
			"2006-01-02 3:04:05",
			"2006/01/02 3:04:05",
			"2006-01-02 15:04:05",
			"2006/01/02 15:04:05",
			"2006-01-02 15:04",
			"2006/01/02 15:04",
			"2006-01-02",
			"2006/01/02",
			"2006-01-02T15:04:05",
			"2006/01/02T15:04:05",
			"2006-01-02T15:04",
			"2006/01/02T15:04",
			"2006-01-02T15:04:05Z",
			"2006/01/02T15:04:05Z",
		}

		for _, format := range dateFormats {
			if _, err := time.Parse(format, value); err == nil {
				return nil // 日期有效
			}
		}
		return errors.New("invalid date format: " + value)
	case CellTypeBool:
		lowerValue := strings.ToLower(strings.TrimSpace(value))
		if lowerValue == "true" || lowerValue == "1" || lowerValue == "yes" || lowerValue == "y" ||
			lowerValue == "false" || lowerValue == "0" || lowerValue == "no" || lowerValue == "n" {
			return nil
		}
		return errors.New("invalid boolean value: " + value)
	case CellTypeURL:
		if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			return errors.New("invalid URL: " + value)
		}
		return nil
	default:
		return nil
	}
}

// setStructFields 递归设置结构体字段值（Excel）
func (e *EntityTypes[T]) setStructFields(value reflect.Value, valueType reflect.Type, row []string, mapping map[string]string, prefix string) error {
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		fieldType := valueType.Field(i)

		if !field.CanSet() {
			continue
		}

		fieldName := fieldType.Name
		if prefix != "" {
			fieldName = prefix + "." + fieldName
		}

		if field.Kind() == reflect.String {
			// 查找字段映射
			if colName, exists := mapping[fieldName]; exists {
				// 将 Excel 列名转换为索引
				colIndex := e.excelColumnNameToIndex(colName)
				if colIndex >= 0 && colIndex < len(row) {
					rawValue := row[colIndex]
					field.SetString(rawValue)
				}
			}
		} else if field.Kind() == reflect.Struct {
			// 递归处理嵌套结构体
			e.setStructFields(field, field.Type(), row, mapping, fieldName)
		} else if field.Kind() == reflect.Slice {
			// 处理切片类型 (如 []OrderLine)
			elemType := field.Type().Elem()
			if elemType.Kind() == reflect.Struct {
				// 创建一个新的结构体实例
				newElem := reflect.New(elemType).Elem()
				if err := e.setStructFields(newElem, elemType, row, mapping, fieldName); err == nil {
					// 检查是否有任何字段被设置了值
					if e.hasNonZeroFields(newElem) {
						// 将新元素添加到切片
						field.Set(reflect.Append(field, newElem))
					}
				}
			}
		}
	}
	return nil
}

// setStructFieldsCSV 递归设置结构体字段值（CSV）
func (e *EntityTypes[T]) setStructFieldsCSV(value reflect.Value, valueType reflect.Type, row []string, mapping ImportCSVMapping, prefix string) error {
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		fieldType := valueType.Field(i)

		if !field.CanSet() {
			continue
		}

		fieldName := fieldType.Name
		if prefix != "" {
			fieldName = prefix + "." + fieldName
		}

		if field.Kind() == reflect.String {
			// 查找字段映射
			if colIndex, exists := mapping[fieldName]; exists {
				if int(colIndex) < len(row) {
					rawValue := row[colIndex]
					field.SetString(rawValue)
				}
			}
		} else if field.Kind() == reflect.Struct {
			// 递归处理嵌套结构体
			e.setStructFieldsCSV(field, field.Type(), row, mapping, fieldName)
		} else if field.Kind() == reflect.Slice {
			// 处理切片类型 (如 []OrderLine)
			elemType := field.Type().Elem()
			if elemType.Kind() == reflect.Struct {
				// 创建一个新的结构体实例
				newElem := reflect.New(elemType).Elem()
				if err := e.setStructFieldsCSV(newElem, elemType, row, mapping, fieldName); err == nil {
					// 检查是否有任何字段被设置了值
					if e.hasNonZeroFields(newElem) {
						// 将新元素添加到切片
						field.Set(reflect.Append(field, newElem))
					}
				}
			}
		}
	}
	return nil
}

// excelColumnNameToIndex 将 Excel 列名转换为索引 (A->0, B->1, ...)
func (e *EntityTypes[T]) excelColumnNameToIndex(colName string) int {
	result := 0
	for i, char := range colName {
		if i == 0 {
			result = int(char) - int('A')
		} else {
			result = result*26 + int(char) - int('A') + 1
		}
	}
	return result
}

// hasNonZeroFields 检查结构体是否有非零字段
func (e *EntityTypes[T]) hasNonZeroFields(value reflect.Value) bool {
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if !field.IsZero() {
			return true
		}
	}
	return false
}

type importType struct {
	Name   string
	Mapper types.MappingDefine
}

type separator int

const (
	SeparatorDot separator = iota
	SeparatorComma
	SeparatorUnknown
)

func guessThousandSeparator(input string) separator {
	dotCount := strings.Count(input, ".")
	commaCount := strings.Count(input, ",")

	// If both dot and comma are present
	// 25,234,233.8 / 25.234.233,8
	if dotCount > 0 && commaCount > 0 {
		// Assume the less frequent one is the thousand separator
		if dotCount > commaCount {
			return SeparatorDot
		}
		return SeparatorComma
	}

	// 25.233 / 25,233
	if dotCount == 0 && commaCount == 1 {
		return SeparatorDot
	} else if dotCount == 1 && commaCount == 0 {
		return SeparatorComma
	}

	// 25.233.812 / 25,233,821
	if dotCount > 1 {
		return SeparatorDot
	}
	if commaCount > 1 {
		return SeparatorComma
	}

	// Default case if no separator is found
	return SeparatorUnknown
}

func convertToFloat64(input string) (float64, error) {

	input = strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) || r == '.' || r == ',' || r == '-' {
			return r
		}
		return -1
	}, input)

	separator := guessThousandSeparator(input)

	if separator == SeparatorUnknown {
		return strconv.ParseFloat(input, 64)
	}

	if separator == SeparatorDot {
		input = strings.ReplaceAll(input, ".", "")
		input = strings.ReplaceAll(input, ",", ".")
	} else { // separator == SeparatorComma
		input = strings.ReplaceAll(input, ",", "")
	}

	// Parse the cleaned string
	return strconv.ParseFloat(input, 64)
}

// isEmptyRow 检查行是否为空行
func (e *EntityTypes[T]) isEmptyRow(row []string) bool {
	if len(row) == 0 {
		return true
	}

	// 检查所有单元格是否都为空或只包含空白字符
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

// csvImportReader的Count方法实现
func (r *csvImportReader[T]) Count() (int, error) {
	// 保存当前文件位置
	currentPos, err := r.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}

	// 重置到文件开头
	_, err = r.file.Seek(0, io.SeekStart)
	if err != nil {
		return 0, err
	}

	csvReader := csv.NewReader(r.file)
	csvReader.FieldsPerRecord = -1

	// 跳过表头
	_, err = csvReader.Read()
	if err != nil {
		return 0, err
	}

	count := 0
	emptyRowCount := 0
	const maxEmptyRows = 10

	for {
		row, err := csvReader.Read()
		if err != nil {
			break
		}

		// 检查是否为空行
		if r.entityTypes.isEmptyRow(row) {
			emptyRowCount++
			if emptyRowCount >= maxEmptyRows {
				break
			}
			continue
		}

		emptyRowCount = 0
		count++
	}

	// 恢复文件位置
	_, err = r.file.Seek(currentPos, io.SeekStart)
	if err != nil {
		return count, err
	}

	return count, nil
}

// csvImportReader的Rows方法实现
func (r *csvImportReader[T]) Rows() func(yield func(T) bool) {
	// 重置到文件开头
	r.file.Seek(0, io.SeekStart)

	if r.primaryKeyField != "" {
		return r.entityTypes.openCSVWithAggregation(r.file, r.config, r.primaryKeyField)
	}
	return r.entityTypes.openCSV(r.file, r.config)
}

// excelImportReader的Count方法实现
func (r *excelImportReader[T]) Count() (int, error) {
	// 重置到文件开头
	_, err := r.file.Seek(0, io.SeekStart)
	if err != nil {
		return 0, err
	}

	excelFile, err := excelize.OpenReader(r.file)
	if err != nil {
		return 0, err
	}
	defer excelFile.Close()

	rows, err := excelFile.GetRows(r.config.SheetName)
	if err != nil || len(rows) <= r.config.HeaderRow {
		return 0, err
	}

	count := 0
	emptyRowCount := 0
	const maxEmptyRows = 10

	for i := r.config.HeaderRow; i < len(rows); i++ {
		row := rows[i]

		// 检查是否为空行
		if r.entityTypes.isEmptyRow(row) {
			emptyRowCount++
			if emptyRowCount >= maxEmptyRows {
				break
			}
			continue
		}

		emptyRowCount = 0
		count++
	}

	return count, nil
}

// excelImportReader的Rows方法实现
func (r *excelImportReader[T]) Rows() func(yield func(T) bool) {
	// 重置到文件开头
	r.file.Seek(0, io.SeekStart)

	if r.primaryKeyField != "" {
		return r.entityTypes.openExcelWithAggregation(r.file, r.config, r.primaryKeyField)
	}
	return r.entityTypes.openExcel(r.file, r.config)
}
