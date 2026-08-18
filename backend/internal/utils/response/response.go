package response

import "github.com/gin-gonic/gin"

// 统一返回状态码常量
const (
	CodeSuccess = 200 // 成功
	CodeFail    = 400 // 业务失败
	CodeAuth    = 401 // 未登录/令牌失效
	CodeServer  = 500 // 服务器内部错误
)

// Response 定义统一的响应结构体
type Response struct {
	Code    int         `json:"code"`    // 状态码
	Message string      `json:"message"` // 消息
	Data    interface{} `json:"data"`    // 数据
}

// 系列辅助函数，用于快速生成不同类型的响应
func SuccessResponse(ctx *gin.Context, data interface{}) {
	ctx.JSON(CodeSuccess, Response{
		Code:    CodeSuccess,
		Message: "成功",
		Data:    data,
	})
}

func FailResponse(ctx *gin.Context, message string) {
	ctx.JSON(CodeFail, Response{
		Code:    CodeFail,
		Message: message,
		Data:    nil,
	})
}

func FailAuthResponse(ctx *gin.Context, message string) {
	ctx.JSON(CodeAuth, Response{
		Code:    CodeAuth,
		Message: message,
		Data:    nil,
	})
}

func ServerErrorResponse(ctx *gin.Context, message string) {
	ctx.JSON(CodeServer, Response{
		Code:    CodeServer,
		Message: message,
		Data:    nil,
	})
}
