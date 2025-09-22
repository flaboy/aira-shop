package tags

import (
	"errors"
	"fmt"
	"strings"

	"github.com/flaboy/aira/aira-core/pkg/database"
	"github.com/flaboy/aira/aira-shop/pkg/addon"

	"gorm.io/gorm"
)

// RegisterTagName registers a new tag name and finds the first available bit position
func RegisterTagName(targetType, name string) error {
	return database.Database().Transaction(func(tx *gorm.DB) error {
		// Check if tag name already exists
		var existingTag addon.TagNames
		if err := tx.Where("target_type = ? AND name = ?", targetType, name).Limit(1).Find(&existingTag).Error; err != nil {
			return err
		}
		if existingTag.ID != 0 {
			return errors.New("tag name already exists")
		}

		// Get all existing tag names for this target type
		var existingTags []addon.TagNames
		if err := tx.Where("target_type = ?", targetType).Find(&existingTags).Error; err != nil {
			return err
		}

		// Find the first available bit position
		cellName, bitNum, err := findFirstAvailableBit(existingTags)
		if err != nil {
			return err
		}

		// Create new tag name record
		newTagName := addon.TagNames{
			TargetType: targetType,
			Name:       name,
			CellName:   cellName,
			BitNum:     bitNum,
		}

		return tx.Create(&newTagName).Error
	})
}

// DeleteTagName deletes a tag name and clears corresponding bits in all tags
func DeleteTagName(targetType, name string) error {
	return database.Database().Transaction(func(tx *gorm.DB) error {
		// Find the tag name to delete
		var tagName addon.TagNames
		if err := tx.Where("target_type = ? AND name = ?", targetType, name).Limit(1).Find(&tagName).Error; err != nil {
			return err
		}
		if tagName.ID == 0 {
			return errors.New("tag name not found")
		}

		// Clear the corresponding bit in all tags of this target type
		bitMask := uint64(1) << (tagName.BitNum - 1)
		updateSQL := fmt.Sprintf("UPDATE tags SET %s = %s & ~%d WHERE target_type = ?",
			tagName.CellName, tagName.CellName, bitMask)

		if err := tx.Exec(updateSQL, targetType).Error; err != nil {
			return err
		}

		// Delete the tag name
		return tx.Delete(&tagName).Error
	})
}

// AddTag adds tags to a target object
func AddTags(targetType string, tx *gorm.DB, targetID uint, tagNames []string) error {
	if len(tagNames) == 0 {
		return nil
	}

	// Find all tag names
	var tags []addon.TagNames
	if err := tx.Where("target_type = ? AND name IN ?", targetType, tagNames).Find(&tags).Error; err != nil {
		return err
	}
	if len(tags) != len(tagNames) {
		return errors.New("one or more tag names not found")
	}

	// Find or create tags record
	var targetTags addon.Tags
	if err := tx.Where("target_type = ? AND target_id = ?", targetType, targetID).Limit(1).Find(&targetTags).Error; err != nil {
		return err
	}

	if targetTags.ID == 0 {
		// Create new tags record
		targetTags = addon.Tags{
			TargetType: targetType,
			TargetID:   targetID,
		}
		if err := tx.Create(&targetTags).Error; err != nil {
			return err
		}
	}

	// Group tags by cell and calculate combined bit masks
	cellUpdates := make(map[string]uint64)
	for _, tag := range tags {
		bitMask := uint64(1) << (tag.BitNum - 1)
		cellUpdates[tag.CellName] |= bitMask
	}

	// Build single UPDATE SQL with all cell updates
	if len(cellUpdates) > 0 {
		updateParts := make([]string, 0, len(cellUpdates))

		for cellName, bitMask := range cellUpdates {
			updateParts = append(updateParts, fmt.Sprintf("%s = %s | %d", cellName, cellName, bitMask))
		}

		// Use strings.Join to create comma-separated SET clause
		updateSQL := fmt.Sprintf("UPDATE tags SET %s WHERE id = ?", strings.Join(updateParts, ", "))

		if err := tx.Exec(updateSQL, targetTags.ID).Error; err != nil {
			return err
		}
	}

	return nil
}

// DeleteTag removes tags from a target object
func DeleteTags(targetType string, tx *gorm.DB, targetID uint, tagNames []string) error {
	if len(tagNames) == 0 {
		return nil
	}

	// Find all tag names
	var tags []addon.TagNames
	if err := tx.Where("target_type = ? AND name IN ?", targetType, tagNames).Find(&tags).Error; err != nil {
		return err
	}
	if len(tags) == 0 {
		return errors.New("no valid tag names found")
	}

	// Find tags record
	var targetTags addon.Tags
	if err := tx.Where("target_type = ? AND target_id = ?", targetType, targetID).Limit(1).Find(&targetTags).Error; err != nil {
		return err
	}
	if targetTags.ID == 0 {
		return errors.New("tags record not found")
	}

	// Group tags by cell and calculate combined bit masks
	cellUpdates := make(map[string]uint64)
	for _, tag := range tags {
		bitMask := uint64(1) << (tag.BitNum - 1)
		cellUpdates[tag.CellName] |= bitMask
	}

	// Build single UPDATE SQL with all cell updates
	if len(cellUpdates) > 0 {
		updateParts := make([]string, 0, len(cellUpdates))

		for cellName, bitMask := range cellUpdates {
			updateParts = append(updateParts, fmt.Sprintf("%s = %s & ~%d", cellName, cellName, bitMask))
		}

		// Use strings.Join to create comma-separated SET clause
		updateSQL := fmt.Sprintf("UPDATE tags SET %s WHERE id = ?", strings.Join(updateParts, ", "))

		if err := tx.Exec(updateSQL, targetTags.ID).Error; err != nil {
			return err
		}
	}

	return nil
}

// ClearTargetsTags 批量清除目标对象的所有标签
func ClearTargetsTags(targetType string, targetIDs []uint) error {
	if len(targetIDs) == 0 {
		return nil
	}

	return database.Database().Transaction(func(tx *gorm.DB) error {
		// 找到所有目标对象的 Tags 记录
		var targetTags []addon.Tags
		err := tx.Where("target_type = ? AND target_id IN ?", targetType, targetIDs).
			Find(&targetTags).Error
		if err != nil {
			return err
		}

		if len(targetTags) == 0 {
			return nil // 没有现有标签，无需清除
		}

		// 批量清零所有 Cell 字段
		err = tx.Model(&addon.Tags{}).
			Where("target_type = ? AND target_id IN ?", targetType, targetIDs).
			Updates(map[string]interface{}{
				"cell1": 0, "cell2": 0, "cell3": 0, "cell4": 0,
				"cell5": 0, "cell6": 0, "cell7": 0, "cell8": 0,
				"cell9": 0, "cell10": 0, "cell11": 0, "cell12": 0,
				"cell13": 0, "cell14": 0, "cell15": 0, "cell16": 0,
			}).Error
		if err != nil {
			return err
		}

		return nil
	})
}

// UpdateTargetsTags 批量更新目标对象的标签（增量更新）
func UpdateTargetsTags(targetType string, targetIDs []uint, tags []string) error {
	if len(targetIDs) == 0 {
		return nil
	}

	return database.Database().Transaction(func(tx *gorm.DB) error {
		// 1. 获取所有标签名称的配置信息
		var tagNames []addon.TagNames
		err := tx.Where("target_type = ? AND name IN ?", targetType, tags).
			Find(&tagNames).Error
		if err != nil {
			return err
		}

		// 构建标签名称到配置的映射
		tagNameMap := make(map[string]*addon.TagNames)
		for i := range tagNames {
			tagName := &tagNames[i]
			tagNameMap[tagName.Name] = tagName
		}

		// 检查是否有不存在的标签
		for _, tag := range tags {
			if _, exists := tagNameMap[tag]; !exists {
				return err
			}
		}

		// 2. 获取所有目标对象当前的 Tags 记录
		var existingTags []addon.Tags
		err = tx.Where("target_type = ? AND target_id IN ?", targetType, targetIDs).
			Find(&existingTags).Error
		if err != nil {
			return err
		}

		// 构建目标ID到Tags记录的映射
		existingTagsMap := make(map[uint]*addon.Tags)
		for i := range existingTags {
			tag := &existingTags[i]
			existingTagsMap[tag.TargetID] = tag
		}

		// 3. 为每个目标对象处理标签
		var tagsToCreate []addon.Tags
		var tagsToUpdate []addon.Tags

		for _, targetID := range targetIDs {
			// 计算新的 Cell 值
			newCellValues := [16]uint{}

			for _, tagName := range tags {
				tagConfig := tagNameMap[tagName]

				// 解析 Cell 编号
				var cellIndex int
				if _, err := fmt.Sscanf(tagConfig.CellName, "Cell%d", &cellIndex); err != nil {
					continue
				}
				cellIndex-- // 转换为0基索引

				if cellIndex < 0 || cellIndex >= 16 {
					continue
				}

				// 计算位掩码并设置对应位
				bitMask := uint(1) << (tagConfig.BitNum - 1)
				newCellValues[cellIndex] |= bitMask
			}

			if existingTag, exists := existingTagsMap[targetID]; exists {
				// 更新现有记录
				existingTag.Cell1 = newCellValues[0]
				existingTag.Cell2 = newCellValues[1]
				existingTag.Cell3 = newCellValues[2]
				existingTag.Cell4 = newCellValues[3]
				existingTag.Cell5 = newCellValues[4]
				existingTag.Cell6 = newCellValues[5]
				existingTag.Cell7 = newCellValues[6]
				existingTag.Cell8 = newCellValues[7]
				existingTag.Cell9 = newCellValues[8]
				existingTag.Cell10 = newCellValues[9]
				existingTag.Cell11 = newCellValues[10]
				existingTag.Cell12 = newCellValues[11]
				existingTag.Cell13 = newCellValues[12]
				existingTag.Cell14 = newCellValues[13]
				existingTag.Cell15 = newCellValues[14]
				existingTag.Cell16 = newCellValues[15]

				tagsToUpdate = append(tagsToUpdate, *existingTag)
			} else {
				// 创建新记录
				newTag := addon.Tags{
					TargetID:   targetID,
					TargetType: targetType,
					Cell1:      newCellValues[0],
					Cell2:      newCellValues[1],
					Cell3:      newCellValues[2],
					Cell4:      newCellValues[3],
					Cell5:      newCellValues[4],
					Cell6:      newCellValues[5],
					Cell7:      newCellValues[6],
					Cell8:      newCellValues[7],
					Cell9:      newCellValues[8],
					Cell10:     newCellValues[9],
					Cell11:     newCellValues[10],
					Cell12:     newCellValues[11],
					Cell13:     newCellValues[12],
					Cell14:     newCellValues[13],
					Cell15:     newCellValues[14],
					Cell16:     newCellValues[15],
				}
				tagsToCreate = append(tagsToCreate, newTag)
			}
		}

		// 4. 执行批量操作
		if len(tagsToCreate) > 0 {
			err = tx.Create(&tagsToCreate).Error
			if err != nil {
				return err
			}
		}

		if len(tagsToUpdate) > 0 {
			for _, tag := range tagsToUpdate {
				err = tx.Model(&addon.Tags{}).Where("id = ?", tag.ID).Updates(map[string]interface{}{
					"cell1": tag.Cell1, "cell2": tag.Cell2, "cell3": tag.Cell3, "cell4": tag.Cell4,
					"cell5": tag.Cell5, "cell6": tag.Cell6, "cell7": tag.Cell7, "cell8": tag.Cell8,
					"cell9": tag.Cell9, "cell10": tag.Cell10, "cell11": tag.Cell11, "cell12": tag.Cell12,
					"cell13": tag.Cell13, "cell14": tag.Cell14, "cell15": tag.Cell15, "cell16": tag.Cell16,
				}).Error
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
}

// Helper function to find the first available bit position
func findFirstAvailableBit(existingTags []addon.TagNames) (string, uint8, error) {
	// Create a map to track used positions
	usedPositions := make(map[string]map[uint8]bool)

	// Initialize the map for all cells
	for i := 1; i <= 16; i++ {
		cellName := fmt.Sprintf("Cell%d", i)
		usedPositions[cellName] = make(map[uint8]bool)
	}

	// Mark used positions
	for _, tag := range existingTags {
		usedPositions[tag.CellName][tag.BitNum] = true
	}

	// Find first available position
	for i := 1; i <= 16; i++ {
		cellName := fmt.Sprintf("Cell%d", i)
		for bit := uint8(1); bit <= 64; bit++ {
			if !usedPositions[cellName][bit] {
				return cellName, bit, nil
			}
		}
	}

	return "", 0, fmt.Errorf("no available bit positions")
}

func GetTagIDsByNames(targetType string, tagNames []string) ([]uint, error) {
	ids := make([]uint, 0)
	if err := database.Database().Model(&addon.TagNames{}).
		Where("target_type = ? AND name IN ?", targetType, tagNames).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// GetTagNamesByIDs returns tag names for given tag IDs
func GetTagNamesByIDs(targetType string, tagIDs []uint) ([]string, error) {
	names := make([]string, 0)
	if err := database.Database().Model(&addon.TagNames{}).
		Where("target_type = ? AND id IN ?", targetType, tagIDs).
		Pluck("name", &names).Error; err != nil {
		return nil, err
	}
	return names, nil
}

// QueryByTag returns a query builder that can be used to filter by tags
func QueryByTag(tx *gorm.DB, targetType string, tagNames []string) *gorm.DB {
	if len(tagNames) == 0 {
		return tx
	}

	// Get tag configurations for the specified tag names
	var tagConfigs []addon.TagNames
	if err := database.Database().Where("target_type = ? AND name IN ?", targetType, tagNames).Find(&tagConfigs).Error; err != nil {
		// If error occurs, return original query (could be logged)
		return tx
	}

	if len(tagConfigs) == 0 {
		// No valid tags found, return query that matches nothing
		return tx.Where("1 = 0")
	}

	tagTableName := (&addon.Tags{}).TableName()

	// Build bitwise conditions for each cell
	cellConditions := make(map[string][]string)

	for _, config := range tagConfigs {
		bitMask := uint64(1) << (config.BitNum - 1)
		condition := fmt.Sprintf("(t.%s & %d) = %d", config.CellName, bitMask, bitMask)
		cellConditions[config.CellName] = append(cellConditions[config.CellName], condition)
	}

	// Build the complete WHERE condition
	var conditions []string
	for _, cellConditions := range cellConditions {
		if len(cellConditions) > 0 {
			// For conditions in the same cell, use OR
			cellCondition := "(" + cellConditions[0]
			for i := 1; i < len(cellConditions); i++ {
				cellCondition += " OR " + cellConditions[i]
			}
			cellCondition += ")"
			conditions = append(conditions, cellCondition)
		}
	}

	if len(conditions) == 0 {
		return tx.Where("1 = 0")
	}

	// Join with tags table and apply conditions
	// For multiple conditions across different cells, use AND (must have all specified tags)
	whereCondition := conditions[0]
	for i := 1; i < len(conditions); i++ {
		whereCondition += " AND " + conditions[i]
	}

	// Get table name from tx.Statement
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

	// Use EXISTS subquery to avoid column ambiguity issues
	subQuery := fmt.Sprintf(`EXISTS (
		SELECT 1 FROM %s as t 
		WHERE t.target_type = ? 
		AND t.target_id = %s.id 
		AND (%s)
	)`, tagTableName, tableName, whereCondition)

	return tx.Where(subQuery, targetType)
}
