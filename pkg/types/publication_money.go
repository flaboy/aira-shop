package types

import "github.com/shopspring/decimal"

// IsPublicationDecimalResourceBounded 只判断发布数值的计算资源界限，不推导业务价格上下限。
// 必须先于比较、舍入、重缩放及字符串/JSON格式化调用；不会格式化输入数值。
func IsPublicationDecimalResourceBounded(value *decimal.Decimal) bool {
	return value != nil && value.Exponent() >= -6 && value.Exponent() <= 6 && value.Coefficient().BitLen() <= 63
}
