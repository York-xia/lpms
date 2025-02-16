package v1

import (
	"lpms/app/handlers"
	"lpms/app/response"
	"lpms/app/service"
	"lpms/app/vo"
	"lpms/exception"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/mvc"
)

type WindowHandler struct {
	handlers.BaseHandler
	Svc service.WindowService
}

func NewWindowHandler() *WindowHandler {
	return &WindowHandler{
		Svc: service.GetWindowService(),
	}
}

// Create godoc
// @Summary 获取窗口期设置
// @Description 获取窗口期设置
// @Tags 审批中心 - 项目审核 - 窗口期设置
// @Success 200 {object} vo.WindowsResponse"获取窗口期成功"
// @Failure 400 {object} vo.Error "请求参数错误"
// @Failure 401 {object} vo.Error "当前用户登录令牌失效"
// @Failure 403 {object} vo.Error "当前操作无权限"
// @Failure 500 {object} vo.Error "服务器内部错误"
// @Security ApiKeyAuth
// @Router /api/v1/inspect/window/settings [get]
func (wh *WindowHandler) List(ctx iris.Context) mvc.Result {
	resp, ex := wh.Svc.List()
	if ex != nil {
		return response.Error(ex)
	}
	return response.JSON(resp)
}

// Create godoc
// @Summary 窗口期设置修改
// @Description 窗口期设置修改
// @Tags 审批中心 - 项目审核 - 窗口期设置
// @Param parameters body vo.WindowsReq true "WindowsReq"
// @Success 200  "窗口期设置修改成功"
// @Failure 400 {object} vo.Error "请求参数错误"
// @Failure 401 {object} vo.Error "当前用户登录令牌失效"
// @Failure 403 {object} vo.Error "当前操作无权限"
// @Failure 500 {object} vo.Error "服务器内部错误"
// @Security ApiKeyAuth
// @Router /api/v1/inspect/window/setting [put]
func (wh *WindowHandler) Update(ctx iris.Context) mvc.Result {
	param := &vo.WindowsReq{}
	if err := ctx.ReadJSON(param); err != nil {
		return response.Error(exception.Wrap(response.ExceptionInvalidRequestBody, err))
	}
	if ex := wh.Svc.Update(wh.UserName, param); ex != nil {
		return response.Error(ex)
	}
	return response.OK()
}

// BeforeActivation 初始化路由
func (wh *WindowHandler) BeforeActivation(b mvc.BeforeActivation) {
	// b.Handle(iris.MethodPost, "/window/setting", "Create")
	b.Handle(iris.MethodGet, "/window/settings", "List")
	b.Handle(iris.MethodPut, "/window/setting", "Update")
}
