package metadata

import (
	"fmt"
	"regexp"

	"github.com/flaboy/aira/aira-core/pkg/database"
	"github.com/flaboy/aira/aira-shop/pkg/addon"

	"gorm.io/gorm"
)

type MetaKeyOptions struct {
	TargetType string
	Name       string
	DataType   addon.MetaKeyType
	Regexp     string
}

// RegisterMetaKey 注册一个新的元数据键
func RegisterMetaKey(targetType, metakey string, options MetaKeyOptions) error {
	return database.Database().Transaction(func(tx *gorm.DB) error {
		// 检查元数据键是否已存在
		var existingKey addon.MetaKey
		if err := tx.Where("target_type = ? AND `meta_name` = ?", targetType, metakey).First(&existingKey).Error; err == nil {
			return err
		} else if err != gorm.ErrRecordNotFound {
			return err
		}

		// 创建新的元数据键
		newMetaKey := addon.MetaKey{
			MetaName:   metakey,
			TargetType: targetType,
			Name:       options.Name,
			DataType:   options.DataType,
			Regexp:     options.Regexp,
		}

		return tx.Create(&newMetaKey).Error
	})
}

// DeleteMetaKey 删除元数据键并删除相关的元数据值
func DeleteMetaKey(targetType, metakey string) error {
	return database.Database().Transaction(func(tx *gorm.DB) error {
		// 查找要删除的元数据键
		var metaKey addon.MetaKey
		if err := tx.Where("target_type = ? AND `meta_name` = ?", targetType, metakey).First(&metaKey).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return err
			}
			return err
		}

		// 删除所有相关的元数据值
		if err := tx.Where("meta_key_id = ?", metaKey.ID).Delete(&addon.MetaValue{}).Error; err != nil {
			return err
		}

		// 删除元数据键
		return tx.Delete(&metaKey).Error
	})
}

// GetMetaList 获取指定目标类型的所有元数据键
func GetMetaList(targetType string) ([]addon.MetaKey, error) {
	var metaKeys []addon.MetaKey
	err := database.Database().Where("target_type = ?", targetType).Find(&metaKeys).Error
	return metaKeys, err
}

// QueryByMeta 根据元数据筛选查询
func QueryByMeta(tx *gorm.DB, targetType string, metakey string, val interface{}) *gorm.DB {
	if metakey == "" {
		return tx
	}

	// 查找元数据键
	var metaKey addon.MetaKey
	if err := database.Database().Where("target_type = ? AND `meta_name` = ?", targetType, metakey).First(&metaKey).Error; err != nil {
		// 如果找不到元数据键，返回空结果
		return tx.Where("1 = 0")
	}

	// 构建查询条件
	valueStr := fmt.Sprintf("%v", val)

	// 获取当前模型的表名
	tableName := tx.Statement.Table
	if tableName == "" && tx.Statement.Schema != nil {
		// If table name is not set but schema is available, use schema table name
		tableName = tx.Statement.Schema.Table
	}
	if tableName == "" {
		// Parse the model to get schema and table name
		if tx.Statement.Model != nil {
			tx.Statement.Parse(tx.Statement.Model)
			if tx.Statement.Schema != nil {
				tableName = tx.Statement.Schema.Table
			}
		}
	}

	// 使用EXISTS子查询来筛选有匹配元数据的记录
	return tx.Where("EXISTS (SELECT 1 FROM meta_values WHERE meta_values.target_type = ? AND meta_values.target_id = "+tableName+".id AND meta_values.meta_key_id = ? AND meta_values.value = ?)",
		targetType, metaKey.ID, valueStr)
}

// UpdateTargetsMeta 批量更新目标对象的元数据（增量更新）
func UpdateTargetsMeta(targetType string, targetIDs []uint, metaData map[string]string) error {
	if len(targetIDs) == 0 {
		return nil
	}

	return database.Database().Transaction(func(tx *gorm.DB) error {
		// 1. 批量获取目标对象当前的所有 meta 数据
		var existingMetaValues []addon.MetaValue
		err := tx.Where("target_type = ? AND target_id IN ?", targetType, targetIDs).
			Preload("MetaKey").
			Find(&existingMetaValues).Error
		if err != nil {
			return err
		}

		// 2. 构建当前数据的映射：targetID -> metaName -> MetaValue
		existingMap := make(map[uint]map[string]*addon.MetaValue)
		for i := range existingMetaValues {
			metaValue := &existingMetaValues[i]
			if metaValue.MetaKey == nil {
				continue
			}

			if existingMap[metaValue.TargetID] == nil {
				existingMap[metaValue.TargetID] = make(map[string]*addon.MetaValue)
			}
			existingMap[metaValue.TargetID][metaValue.MetaKey.MetaName] = metaValue
		}

		// 3. 获取所有需要的 MetaKey
		var metaNames []string
		for metaName := range metaData {
			metaNames = append(metaNames, metaName)
		}

		var metaKeys []addon.MetaKey
		err = tx.Where("target_type = ? AND meta_name IN ?", targetType, metaNames).
			Find(&metaKeys).Error
		if err != nil {
			return err
		}

		// 构建 MetaKey 映射
		metaKeyMap := make(map[string]*addon.MetaKey)
		for i := range metaKeys {
			metaKey := &metaKeys[i]
			metaKeyMap[metaKey.MetaName] = metaKey
		}

		// 4. 为每个目标对象处理 meta 数据
		var valuesToDelete []uint
		var valuesToUpdate []addon.MetaValue
		var valuesToCreate []addon.MetaValue

		for _, targetID := range targetIDs {
			currentMeta := existingMap[targetID]
			if currentMeta == nil {
				currentMeta = make(map[string]*addon.MetaValue)
			}

			// 处理需要删除的字段（当前有但新数据中没有的）
			for metaName, existingValue := range currentMeta {
				if _, exists := metaData[metaName]; !exists {
					valuesToDelete = append(valuesToDelete, existingValue.ID)
				}
			}

			// 处理需要创建或更新的字段
			for metaName, newValue := range metaData {
				if newValue == "" {
					continue // 跳过空值
				}

				metaKey := metaKeyMap[metaName]
				if metaKey == nil {
					return err
				}

				// 验证值格式
				if metaKey.Regexp != "" {
					match, err := regexp.MatchString(metaKey.Regexp, newValue)
					if err != nil {
						return err
					}
					if !match {
						return err
					}
				}

				if existingValue, exists := currentMeta[metaName]; exists {
					// 更新现有值
					if existingValue.Value != newValue {
						existingValue.Value = newValue
						valuesToUpdate = append(valuesToUpdate, *existingValue)
					}
				} else {
					// 创建新值
					valuesToCreate = append(valuesToCreate, addon.MetaValue{
						TargetType: targetType,
						TargetID:   targetID,
						MetaKeyID:  metaKey.ID,
						Value:      newValue,
						// DataType:   metaKey.DataType,
					})
				}
			}
		}

		// 5. 执行批量操作
		if len(valuesToDelete) > 0 {
			err = tx.Where("id IN ?", valuesToDelete).Delete(&addon.MetaValue{}).Error
			if err != nil {
				return err
			}
		}

		if len(valuesToCreate) > 0 {
			err = tx.Create(&valuesToCreate).Error
			if err != nil {
				return err
			}
		}

		if len(valuesToUpdate) > 0 {
			for _, value := range valuesToUpdate {
				err = tx.Model(&addon.MetaValue{}).Where("id = ?", value.ID).
					Updates(map[string]interface{}{
						"value": value.Value,
						// "data_type": value.DataType,
					}).Error
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
}
