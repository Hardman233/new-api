package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetVideoRouter(router *gin.Engine) {
	xaiPublicFiles := router.Group("/xai/files")
	xaiPublicFiles.Use(middleware.RouteTag("relay"))
	{
		xaiPublicFiles.GET("/:file_id/content", controller.XAIFilePublicContent)
	}

	// Official xAI gRPC calls use this bridge to execute the platform's token
	// authentication and audit middleware before file operations run.
	xaiGRPCBridge := router.Group("/_xai/grpc")
	xaiGRPCBridge.Use(middleware.RouteTag("relay"))
	xaiGRPCBridge.Use(middleware.TokenAuth(), middleware.AuditLog())
	{
		xaiGRPCBridge.POST("/:service/:method", controller.XAIGrpcAuthenticate)
	}

	// Video proxy: accepts either session auth (dashboard) or token auth (API clients)
	videoProxyRouter := router.Group("/v1")
	videoProxyRouter.Use(middleware.RouteTag("relay"))
	videoProxyRouter.Use(middleware.TokenOrUserAuth())
	{
		videoProxyRouter.GET("/videos/:task_id/content", controller.VideoProxy)
		videoProxyRouter.HEAD("/videos/:task_id/content", controller.VideoProxy)
	}

	videoV1Router := router.Group("/v1")
	videoV1Router.Use(middleware.RouteTag("relay"))
	videoV1Router.Use(middleware.TokenAuth(), middleware.Distribute())
	videoV1Router.Use(middleware.AuditLog())
	{
		videoV1Router.POST("/video/generations", controller.RelayTask)
		videoV1Router.POST("/videos/:video_id/remix", controller.RelayTask)
		videoV1Router.POST("/videos/:video_id/extend", controller.RelayTask)
		videoV1Router.POST("/videos/extensions", controller.RelayTask)
	}
	// openai compatible API video routes
	// docs: https://platform.openai.com/docs/api-reference/videos/create
	{
		videoV1Router.POST("/videos", controller.RelayTask)
	}

	// Fetches load the owning task and channel from storage, so they do not need
	// request-time channel distribution or a model field.
	videoFetchRouter := router.Group("/v1")
	videoFetchRouter.Use(middleware.RouteTag("relay"))
	videoFetchRouter.Use(middleware.TokenAuth(), middleware.AuditLog())
	{
		videoFetchRouter.GET("/video/generations/:task_id", controller.RelayTaskFetch)
		videoFetchRouter.GET("/videos/:task_id", controller.RelayTaskFetch)
	}

	klingV1Router := router.Group("/kling/v1")
	klingV1Router.Use(middleware.RouteTag("relay"))
	klingV1Router.Use(middleware.KlingRequestConvert(), middleware.TokenAuth(), middleware.Distribute())
	{
		klingV1Router.POST("/videos/text2video", controller.RelayTask)
		klingV1Router.POST("/videos/image2video", controller.RelayTask)
		klingV1Router.GET("/videos/text2video/:task_id", controller.RelayTaskFetch)
		klingV1Router.GET("/videos/image2video/:task_id", controller.RelayTaskFetch)
	}

	// Jimeng official API routes - direct mapping to official API format
	jimengOfficialGroup := router.Group("jimeng")
	jimengOfficialGroup.Use(middleware.RouteTag("relay"))
	jimengOfficialGroup.Use(middleware.JimengRequestConvert(), middleware.TokenAuth(), middleware.Distribute())
	{
		// Maps to: /?Action=CVSync2AsyncSubmitTask&Version=2022-08-31 and /?Action=CVSync2AsyncGetResult&Version=2022-08-31
		jimengOfficialGroup.POST("/", controller.RelayTask)
	}

	viduGenerateGroup := router.Group("/ent/v2")
	viduGenerateGroup.Use(middleware.RouteTag("relay"))
	viduGenerateGroup.Use(middleware.ViduRequestConvert(), middleware.TokenAuth(), middleware.ViduSubjectChannelBind(), middleware.Distribute())
	{
		viduGenerateGroup.POST("/text2video", controller.RelayTask)
		viduGenerateGroup.POST("/img2video", controller.RelayTask)
		viduGenerateGroup.POST("/start-end2video", controller.RelayTask)
		viduGenerateGroup.POST("/reference2video", controller.RelayTask)
	}

	viduOfficialGroup := router.Group("/ent/v2")
	viduOfficialGroup.Use(middleware.RouteTag("relay"))
	viduOfficialGroup.Use(middleware.ViduRequestConvert(), middleware.TokenAuth())
	{
		viduOfficialGroup.GET("/tasks/:task_id/creations", controller.ViduOfficialCreations)
		viduOfficialGroup.GET("/tasks", controller.ViduOfficialTaskList)
		viduOfficialGroup.POST("/tasks/:task_id/cancel", controller.ViduOfficialCancel)
	}

	viduProxyGroup := router.Group("")
	viduProxyGroup.Use(middleware.RouteTag("relay"))
	viduProxyGroup.Use(middleware.ViduProxyRequestPrepare(), middleware.TokenAuth(), middleware.Distribute())
	{
		viduProxyGroup.POST("/ent/v2/subjects", controller.ViduOfficialCreateSubject)
		viduProxyGroup.GET("/ent/v2/subjects", controller.ViduOfficialListSubjects)
		viduProxyGroup.PUT("/ent/v2/subjects/:subject_id", controller.ViduOfficialUpdateSubject)
		viduProxyGroup.DELETE("/ent/v2/subjects/:subject_id", controller.ViduOfficialDeleteSubject)
		viduProxyGroup.GET("/ent/v2/credits", controller.ViduOfficialProxy)
		viduProxyGroup.POST("/ent/v2/img2video-prompt-recommendation", controller.ViduOfficialProxy)
		viduProxyGroup.POST("/tools/v2/files/uploads", controller.ViduOfficialProxy)
		viduProxyGroup.POST("/tools/v2/files/uploads/:resource_id/finish", controller.ViduOfficialProxy)
	}
}
