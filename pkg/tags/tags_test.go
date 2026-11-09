package tags

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/flaboy/aira-shop/pkg/addon"
	"github.com/flaboy/aira-web/pkg/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 只创建临时SQLite；不读取应用环境、不连接共享库或Redis。
func deleteTagsFixture(t *testing.T, prefix string) *gorm.DB {
	t.Helper()
	previous := config.Config
	config.Config = &config.FrameworkConfig{AiraTablePreifix: prefix}
	t.Cleanup(func() { config.Config = previous })
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tags.sqlite")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	if err := db.AutoMigrate(&addon.Tags{}, &addon.TagNames{}); err != nil {
		t.Fatal(err)
	}
	names := []addon.TagNames{{TargetType: "customers", Name: "drop-low", CellName: "Cell1", BitNum: 2}, {TargetType: "customers", Name: "drop-high", CellName: "Cell16", BitNum: 32}}
	if err := db.Create(&names).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint{7, 8} {
		if err := db.Create(&addon.Tags{ID: id, TargetID: id, TargetType: "customers", Cell1: 3, Cell16: (1 << 31) | 8}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if prefix != "" {
		if err := db.Exec("CREATE TABLE tags(id integer primary key, cell1 integer, cell16 integer)").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO tags(id,cell1,cell16) VALUES(7,99,77)").Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func assertTagBits(t *testing.T, db *gorm.DB, id, low, high uint) {
	t.Helper()
	var row addon.Tags
	if err := db.First(&row, id).Error; err != nil {
		t.Fatal(err)
	}
	if row.Cell1 != low || row.Cell16 != high {
		t.Fatalf("target %d bits got %d/%d want %d/%d", id, row.Cell1, row.Cell16, low, high)
	}
}

func TestDeleteTagsRespectsConfiguredTableAndPreservesOtherBits(t *testing.T) {
	for _, prefix := range []string{"ar_", ""} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			db := deleteTagsFixture(t, prefix)
			if err := db.Transaction(func(tx *gorm.DB) error { return DeleteTags("customers", tx, 7, []string{"drop-low", "drop-high"}) }); err != nil {
				t.Fatal(err)
			}
			assertTagBits(t, db, 7, 1, 8)
			assertTagBits(t, db, 8, 3, (1<<31)|8)
			if prefix != "" {
				var legacy struct {
					Cell1  uint
					Cell16 uint
				}
				if err := db.Table("tags").Where("id=7").First(&legacy).Error; err != nil {
					t.Fatal(err)
				}
				if legacy.Cell1 != 99 || legacy.Cell16 != 77 {
					t.Fatal("unprefixed same-ID row was changed")
				}
			}
		})
	}
}

func TestDeleteTagsUsesCallerTransaction(t *testing.T) {
	db := deleteTagsFixture(t, "ar_")
	later := errors.New("isolated later operation failed")
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := DeleteTags("customers", tx, 7, []string{"drop-low", "drop-high"}); err != nil {
			return err
		}
		return later
	})
	if !errors.Is(err, later) {
		t.Fatalf("wrong rollback cause: %v", err)
	}
	assertTagBits(t, db, 7, 3, (1<<31)|8)
	assertTagBits(t, db, 8, 3, (1<<31)|8)
}
