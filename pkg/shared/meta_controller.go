package shared

import (
	"github.com/flaboy/aira/aira-core/pkg/database"
	"github.com/flaboy/aira/aira-shop/pkg/addon"
	"github.com/flaboy/aira/aira-shop/pkg/metadata"
	"github.com/flaboy/aira/aira-web/pkg/crud"
	"github.com/flaboy/aira/aira-web/pkg/routes"

	"github.com/flaboy/pin"
	"github.com/flaboy/pin/usererrors"
)

// MetaController Meta管理控制器
type MetaController struct {
	router *routes.GinRouter
}

// NewMetaController 创建Meta控制器
func NewMetaController() *MetaController {
	controller := &MetaController{
		router: routes.NewGinRouter(""),
	}
	controller.registerRoutes()
	return controller
}

// registerRoutes 注册Meta路由到自己的路由器
func (mc *MetaController) registerRoutes() {

	mc.router.GET("/:target_type", mc.QueryByTargetType)

	mc.router.POST("/:target_type", mc.CreateByTargetType)

	mc.router.PUT("/:target_type/:id", mc.UpdateByTargetType)

	mc.router.DELETE("/:target_type/:id", mc.DeleteByTargetType)

	mc.router.GET("/:target_type/statistics", mc.StatisticsByTargetType)

	mc.router.POST("/:target_type/bulk-delete", mc.BulkDeleteByTargetType)

}

// HandleRequest 处理Meta请求
func (mc *MetaController) HandleRequest(c *pin.Context, method, path string) error {

	err := mc.router.HandleRequest(c, method, path)
	if err != nil {

	} else {

	}
	return err
}

// QueryByTargetType GET /:target_type
func (mc *MetaController) QueryByTargetType(c *pin.Context) error {
	targetType := routes.GetParam(c, "target_type")
	if targetType == "" {
		return usererrors.New("target_type is required")
	}

	form := crud.QueryForm{}
	if err := form.Parse(c); err != nil {
		return err
	}

	list := []addon.MetaKey{}
	tx := database.Database().Model(&addon.MetaKey{}).Where("target_type = ?", targetType)

	err := tx.Count(&form.Pagination.Total).
		Offset((form.Pagination.Page - 1) * form.Pagination.Size).
		Limit(form.Pagination.Size).
		Find(&list).Error

	if err != nil {
		return err
	}

	return c.Render(crud.QueryResult{Items: list, Pagination: &form.Pagination})
}

// CreateByTargetType POST /:target_type
func (mc *MetaController) CreateByTargetType(c *pin.Context) error {
	targetType := routes.GetParam(c, "target_type")
	if targetType == "" {
		return usererrors.New("target_type is required")
	}

	type CreateMetaRequest struct {
		MetaName string            `json:"meta_name" binding:"required"`
		Name     string            `json:"name" binding:"required"`
		DataType addon.MetaKeyType `json:"data_type" binding:"required"`
		Regexp   string            `json:"regexp"`
	}

	var req CreateMetaRequest
	if err := c.BindJSON(&req); err != nil {
		return err
	}

	options := metadata.MetaKeyOptions{
		TargetType: targetType,
		Name:       req.Name,
		DataType:   req.DataType,
		Regexp:     req.Regexp,
	}

	err := metadata.RegisterMetaKey(targetType, req.MetaName, options)
	if err != nil {
		return err
	}

	// Return the created meta key
	var metaKey addon.MetaKey
	err = database.Database().Where("target_type = ? AND meta_name = ?", targetType, req.MetaName).Limit(1).Find(&metaKey).Error
	if err != nil {
		return err
	}

	return c.Render(metaKey)
}

// UpdateByTargetType PUT /:target_type/:id
func (mc *MetaController) UpdateByTargetType(c *pin.Context) error {
	targetType := routes.GetParam(c, "target_type")
	id := routes.GetParam(c, "id")

	if targetType == "" {
		return usererrors.New("target_type is required")
	}

	type UpdateMetaRequest struct {
		Name     string            `json:"name" binding:"required"`
		DataType addon.MetaKeyType `json:"data_type" binding:"required"`
		Regexp   string            `json:"regexp"`
	}

	var req UpdateMetaRequest
	if err := c.BindJSON(&req); err != nil {
		return err
	}

	var metaKey addon.MetaKey
	err := database.Database().Where("id = ? AND target_type = ?", id, targetType).Limit(1).Find(&metaKey).Error
	if err != nil {
		return err
	}
	if metaKey.ID == 0 {
		return usererrors.New("Meta key not found")
	}

	// Update the meta key
	err = database.Database().Model(&metaKey).Updates(addon.MetaKey{
		Name:     req.Name,
		DataType: req.DataType,
		Regexp:   req.Regexp,
	}).Error
	if err != nil {
		return err
	}

	return c.Render(metaKey)
}

// DeleteByTargetType DELETE /:target_type/:id
func (mc *MetaController) DeleteByTargetType(c *pin.Context) error {
	targetType := routes.GetParam(c, "target_type")
	id := routes.GetParam(c, "id")

	if targetType == "" {
		return usererrors.New("target_type is required")
	}

	var metaKey addon.MetaKey
	err := database.Database().Where("id = ? AND target_type = ?", id, targetType).Limit(1).Find(&metaKey).Error
	if err != nil {
		return err
	}
	if metaKey.ID == 0 {
		return usererrors.New("Meta key not found")
	}

	err = metadata.DeleteMetaKey(metaKey.TargetType, metaKey.MetaName)
	if err != nil {
		return err
	}

	return c.Render(map[string]interface{}{"id": id, "deleted": true})
}

// StatisticsByTargetType GET /:target_type/statistics
func (mc *MetaController) StatisticsByTargetType(c *pin.Context) error {
	targetType := routes.GetParam(c, "target_type")
	if targetType == "" {
		return usererrors.New("target_type is required")
	}

	var metaKeys []addon.MetaKey
	err := database.Database().Where("target_type = ?", targetType).Find(&metaKeys).Error
	if err != nil {
		return err
	}

	type MetaStatistics struct {
		MetaName string `json:"meta_name"`
		Name     string `json:"name"`
	}

	var statistics []MetaStatistics
	for _, metaKey := range metaKeys {
		statistics = append(statistics, MetaStatistics{
			MetaName: metaKey.MetaName,
			Name:     metaKey.Name,
		})
	}

	return c.Render(map[string]interface{}{
		"target_type": targetType,
		"statistics":  statistics,
	})
}

// BulkDeleteByTargetType POST /:target_type/bulk-delete
func (mc *MetaController) BulkDeleteByTargetType(c *pin.Context) error {
	targetType := routes.GetParam(c, "target_type")
	if targetType == "" {
		return usererrors.New("target_type is required")
	}

	type BulkDeleteRequest struct {
		IDs []uint `json:"ids" binding:"required"`
	}

	var req BulkDeleteRequest
	if err := c.BindJSON(&req); err != nil {
		return err
	}

	var metaKeys []addon.MetaKey
	err := database.Database().Where("id IN ? AND target_type = ?", req.IDs, targetType).Find(&metaKeys).Error
	if err != nil {
		return err
	}

	var deletedCount int
	for _, metaKey := range metaKeys {
		err := metadata.DeleteMetaKey(metaKey.TargetType, metaKey.MetaName)
		if err == nil {
			deletedCount++
		}
	}

	return c.Render(map[string]interface{}{
		"deleted_count": deletedCount,
		"total_count":   len(req.IDs),
	})
}
