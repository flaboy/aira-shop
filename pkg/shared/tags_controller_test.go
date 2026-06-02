package shared

import (
	"fmt"
	"testing"

	"github.com/flaboy/aira-shop/pkg/addon"
)

func TestBuildTagUsageCounts(t *testing.T) {
	tagNames := make([]addon.TagNames, 0, 88)
	for i := 1; i <= 88; i++ {
		cellName := "Cell1"
		bitNum := uint8(i)
		if i > 64 {
			cellName = "Cell2"
			bitNum = uint8(i - 64)
		}
		tagNames = append(tagNames, addon.TagNames{
			ID:       uint(i),
			Name:     fmt.Sprintf("tag-%02d", i),
			CellName: cellName,
			BitNum:   bitNum,
		})
	}

	tagRows := []addon.Tags{
		{Cell1: 1 | 16, Cell2: 1},
		{Cell1: 16},
		{Cell1: 0, Cell2: 1},
	}

	counts := buildTagUsageCounts(tagNames, tagRows)

	if counts[1] != 1 {
		t.Fatalf("tag-01 使用量 = %d，期望 1", counts[1])
	}
	if counts[5] != 2 {
		t.Fatalf("tag-05 使用量 = %d，期望 2", counts[5])
	}
	if counts[65] != 2 {
		t.Fatalf("tag-65 使用量 = %d，期望 2", counts[65])
	}
	if counts[2] != 0 {
		t.Fatalf("tag-02 使用量 = %d，期望 0", counts[2])
	}
}
