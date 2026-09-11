package factory_api

import (
	"errors"
	"net/http"
	"strings"

	"senspace/pkg/app"
	"senspace/pkg/app/contextx"
	"senspace/pkg/e"
	"senspace/service/factory_service"

	"github.com/gin-gonic/gin"
)

// @Summary 创建插件发布记录
// @Tags 数字工厂
// @Param data body factory_service.PublishRequest true "发布请求"
// @Success 200 {object} e.Error
// @Router /api/v1/factory/publish [post]
func PublishPlugin(ctx *contextx.AppContext) {
	var req factory_service.PublishRequest
	err := ctx.Gin.ShouldBindJSON(&req)
	e.PanicParameterError(err)

	record, err := factory_service.PublishPlugin(*ctx.User, req)
	panicIfFactoryError(err)
	app.Response(ctx.Gin, e.SuccessData(record))
}

// @Summary 冻结当前插件发布资产
// @Tags 数字工厂
// @Param pluginId path string true "插件业务ID"
// @Success 200 {object} e.Error
// @Router /api/v1/factory/plugins/{pluginId}/freeze-current [post]
func FreezeCurrentPluginReleaseAssets(ctx *contextx.AppContext) {
	record, err := factory_service.FreezeCurrentPluginReleaseAssets(*ctx.User, ctx.Gin.Param("pluginId"), ctx.Gin.Query("batchId"))
	panicIfFactoryError(err)
	app.Response(ctx.Gin, e.SuccessData(record))
}

// @Summary 清除当前插件发布冻结资产
// @Tags 数字工厂
// @Param pluginId path string true "插件业务ID"
// @Success 200 {object} e.Error
// @Router /api/v1/factory/plugins/{pluginId}/clear-freeze-current [post]
func ClearCurrentPluginReleaseAssetsFreeze(ctx *contextx.AppContext) {
	record, err := factory_service.ClearCurrentPluginReleaseAssetsFreeze(*ctx.User, ctx.Gin.Param("pluginId"))
	panicIfFactoryError(err)
	app.Response(ctx.Gin, e.SuccessData(record))
}

// @Summary 清除单个发布记录（dev）
// @Tags 数字工厂
// @Param id path string true "发布记录ID"
// @Success 200 {object} e.Error
// @Router /api/v1/factory/releases/{id}/clear [post]
func ClearReleaseDev(ctx *contextx.AppContext) {
	record, err := factory_service.ClearReleaseDev(*ctx.User, ctx.Gin.Param("id"))
	panicIfFactoryError(err)
	app.Response(ctx.Gin, e.SuccessData(record))
}

// @Summary 清除单个发布记录
// @Tags 数字工厂
// @Param id path string true "发布记录ID"
// @Success 200 {object} e.Error
// @Router /api/v1/factory/releases/{id}/clear [post]
func ClearRelease(ctx *contextx.AppContext) {
	record, err := factory_service.ClearRelease(*ctx.User, ctx.Gin.Param("id"))
	panicIfFactoryError(err)
	app.Response(ctx.Gin, e.SuccessData(record))
}

// @Summary 生成插件资产数据
// @Tags 数字工厂
// @Param pluginId path string true "插件业务ID"
// @Param data body factory_service.GenerateReleaseAssetDataRequest true "生成参数"
// @Success 200 {object} e.Error
// @Router /api/v1/factory/plugins/{pluginId}/generate-asset-data [post]
func GenerateReleaseAssetData(ctx *contextx.AppContext) {
	var req factory_service.GenerateReleaseAssetDataRequest
	err := ctx.Gin.ShouldBindJSON(&req)
	e.PanicParameterError(err)

	record, err := factory_service.GenerateReleaseAssetData(*ctx.User, ctx.Gin.Param("pluginId"), req)
	panicIfFactoryError(err)
	app.Response(ctx.Gin, e.SuccessData(record))
}

// @Summary 查询我的发布记录
// @Tags 数字工厂
// @Param pluginId query string false "插件业务ID"
// @Param status query string false "发布状态"
// @Param currentOnly query bool false "是否只返回当前主推版本"
// @Success 200 {object} e.Error
// @Router /api/v1/factory/releases/my [get]
func GetMyReleases(ctx *contextx.AppContext) {
	query := factory_service.ReleaseQuery{
		PluginId: ctx.Gin.Query("pluginId"),
		Status:   ctx.Gin.Query("status"),
	}
	if raw := ctx.Gin.Query("currentOnly"); raw != "" {
		value := raw == "true" || raw == "1"
		query.CurrentOnly = &value
	}

	records, err := factory_service.ListMyReleases(ctx.User.Id, query)
	panicIfFactoryError(err)
	app.Response(ctx.Gin, e.SuccessData(records))
}

// @Summary 查询数字工厂市场列表
// @Tags 数字工厂
// @Param pluginId query string false "插件业务ID"
// @Param category query string false "市场分类"
// @Param tags query string false "逗号分隔标签"
// @Param status query string false "发布状态"
// @Param currentOnly query bool false "是否只返回当前主推版本"
// @Success 200 {object} e.Error
// @Router /api/v1/factory/market [get]
func GetPublicMarketList(ctx *gin.Context) {
	records, err := factory_service.ListMarketReleases(buildMarketQuery(ctx))
	panicIfFactoryError(err)
	app.Response(ctx, e.SuccessData(records))
}

func GetMarketList(ctx *contextx.AppContext) {
	records, err := factory_service.ListMarketReleases(buildMarketQuery(ctx.Gin))
	panicIfFactoryError(err)
	app.Response(ctx.Gin, e.SuccessData(records))
}

// @Summary 查询发布详情
// @Tags 数字工厂
// @Param id path string true "发布记录ID"
// @Success 200 {object} e.Error
// @Router /api/v1/factory/releases/{id} [get]
func GetReleaseDetail(ctx *contextx.AppContext) {
	record, err := factory_service.GetReleaseDetail(ctx.Gin.Param("id"))
	panicIfFactoryError(err)
	app.Response(ctx.Gin, e.SuccessData(record))
}

// @Summary 按价值模板铸造发布资产
// @Tags 数字工厂
// @Param id path string true "发布记录ID"
// @Param data body factory_service.MintAssetRequest true "铸造参数"
// @Success 200 {object} e.Error
// @Router /api/v1/factory/releases/{id}/mint [post]
func MintReleaseAsset(ctx *contextx.AppContext) {
	var req factory_service.MintAssetRequest
	err := ctx.Gin.ShouldBindJSON(&req)
	e.PanicParameterError(err)

	record, err := factory_service.MintReleaseAsset(*ctx.User, ctx.Gin.Param("id"), req)
	panicIfFactoryError(err)
	app.Response(ctx.Gin, e.SuccessData(record))
}

// @Summary 更新发布市场信息
// @Tags 数字工厂
// @Param id path string true "发布记录ID"
// @Param data body factory_service.UpdateReleaseRequest true "市场信息"
// @Success 200 {object} e.Error
// @Router /api/v1/factory/releases/{id} [patch]
func UpdateRelease(ctx *contextx.AppContext) {
	var req factory_service.UpdateReleaseRequest
	err := ctx.Gin.ShouldBindJSON(&req)
	e.PanicParameterError(err)
	req.Id = ctx.Gin.Param("id")

	record, err := factory_service.UpdateReleaseMarket(ctx.User.Id, req)
	panicIfFactoryError(err)
	app.Response(ctx.Gin, e.SuccessData(record))
}

// @Summary 更新发布价格
// @Tags 数字工厂
// @Param id path string true "发布记录ID"
// @Param data body factory_service.UpdateReleasePriceRequest true "价格信息"
// @Success 200 {object} e.Error
// @Router /api/v1/factory/releases/{id}/price [patch]
func UpdateReleasePrice(ctx *contextx.AppContext) {
	var req factory_service.UpdateReleasePriceRequest
	err := ctx.Gin.ShouldBindJSON(&req)
	e.PanicParameterError(err)
	req.Id = ctx.Gin.Param("id")

	record, err := factory_service.UpdateReleasePrice(ctx.User.Id, req)
	panicIfFactoryError(err)
	app.Response(ctx.Gin, e.SuccessData(record))
}

// @Summary 更新发布状态
// @Tags 数字工厂
// @Param id path string true "发布记录ID"
// @Param data body factory_service.UpdateReleaseStatusRequest true "状态信息"
// @Success 200 {object} e.Error
// @Router /api/v1/factory/releases/{id}/status [patch]
func UpdateReleaseStatus(ctx *contextx.AppContext) {
	var req factory_service.UpdateReleaseStatusRequest
	err := ctx.Gin.ShouldBindJSON(&req)
	e.PanicParameterError(err)
	req.Id = ctx.Gin.Param("id")

	record, err := factory_service.UpdateReleaseStatus(ctx.User.Id, req)
	panicIfFactoryError(err)
	app.Response(ctx.Gin, e.SuccessData(record))
}

// @Summary 查询我的插件资产
// @Tags 数字工厂
// @Success 200 {object} e.Error
// @Router /api/v1/factory/ownership/my [get]
func GetMyOwnerships(ctx *contextx.AppContext) {
	records, err := factory_service.ListMyOwnerships(ctx.User.Id)
	panicIfFactoryError(err)
	app.Response(ctx.Gin, e.SuccessData(records))
}

// 转换服务层错误。
func panicIfFactoryError(err error) {
	if err == nil {
		return
	}

	var serviceErr *factory_service.ServiceError
	if errors.As(err, &serviceErr) {
		switch serviceErr.Kind {
		case factory_service.ErrorKindParameter:
			panic(e.ParameterError(serviceErr.Message))
		case factory_service.ErrorKindForbidden:
			panic(e.OtherError(serviceErr.Message))
		case factory_service.ErrorKindNotFound:
			panic(e.NewError(http.StatusNotFound, 200404, serviceErr.Message))
		case factory_service.ErrorKindConflict:
			panic(e.NewError(http.StatusBadRequest, 200409, serviceErr.Message))
		default:
			panic(e.NewError(http.StatusInternalServerError, 200500, serviceErr.Message))
		}
	}
	e.PanicServerErr(err)
}

// 拆分逗号分隔字符串。
func splitCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, item := range parts {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		result = append(result, item)
	}
	return result
}

func buildMarketQuery(ctx *gin.Context) factory_service.MarketQuery {
	query := factory_service.MarketQuery{
		PluginId: ctx.Query("pluginId"),
		Category: ctx.Query("category"),
		Status:   ctx.Query("status"),
		Tags:     splitCSV(ctx.Query("tags")),
	}
	if raw := ctx.Query("currentOnly"); raw != "" {
		value := raw == "true" || raw == "1"
		query.CurrentOnly = &value
	}
	return query
}
