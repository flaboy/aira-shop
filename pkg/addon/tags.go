package addon

import (
	"fmt"

	"github.com/flaboy/aira/aira-web/pkg/config"
	"github.com/flaboy/aira/aira-web/pkg/migration"
	"gorm.io/gorm"
)

type Tags struct {
	ID         uint `gorm:"primarykey"`
	TargetID   uint
	TargetType string          `gorm:"size(20)"`
	Cell1      uint            `gorm:"not null;default:0"`
	Cell2      uint            `gorm:"not null;default:0"`
	Cell3      uint            `gorm:"not null;default:0"`
	Cell4      uint            `gorm:"not null;default:0"`
	Cell5      uint            `gorm:"not null;default:0"`
	Cell6      uint            `gorm:"not null;default:0"`
	Cell7      uint            `gorm:"not null;default:0"`
	Cell8      uint            `gorm:"not null;default:0"`
	Cell9      uint            `gorm:"not null;default:0"`
	Cell10     uint            `gorm:"not null;default:0"`
	Cell11     uint            `gorm:"not null;default:0"`
	Cell12     uint            `gorm:"not null;default:0"`
	Cell13     uint            `gorm:"not null;default:0"`
	Cell14     uint            `gorm:"not null;default:0"`
	Cell15     uint            `gorm:"not null;default:0"`
	Cell16     uint            `gorm:"not null;default:0"`
	tagBitMaps [16][]TagBitMap // 缓存的位图数组，按Cell分组
}

// TagBitMap 表示每个位对应的标签名称
type TagBitMap struct {
	BitMask uint
	ID      uint // 对应的TagNames ID
	Name    string
}

type TagNames struct {
	ID         uint   `gorm:"primarykey"`
	TargetType string `gorm:"size(20)" json:"-"`
	Name       string `gorm:"size(64)"`
	CellName   string `gorm:"size(20)" json:"-"`
	BitNum     uint8  `json:"-"`
}

func (Tags) TableName() string {
	return config.Config.AiraTablePreifix + "tags"
}

func (TagNames) TableName() string {
	return config.Config.AiraTablePreifix + "tag_names"
}

func init() {
	migration.RegisterAutoMigrateModels(&Tags{})
	migration.RegisterAutoMigrateModels(&TagNames{})
}

func (m *Tags) AfterFind(tx *gorm.DB) (err error) {
	lockKey := fmt.Sprintf("tags-bitmap-%s", m.TargetType)
	tagBitMapsInterface, ok := tx.Statement.DB.InstanceGet(lockKey)
	if !ok {
		// 从数据库获取标签名称配置
		var tagNames []TagNames
		if err := tx.Model(&TagNames{}).Where("target_type = ?", m.TargetType).Find(&tagNames).Error; err != nil {
			return err
		}

		// 构建16个Cell的位图缓存
		var tagBitMaps [16][]TagBitMap
		for _, tagName := range tagNames {
			// 解析Cell编号 (Cell1 -> 0, Cell2 -> 1, ...)
			var cellIndex int
			if _, err := fmt.Sscanf(tagName.CellName, "Cell%d", &cellIndex); err != nil {
				continue
			}
			cellIndex-- // 转换为0基索引

			if cellIndex < 0 || cellIndex >= 16 {
				continue
			}

			// 计算位掩码
			bitMask := uint(1) << (tagName.BitNum - 1)

			// 添加到对应Cell的位图数组
			tagBitMaps[cellIndex] = append(tagBitMaps[cellIndex], TagBitMap{
				BitMask: bitMask,
				ID:      tagName.ID,
				Name:    tagName.Name,
			})
		}

		// 缓存位图数组
		tx.Statement.DB.InstanceSet(lockKey, tagBitMaps)
		m.tagBitMaps = tagBitMaps
	} else {
		// 从缓存获取位图数组
		if tagBitMapsInterface == nil {
			return err
		}
		var ok bool
		m.tagBitMaps, ok = tagBitMapsInterface.([16][]TagBitMap)
		if !ok {
			return err
		}
	}

	return nil
}

func (m *Tags) GetIDs() []uint {
	// 获取所有Cell的值
	cellValues := [16]uint{
		m.Cell1, m.Cell2, m.Cell3, m.Cell4,
		m.Cell5, m.Cell6, m.Cell7, m.Cell8,
		m.Cell9, m.Cell10, m.Cell11, m.Cell12,
		m.Cell13, m.Cell14, m.Cell15, m.Cell16,
	}

	var activeTagIDs []uint

	// 遍历16个Cell
	for cellIndex := 0; cellIndex < 16; cellIndex++ {
		cellValue := cellValues[cellIndex]
		if cellValue == 0 {
			continue // 如果Cell值为0，跳过
		}

		// 获取该Cell的位图数组
		bitMaps := m.tagBitMaps[cellIndex]

		// 使用位运算快速检查每个位
		for _, bitMap := range bitMaps {
			if (cellValue & bitMap.BitMask) != 0 {
				activeTagIDs = append(activeTagIDs, bitMap.ID)
			}
		}
	}

	return activeTagIDs
}

func (m *Tags) GetNames() []string {
	// 获取所有Cell的值
	cellValues := [16]uint{
		m.Cell1, m.Cell2, m.Cell3, m.Cell4,
		m.Cell5, m.Cell6, m.Cell7, m.Cell8,
		m.Cell9, m.Cell10, m.Cell11, m.Cell12,
		m.Cell13, m.Cell14, m.Cell15, m.Cell16,
	}

	var activeTagNames []string

	// 遍历16个Cell
	for cellIndex := 0; cellIndex < 16; cellIndex++ {
		cellValue := cellValues[cellIndex]
		if cellValue == 0 {
			continue // 如果Cell值为0，跳过
		}

		// 获取该Cell的位图数组
		bitMaps := m.tagBitMaps[cellIndex]

		// 使用位运算快速检查每个位
		for _, bitMap := range bitMaps {
			if (cellValue & bitMap.BitMask) != 0 {
				activeTagNames = append(activeTagNames, bitMap.Name)
			}
		}
	}

	return activeTagNames
}
