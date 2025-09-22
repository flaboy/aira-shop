package shared

import (
	"github.com/flaboy/aira/aira-core/pkg/database"
	"github.com/flaboy/aira/aira-shop/pkg/addon"
	"github.com/flaboy/aira/aira-shop/pkg/tags"
	"github.com/flaboy/aira/aira-web/pkg/routes"

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

	// Add usage count for each tag
	type TagWithUsage struct {
		addon.TagNames
		UsageCount int64 `json:"usage_count"`
	}

	result := []TagWithUsage{}
	for _, tag := range tagNames {
		count, _ := tc.getTagUsageCount(tag.TargetType, tag.Name)
		result = append(result, TagWithUsage{
			TagNames:   tag,
			UsageCount: count,
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

// Helper function to get tag usage count
func (tc *TagsController) getTagUsageCount(targetType, tagName string) (int64, error) {
	// Find the tag configuration
	var tag addon.TagNames
	err := database.Database().Where("target_type = ? AND name = ?", targetType, tagName).Limit(1).Find(&tag).Error
	if err != nil {
		return 0, err
	}
	if tag.ID == 0 {
		return 0, nil
	}

	// Count how many records have this tag set
	bitMask := uint64(1) << (tag.BitNum - 1)
	var count int64
	tagTableName := (&addon.Tags{}).TableName()

	countSQL := "SELECT COUNT(*) FROM " + tagTableName + " WHERE target_type = ? AND (" + tag.CellName + " & ?) = ?"
	err = database.Database().Raw(countSQL, targetType, bitMask, bitMask).Scan(&count).Error

	return count, err
}
