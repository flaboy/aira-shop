package addon

import (
	"github.com/flaboy/aira-web/pkg/config"
	"github.com/flaboy/aira-web/pkg/migration"
)

type MetaKeyType string

const (
	MetaKeyTypeString  MetaKeyType = "string"
	MetaKeyTypeNumber  MetaKeyType = "number"
	MetaKeyTypeBoolean MetaKeyType = "boolean"
	MetaKeyTypeDate    MetaKeyType = "date"
	MetaKeyTypeText    MetaKeyType = "text"
	MetaKeyTypeUrl     MetaKeyType = "url"
)

type MetaKey struct {
	ID         uint        `gorm:"primarykey" json:"-"`
	MetaName   string      `gorm:"size:64"`
	TargetType string      `gorm:"size:20" json:"-"`
	Name       string      `gorm:"size:64"`
	DataType   MetaKeyType `gorm:"size:20;not null"`
	Regexp     string      `gorm:"size:128" json:"-"`
}

type MetaValue struct {
	ID         uint     `gorm:"primarykey" json:"-"`
	TargetID   uint     `gorm:"not null" json:"-"`
	TargetType string   `gorm:"size:20;not null" json:"-"`
	MetaKeyID  uint     `gorm:"not null"`
	Value      string   `gorm:"size:256;not null"`
	MetaKey    *MetaKey `gorm:"->;foreignKey:MetaKeyID;references:ID"`
}

func (MetaKey) TableName() string {
	return config.Config.AiraTablePreifix + "meta_keys"
}

func (MetaValue) TableName() string {
	return config.Config.AiraTablePreifix + "meta_values"
}

func init() {
	migration.RegisterAutoMigrateModels(&MetaKey{})
	migration.RegisterAutoMigrateModels(&MetaValue{})
}

type MetaValues []MetaValue

func (mv *MetaValues) GetAll() map[string]string {
	result := make(map[string]string)
	for _, meta := range *mv {
		if meta.MetaKey != nil {
			result[meta.MetaKey.MetaName] = meta.Value
		}
	}
	return result
}

func (mv *MetaValues) Get(key string) (string, bool) {
	if mv == nil {
		return "", false
	}
	for _, meta := range *mv {
		if meta.MetaKey != nil && meta.MetaKey.MetaName == key {
			return meta.Value, true
		}
	}
	return "", false
}
