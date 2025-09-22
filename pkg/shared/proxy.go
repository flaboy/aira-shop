package shared

import (
	"github.com/flaboy/pin"
)

// 全局控制器实例
var globalMetaController *MetaController
var globalTagsController *TagsController

// init 初始化全局控制器
func init() {

	globalMetaController = NewMetaController()
	globalTagsController = NewTagsController()

}

// GetGlobalMetaController 获取全局Meta控制器
func GetGlobalMetaController() *MetaController {
	return globalMetaController
}

// GetGlobalTagsController 获取全局Tags控制器
func GetGlobalTagsController() *TagsController {
	return globalTagsController
}

// HandleMetaRequest 处理Meta请求的便捷函数
func HandleMetaRequest(c *pin.Context, method, path string) error {

	return globalMetaController.HandleRequest(c, method, path)
}

// HandleTagsRequest 处理Tags请求的便捷函数
func HandleTagsRequest(c *pin.Context, method, path string) error {

	return globalTagsController.HandleRequest(c, method, path)
}
