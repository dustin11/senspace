package ds_api

import (
	"encoding/json"
	"mime/multipart"
	"strconv"

	"senspace/pkg/app"
	"senspace/pkg/app/contextx"
	"senspace/pkg/bizerr"
	"senspace/pkg/e"
	"senspace/pkg/setting"
	"senspace/pkg/util"
	"senspace/service/ds_service"
)

const defaultPluginUploadMaxSize = 30 * 1024 * 1024

func resolvePluginUploadMaxSize() int64 {
	if setting.Config.App.PluginUploadMaxSize > 0 {
		return setting.Config.App.PluginUploadMaxSize
	}
	return defaultPluginUploadMaxSize
}

func preparePluginAssetMultipartBody(ctx *contextx.AppContext) {
	err := util.LimitRequestBodyBytes(ctx.Gin.Writer, ctx.Gin.Request, resolvePluginUploadMaxSize())
	if util.IsRequestBodyTooLargeError(err) {
		e.PanicParameterErrorTipMsg(err, "上传内容超过 30 MB 限制")
	}
	e.PanicServerErr(err)
}

func bindPluginStateInput(ctx *contextx.AppContext) ds_service.PluginStateInput {
	var req ds_service.PluginStateInput
	err := ctx.Gin.ShouldBindJSON(&req)
	e.PanicParameterError(err)
	e.PanicIfParameterError(
		req.State != nil && (req.ResourceState != nil || req.Bindings != nil),
		"实例状态和资源状态不能同时保存",
	)
	for index, binding := range req.Bindings {
		if binding.AssetId == 0 {
			e.PanicIfParameterError(true, "资源ID不能为空")
		}
		if len(binding.Config) > 0 && !json.Valid(binding.Config) {
			e.PanicIfParameterError(true, "资源配置JSON无效")
		}
		req.Bindings[index] = binding
	}
	return req
}

func bindResourceStateCommitInput(ctx *contextx.AppContext) (ds_service.ResourceStateCommitInput, map[string]*multipart.FileHeader) {
	var req ds_service.ResourceStateCommitInput
	payload := ctx.Gin.PostForm("payload")
	if payload == "" {
		preparePluginAssetMultipartBody(ctx)
		payload = ctx.Gin.PostForm("payload")
	}
	e.PanicIfParameterError(payload == "", "payload不能为空")
	err := json.Unmarshal([]byte(payload), &req)
	e.PanicParameterError(err)
	files := map[string]*multipart.FileHeader{}
	form, err := ctx.Gin.MultipartForm()
	if util.IsRequestBodyTooLargeError(err) {
		e.PanicParameterErrorTipMsg(err, "上传内容超过 30 MB 限制")
	}
	if err == nil && form != nil {
		for key, list := range form.File {
			if len(list) > 0 {
				files[key] = list[0]
			}
		}
	}
	return req, files
}

// 素材库图片导入请求。
type pluginAssetImportImageInput struct {
	ImageId       json.RawMessage `json:"imageId"`       // 素材库图片 ID。
	CollectionKey string          `json:"collectionKey"` // 资源集合键。
}

func bindPluginAssetImportImageInput(ctx *contextx.AppContext) pluginAssetImportImageInput {
	var req pluginAssetImportImageInput
	err := ctx.Gin.ShouldBindJSON(&req)
	e.PanicParameterError(err)
	_, err = req.parseImageID()
	e.PanicParameterError(err)
	return req
}

func (req pluginAssetImportImageInput) parseImageID() (uint64, error) {
	var id uint64
	if err := json.Unmarshal(req.ImageId, &id); err == nil && id > 0 {
		return id, nil
	}
	var idText string
	if err := json.Unmarshal(req.ImageId, &idText); err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseUint(idText, 10, 64)
	if err != nil || parsed == 0 {
		return 0, strconv.ErrSyntax
	}
	return parsed, nil
}

// @Summary 上传 fact 插件实例资源
// @Tags 插件资源
// @Param factAssetId path string true "插件资产实例ID"
// @Param collectionKey formData string false "资源集合键"
// @Param file formData file true "资源文件"
// @Security ApiKeyAuth
// @Router /api/v1/plugin-assets/fact/{factAssetId}/upload [post]
func PluginAssetUploadFact(ctx *contextx.AppContext) {
	factAssetId, err := strconv.ParseInt(ctx.Gin.Param("factAssetId"), 10, 64)
	e.PanicParameterError(err)
	preparePluginAssetMultipartBody(ctx)
	file, err := ctx.Gin.FormFile("file")
	if util.IsRequestBodyTooLargeError(err) {
		e.PanicParameterErrorTipMsg(err, "上传内容超过 30 MB 限制")
	}
	e.PanicParameterErrorTipMsg(err, "file missing")
	scope, err := ds_service.ResolveFactPluginAssetScope(*ctx.User, factAssetId)
	e.PanicServerErr(err)
	result, err := ds_service.UploadPluginAssetInScope(*ctx.User, scope, ctx.Gin.PostForm("collectionKey"), file)
	e.PanicServerErr(err)
	app.Response(ctx.Gin, e.SuccessData(result))
}

// @Summary 上传 dev 插件实例资源
// @Tags 插件资源
// @Param pluginId path string true "插件ID"
// @Param version path string true "插件版本"
// @Param collectionKey formData string false "资源集合键"
// @Param file formData file true "资源文件"
// @Security ApiKeyAuth
// @Router /api/v1/plugin-assets/dev/{pluginId}/{version}/upload [post]
func PluginAssetUploadDev(ctx *contextx.AppContext) {
	pluginId := ctx.Gin.Param("pluginId")
	version := ctx.Gin.Param("version")
	preparePluginAssetMultipartBody(ctx)
	file, err := ctx.Gin.FormFile("file")
	if util.IsRequestBodyTooLargeError(err) {
		e.PanicParameterErrorTipMsg(err, "上传内容超过 30 MB 限制")
	}
	e.PanicParameterErrorTipMsg(err, "file missing")
	scope, err := ds_service.ResolveDevPluginAssetScope(*ctx.User, pluginId, version)
	e.PanicServerErr(err)
	result, err := ds_service.UploadPluginAssetInScope(*ctx.User, scope, ctx.Gin.PostForm("collectionKey"), file)
	e.PanicServerErr(err)
	app.Response(ctx.Gin, e.SuccessData(result))
}

// @Summary 从素材库导入 fact 插件实例图片
// @Tags 插件资源
// @Param factAssetId path string true "插件资产实例ID"
// @Param body body pluginAssetImportImageInput true "导入图片请求"
// @Security ApiKeyAuth
// @Router /api/v1/plugin-assets/fact/{factAssetId}/import-image [post]
func PluginAssetImportImageFact(ctx *contextx.AppContext) {
	factAssetId, err := strconv.ParseInt(ctx.Gin.Param("factAssetId"), 10, 64)
	e.PanicParameterError(err)
	req := bindPluginAssetImportImageInput(ctx)
	imageID, err := req.parseImageID()
	e.PanicParameterError(err)
	scope, err := ds_service.ResolveFactPluginAssetScope(*ctx.User, factAssetId)
	e.PanicServerErr(err)
	result, err := ds_service.ImportPluginAssetImageInScope(*ctx.User, scope, req.CollectionKey, imageID)
	e.PanicServerErr(err)
	app.Response(ctx.Gin, e.SuccessData(result))
}

// @Summary 从素材库导入 dev 插件实例图片
// @Tags 插件资源
// @Param pluginId path string true "插件ID"
// @Param version path string true "插件版本"
// @Param body body pluginAssetImportImageInput true "导入图片请求"
// @Security ApiKeyAuth
// @Router /api/v1/plugin-assets/dev/{pluginId}/{version}/import-image [post]
func PluginAssetImportImageDev(ctx *contextx.AppContext) {
	req := bindPluginAssetImportImageInput(ctx)
	imageID, err := req.parseImageID()
	e.PanicParameterError(err)
	scope, err := ds_service.ResolveDevPluginAssetScope(*ctx.User, ctx.Gin.Param("pluginId"), ctx.Gin.Param("version"))
	e.PanicServerErr(err)
	result, err := ds_service.ImportPluginAssetImageInScope(*ctx.User, scope, req.CollectionKey, imageID)
	e.PanicServerErr(err)
	app.Response(ctx.Gin, e.SuccessData(result))
}

// @Summary 保存 fact 插件实例状态
// @Tags 插件资源
// @Param factAssetId path string true "插件资产实例ID"
// @Param body body ds_service.PluginStateInput true "实例状态"
// @Security ApiKeyAuth
// @Router /api/v1/plugin-assets/fact/{factAssetId}/state [put]
func PluginAssetSaveStateFact(ctx *contextx.AppContext) {
	factAssetId, err := strconv.ParseInt(ctx.Gin.Param("factAssetId"), 10, 64)
	e.PanicParameterError(err)
	scope, err := ds_service.ResolveFactPluginAssetScope(*ctx.User, factAssetId)
	bizerr.PanicHTTP(err)
	req := bindPluginStateInput(ctx)
	var snapshot *ds_service.PluginAssetSnapshot
	if req.ResourceState != nil || req.Bindings != nil {
		snapshot, err = ds_service.SaveResourceStateInScope(*ctx.User, scope, ds_service.ResourceStateSaveInput{
			ExpectedRevision: req.ExpectedRevision,
			ResourceState:    req.ResourceState,
			Bindings:         req.Bindings,
		})
	} else {
		snapshot, err = ds_service.SaveStateInScope(*ctx.User, scope, ds_service.StateSaveInput{
			ExpectedRevision: req.ExpectedRevision,
			State:            req.State,
		})
	}
	bizerr.PanicHTTP(err)
	app.Response(ctx.Gin, e.SuccessData(snapshot))
}

// @Summary 保存 dev 插件实例状态
// @Tags 插件资源
// @Param pluginId path string true "插件ID"
// @Param version path string true "插件版本"
// @Param body body ds_service.PluginStateInput true "实例状态"
// @Security ApiKeyAuth
// @Router /api/v1/plugin-assets/dev/{pluginId}/{version}/state [put]
func PluginAssetSaveStateDev(ctx *contextx.AppContext) {
	scope, err := ds_service.ResolveDevPluginAssetScope(*ctx.User, ctx.Gin.Param("pluginId"), ctx.Gin.Param("version"))
	e.PanicServerErr(err)
	req := bindPluginStateInput(ctx)
	var snapshot *ds_service.PluginAssetSnapshot
	if req.ResourceState != nil || req.Bindings != nil {
		snapshot, err = ds_service.SaveResourceStateInScope(*ctx.User, scope, ds_service.ResourceStateSaveInput{
			ExpectedRevision: req.ExpectedRevision,
			ResourceState:    req.ResourceState,
			Bindings:         req.Bindings,
		})
	} else {
		snapshot, err = ds_service.SaveStateInScope(*ctx.User, scope, ds_service.StateSaveInput{
			ExpectedRevision: req.ExpectedRevision,
			State:            req.State,
		})
	}
	e.PanicServerErr(err)
	app.Response(ctx.Gin, e.SuccessData(snapshot))
}

// @Summary 提交 fact 插件实例资源和状态
// @Tags 插件资源
// @Param factAssetId path string true "插件资产实例ID"
// @Param payload formData string true "提交参数JSON"
// @Param files formData file false "本地资源文件"
// @Security ApiKeyAuth
// @Router /api/v1/plugin-assets/fact/{factAssetId}/commit [post]
func PluginAssetCommitFact(ctx *contextx.AppContext) {
	factAssetId, err := strconv.ParseInt(ctx.Gin.Param("factAssetId"), 10, 64)
	e.PanicParameterError(err)
	scope, err := ds_service.ResolveFactPluginAssetScope(*ctx.User, factAssetId)
	e.PanicServerErr(err)
	preparePluginAssetMultipartBody(ctx)
	req, files := bindResourceStateCommitInput(ctx)
	result, err := ds_service.CommitResourceStateInScope(*ctx.User, scope, req, files)
	e.PanicServerErr(err)
	app.Response(ctx.Gin, e.SuccessData(result))
}

// @Summary 提交 dev 插件实例资源和状态
// @Tags 插件资源
// @Param pluginId path string true "插件ID"
// @Param version path string true "插件版本"
// @Param payload formData string true "提交参数JSON"
// @Param files formData file false "本地资源文件"
// @Security ApiKeyAuth
// @Router /api/v1/plugin-assets/dev/{pluginId}/{version}/commit [post]
func PluginAssetCommitDev(ctx *contextx.AppContext) {
	scope, err := ds_service.ResolveDevPluginAssetScope(*ctx.User, ctx.Gin.Param("pluginId"), ctx.Gin.Param("version"))
	e.PanicServerErr(err)
	preparePluginAssetMultipartBody(ctx)
	req, files := bindResourceStateCommitInput(ctx)
	result, err := ds_service.CommitResourceStateInScope(*ctx.User, scope, req, files)
	e.PanicServerErr(err)
	app.Response(ctx.Gin, e.SuccessData(result))
}

// @Summary 提交 draft 插件实例资源和状态
// @Tags 插件资源
// @Param releaseId path string true "发布记录ID"
// @Param draftId path string true "草稿ID"
// @Param payload formData string true "提交参数JSON"
// @Param files formData file false "本地资源文件"
// @Security ApiKeyAuth
// @Router /api/v1/plugin-assets/draft/{releaseId}/{draftId}/commit [post]
func PluginAssetCommitDraft(ctx *contextx.AppContext) {
	releaseId, err := strconv.ParseInt(ctx.Gin.Param("releaseId"), 10, 64)
	e.PanicParameterError(err)
	scope, err := ds_service.ResolveDraftPluginAssetScope(*ctx.User, releaseId, ctx.Gin.Param("draftId"))
	e.PanicServerErr(err)
	preparePluginAssetMultipartBody(ctx)
	req, files := bindResourceStateCommitInput(ctx)
	result, err := ds_service.CommitResourceStateInScope(*ctx.User, scope, req, files)
	e.PanicServerErr(err)
	app.Response(ctx.Gin, e.SuccessData(result))
}

// @Summary 获取当前用户的活动 draft 插件实例资源
// @Tags 插件资源
// @Param releaseId path string true "发布记录ID"
// @Security ApiKeyAuth
// @Router /api/v1/plugin-assets/draft/{releaseId}/active [get]
func PluginAssetGetActiveDraft(ctx *contextx.AppContext) {
	releaseId, err := strconv.ParseInt(ctx.Gin.Param("releaseId"), 10, 64)
	e.PanicParameterError(err)
	result, err := ds_service.GetActivePluginInstanceDraft(*ctx.User, releaseId)
	e.PanicServerErr(err)
	app.Response(ctx.Gin, e.SuccessData(result))
}

// @Summary 删除 fact 插件实例资源
// @Tags 插件资源
// @Param factAssetId path string true "插件资产实例ID"
// @Param assetId path string true "资源ID"
// @Security ApiKeyAuth
// @Router /api/v1/plugin-assets/fact/{factAssetId}/assets/{assetId} [delete]
func PluginAssetDeleteFact(ctx *contextx.AppContext) {
	factAssetId, err := strconv.ParseInt(ctx.Gin.Param("factAssetId"), 10, 64)
	e.PanicParameterError(err)
	assetId, err := strconv.ParseUint(ctx.Gin.Param("assetId"), 10, 64)
	e.PanicParameterError(err)
	scope, err := ds_service.ResolveFactPluginAssetScope(*ctx.User, factAssetId)
	e.PanicServerErr(err)
	snapshot, err := ds_service.DeletePluginAssetInScope(*ctx.User, scope, assetId)
	e.PanicServerErr(err)
	app.Response(ctx.Gin, e.SuccessData(snapshot))
}

// @Summary 删除 dev 插件实例资源
// @Tags 插件资源
// @Param pluginId path string true "插件ID"
// @Param version path string true "插件版本"
// @Param assetId path string true "资源ID"
// @Security ApiKeyAuth
// @Router /api/v1/plugin-assets/dev/{pluginId}/{version}/assets/{assetId} [delete]
func PluginAssetDeleteDev(ctx *contextx.AppContext) {
	assetId, err := strconv.ParseUint(ctx.Gin.Param("assetId"), 10, 64)
	e.PanicParameterError(err)
	scope, err := ds_service.ResolveDevPluginAssetScope(*ctx.User, ctx.Gin.Param("pluginId"), ctx.Gin.Param("version"))
	e.PanicServerErr(err)
	snapshot, err := ds_service.DeletePluginAssetInScope(*ctx.User, scope, assetId)
	e.PanicServerErr(err)
	app.Response(ctx.Gin, e.SuccessData(snapshot))
}

// @Summary 重建 fact 插件实例资源快照
// @Tags 插件资源
// @Param factAssetId path string true "插件资产实例ID"
// @Security ApiKeyAuth
// @Router /api/v1/plugin-assets/fact/{factAssetId}/snapshot/rebuild [post]
func PluginAssetRebuildSnapshotFact(ctx *contextx.AppContext) {
	factAssetId, err := strconv.ParseInt(ctx.Gin.Param("factAssetId"), 10, 64)
	e.PanicParameterError(err)
	scope, err := ds_service.ResolveFactPluginAssetScope(*ctx.User, factAssetId)
	e.PanicServerErr(err)
	snapshot, err := ds_service.RebuildPluginAssetSnapshot(scope)
	e.PanicServerErr(err)
	app.Response(ctx.Gin, e.SuccessData(snapshot))
}

// @Summary 重建 dev 插件实例资源快照
// @Tags 插件资源
// @Param pluginId path string true "插件ID"
// @Param version path string true "插件版本"
// @Security ApiKeyAuth
// @Router /api/v1/plugin-assets/dev/{pluginId}/{version}/snapshot/rebuild [post]
func PluginAssetRebuildSnapshotDev(ctx *contextx.AppContext) {
	scope, err := ds_service.ResolveDevPluginAssetScope(*ctx.User, ctx.Gin.Param("pluginId"), ctx.Gin.Param("version"))
	e.PanicServerErr(err)
	snapshot, err := ds_service.RebuildPluginAssetSnapshot(scope)
	e.PanicServerErr(err)
	app.Response(ctx.Gin, e.SuccessData(snapshot))
}

// 兼容旧 fact 路由。
func PluginAssetUpload(ctx *contextx.AppContext) {
	PluginAssetUploadFact(ctx)
}

// 兼容旧 fact 路由。
func PluginAssetImportImage(ctx *contextx.AppContext) {
	PluginAssetImportImageFact(ctx)
}

// 兼容旧 fact 路由。
func PluginAssetSaveState(ctx *contextx.AppContext) {
	PluginAssetSaveStateFact(ctx)
}

// 兼容旧 fact 路由。
func PluginAssetDelete(ctx *contextx.AppContext) {
	PluginAssetDeleteFact(ctx)
}

// 兼容旧 fact 路由。
func PluginAssetRebuildSnapshot(ctx *contextx.AppContext) {
	PluginAssetRebuildSnapshotFact(ctx)
}
