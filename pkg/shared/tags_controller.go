package shared

import (
	"github.com/flaboy/aira-core/pkg/database"
	"github.com/flaboy/aira-shop/pkg/addon"
	"github.com/flaboy/aira-shop/pkg/tags"
	"github.com/flaboy/aira-web/pkg/routes"

	"github.com/flaboy/pin"
	"github.com/flaboy/pin/usererrors"
	"gorm.io/gorm"
)

// TagsController Tags管理控制器
type TagsController struct {
	router *routes.GinRouter
}

// NewTagsController 创建Tags控制器
func NewTagsController() *TagsController {
	controller := &TagsController{
		router: routes.NewGinRouter(""),
	}
	controller.registerRoutes()
	return controller
}

// registerRoutes 注册Tags路由到自己的路由器
func (tc *TagsController) registerRoutes() {

	tc.router.GET("/:target_type", tc.QueryByTargetType)

	tc.router.POST("/:target_type", tc.CreateByTargetType)

	tc.router.DELETE("/:target_type/:name", tc.DeleteByTargetType)

	tc.router.PUT("/:target_type/:name", tc.UpdateByTargetType)

	tc.router.GET("/:target_type/:name/items", tc.GetTaggedItemsByTargetType)

}

// HandleRequest 处理Tags请求
func (tc *TagsController) HandleRequest(c *pin.Context, method, path string) error {

	return tc.router.HandleRequest(c, method, path)
}

// QueryByTargetType GET /:target_type
func (tc *TagsController) QueryByTargetType(c *pin.Context) error {

	targetType := routes.GetParam(c, "target_type")

	if targetType == "" {

		return usererrors.New("target_type is required")
	}

	var tagNames []addon.TagNames
	err := database.Database().Where("target_type = ?", targetType).Find(&tagNames).Error
	if err != nil {
		return err
	}

	var targetTags []addon.Tags
	err = database.Database().Where("target_type = ?", targetType).Find(&targetTags).Error
	if err != nil {
		return err
	}
	usageCounts := buildTagUsageCounts(tagNames, targetTags)

	type TagWithUsage struct {
		addon.TagNames
		UsageCount int64 `json:"usage_count"`
	}

	result := []TagWithUsage{}
	for _, tag := range tagNames {
		result = append(result, TagWithUsage{
			TagNames:   tag,
			UsageCount: usageCounts[tag.ID],
		})
	}

	return c.Render(result)
}

// CreateByTargetType POST /:target_type
func (tc *TagsController) CreateByTargetType(c *pin.Context) error {
	targetType := routes.GetParam(c, "target_type")
	if targetType == "" {
		return usererrors.New("target_type is required")
	}

	type CreateTagRequest struct {
		Name string `json:"name" binding:"required"`
	}

	var req CreateTagRequest
	if err := c.BindJSON(&req); err != nil {
		return err
	}

	err := tags.RegisterTagName(targetType, req.Name)
	if err != nil {
		return err
	}

	// Return the created tag
	var tag addon.TagNames
	err = database.Database().Where("target_type = ? AND name = ?", targetType, req.Name).Limit(1).Find(&tag).Error
	if err != nil {
		return err
	}

	return c.Render(tag)
}

// UpdateByTargetType PUT /:target_type/:name
func (tc *TagsController) UpdateByTargetType(c *pin.Context) error {
	targetType := routes.GetParam(c, "target_type")
	oldName := routes.GetParam(c, "name")

	if targetType == "" {
		return usererrors.New("target_type is required")
	}

	type UpdateTagRequest struct {
		NewName string `json:"new_name" binding:"required"`
	}

	var req UpdateTagRequest
	if err := c.BindJSON(&req); err != nil {
		return err
	}

	// 直接在控制器层实现标签重命名
	return database.Database().Transaction(func(tx *gorm.DB) error {
		// 1. 检查原标签是否存在
		var oldTag addon.TagNames
		err := tx.Where("target_type = ? AND name = ?", targetType, oldName).First(&oldTag).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return usererrors.New("Tag not found")
			}
			return err
		}

		// 2. 检查新标签名是否已存在
		var existingTag addon.TagNames
		err = tx.Where("target_type = ? AND name = ?", targetType, req.NewName).First(&existingTag).Error
		if err == nil {
			return usererrors.New("Tag with new name already exists")
		} else if err != gorm.ErrRecordNotFound {
			return err
		}

		// 3. 更新标签名称
		err = tx.Model(&oldTag).Update("name", req.NewName).Error
		if err != nil {
			return err
		}

		return nil
	})
}

// DeleteByTargetType DELETE /:target_type/:name
func (tc *TagsController) DeleteByTargetType(c *pin.Context) error {
	targetType := routes.GetParam(c, "target_type")
	tagName := routes.GetParam(c, "name")

	if targetType == "" {
		return usererrors.New("target_type is required")
	}

	err := tags.DeleteTagName(targetType, tagName)
	if err != nil {
		return err
	}

	return c.Render(map[string]interface{}{"name": tagName, "deleted": true})
}

func buildTagUsageCounts(tagNames []addon.TagNames, tagRows []addon.Tags) map[uint]int64 {
	counts := make(map[uint]int64, len(tagNames))
	for _, tagName := range tagNames {
		bitMask := uint(1) << (tagName.BitNum - 1)
		for _, tagRow := range tagRows {
			if tagCellValue(tagRow, tagName.CellName)&bitMask == bitMask {
				counts[tagName.ID]++
			}
		}
	}
	return counts
}

func tagCellValue(tagRow addon.Tags, cellName string) uint {
	switch cellName {
	case "Cell1":
		return tagRow.Cell1
	case "Cell2":
		return tagRow.Cell2
	case "Cell3":
		return tagRow.Cell3
	case "Cell4":
		return tagRow.Cell4
	case "Cell5":
		return tagRow.Cell5
	case "Cell6":
		return tagRow.Cell6
	case "Cell7":
		return tagRow.Cell7
	case "Cell8":
		return tagRow.Cell8
	case "Cell9":
		return tagRow.Cell9
	case "Cell10":
		return tagRow.Cell10
	case "Cell11":
		return tagRow.Cell11
	case "Cell12":
		return tagRow.Cell12
	case "Cell13":
		return tagRow.Cell13
	case "Cell14":
		return tagRow.Cell14
	case "Cell15":
		return tagRow.Cell15
	case "Cell16":
		return tagRow.Cell16
	}
	panic("invalid tag cell name")
}

// GetTaggedItemsByTargetType GET /:target_type/:name/items
func (tc *TagsController) GetTaggedItemsByTargetType(c *pin.Context) error {
	targetType := routes.GetParam(c, "target_type")

	if targetType == "" {
		return usererrors.New("target_type is required")
	}

	// TODO: 需要在service中实现GetTaggedItems方法
	// tagName := routes.GetParam(c, "name")
	// items, err := services.GetTaggedItems(targetType, tagName, page, size)
	// 暂时返回错误提示
	return usererrors.New("GetTaggedItems method not implemented in service layer")
}
