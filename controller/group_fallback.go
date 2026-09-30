package controller

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func GetGroupFallbacks(c *gin.Context) {
	data, err := service.GetGroupFallbackAdminData()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}

func PutGroupFallback(c *gin.Context) {
	var config model.GroupFallbackConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		common.ApiError(c, err)
		return
	}
	sourceGroup := strings.TrimSpace(c.Param("source_group"))
	if config.SourceGroup != "" && strings.TrimSpace(config.SourceGroup) != sourceGroup {
		common.ApiErrorMsg(c, "请求路径与配置中的源分组不一致")
		return
	}
	config.SourceGroup = sourceGroup
	if err := service.SaveGroupFallbackConfig(config, c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	data, err := service.GetGroupFallbackAdminData()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}
