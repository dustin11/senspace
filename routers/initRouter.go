package routers

import (
	"log"
	"net/http"
	"net/url"
	"senspace/asset"
	"senspace/domain/ds"
	"senspace/domain/factory"
	"senspace/middleware"
	"senspace/pkg/e"
	"senspace/pkg/setting"
	"senspace/pkg/util"
	"strings"
	"time"

	"github.com/alecthomas/template"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/gin-contrib/cors"
)

func SetupRouter() *gin.Engine {
	router := gin.New()

	// 明确列出受信任的代理地址或 CIDR（不要用 "*" 或 nil 去信任所有代理）
	// 开发环境常用 localhost；生产环境请填写你的负载均衡/反向代理的 IP 或网段
	if err := router.SetTrustedProxies([]string{
		"127.0.0.1",  // localhost (dev)
		"10.0.0.0/8", // 示例：内部网段或云内网段
		// "203.0.113.5",    // 或具体代理 IP
	}); err != nil {
		log.Println("SetTrustedProxies failed:", err)
	}

	// 使用显式的 CORS 配置：允许凭证且必须指定具体 origin（不能用 "*"）
	allowedOrigins := setting.Config.App.AllowedCORSOrigins
	if len(allowedOrigins) == 0 {
		allowedOrigins = []string{"http://localhost"}
	}

	corsCfg := cors.Config{
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Plugin-Share-Token", "If-None-Match"},
		ExposeHeaders:    []string{"Content-Length", "ETag"},
		AllowCredentials: true,           // 必须启用，浏览器才能发送/接收带凭证的跨域 Cookie
		MaxAge:           12 * time.Hour, // 预检请求的缓存时间
	}
	corsCfg.AllowOriginWithContextFunc = func(c *gin.Context, origin string) bool {
		return isAllowedOrigin(c, origin, allowedOrigins, setting.IsDevLikeEnv())
	}

	router.Use(middleware.Logger(), gin.Recovery(), middleware.ErrHandler(), cors.New(corsCfg)) //middleware.Cors()
	router.NoMethod(e.HandleNotFound)
	router.NoRoute(e.HandleNotFound)
	router.GET("/", helloGinAndMethod)
	router.POST("/", helloGinAndMethod)
	router.GET("/user/:name", func(ctx *gin.Context) {
		ctx.String(http.StatusOK, "用户"+ctx.Param("name")+"已经保存")
	})
	router.POST("/user/register", func(ctx *gin.Context) {
		ctx.String(http.StatusOK, "ok")
	})

	tmpPath := "asset/statics/templates/*"
	if gin.Mode() == gin.TestMode {
		tmpPath = "./../asset/statics/templates/*"
	}
	router.LoadHTMLGlob(tmpPath)
	router.Static("asset/statics", ".assert/statics")
	router.StaticFile("/favicon.ico", "./favicon.ico")
	router.StaticFS("/avatar", http.Dir(util.RootPath()+"avatar/"))
	// 将磁盘目录挂到 /static/images 下（请求示例 /static/images/1001/xxx.jpg）
	router.StaticFS("/static/images", gin.Dir(setting.Config.App.FilePath.Image, false))
	// 用户插件实例资源：上传文件、实例状态与运行快照。
	router.StaticFS("/static/plugin-assets", gin.Dir(ds.PluginAssetsRoot(), false))
	// 数字工厂静态资产：发布模板、铸造 NFT 元数据、owner hash 索引。
	router.StaticFS("/static/factory", gin.Dir(factory.FactoryStaticRoot(), false))
	//swagger
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	SetupApiV1Router(router)

	return router
}

func htmlAssets() {
	//r := multitemplate.New()
	bytes, err := asset.Asset("temp/oldindex.html")
	if err != nil {
		log.Println(err)
		return
	}
	t, err := template.New("index").Parse(string(bytes))
	log.Println(t, err)
	//r.Add("index", t)
	//router.HTMLRender = r
}

func helloGinAndMethod(ctx *gin.Context) {
	ctx.String(http.StatusOK, "hello gin "+strings.ToLower(ctx.Request.Method)+" method")
}

// isAllowedOrigin 优先使用显式白名单；仅在开发态环境下，额外允许“请求 Host 与 Origin 完全一致”的局域网访问，
// 方便通过 192.168.x.x 这类地址从其他设备调试。生产环境不走该兜底。
func isAllowedOrigin(ctx *gin.Context, origin string, allowedOrigins []string, allowDevHostFallback bool) bool {
	normalizedOrigin := strings.TrimSpace(strings.ToLower(origin))
	if normalizedOrigin == "" {
		return true
	}

	// 第一优先级：显式配置的 CORS 白名单。
	for _, allowed := range allowedOrigins {
		if normalizedOrigin == strings.TrimSpace(strings.ToLower(allowed)) {
			return true
		}
	}

	// 第二优先级：仅开发环境启用的同源兜底。
	// 当页面就是从当前服务地址打开的，比如 http://192.168.1.107:8081，
	// 即使没有手动写进 allowedCORSOrigins，也允许通过。
	if !allowDevHostFallback {
		return false
	}

	requestOrigin := requestOriginFromContext(ctx)
	return requestOrigin != "" && normalizedOrigin == requestOrigin
}

// requestOriginFromContext 基于当前请求推导服务自身的 origin，用于开发环境下的同源兜底匹配。
// 这里会结合 TLS / X-Forwarded-Proto 判断 http 或 https，避免代理场景下协议判断错误。
func requestOriginFromContext(ctx *gin.Context) string {
	if ctx == nil || ctx.Request == nil {
		return ""
	}

	scheme := "http"
	if ctx.Request.TLS != nil || strings.EqualFold(ctx.GetHeader("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}

	// 优先使用代理透传的 X-Forwarded-Host，保留浏览器访问时的原始 host:port。
	// 例如从另一台电脑访问 http://192.168.1.107:8081 时，需要保留 :8081，
	// 否则会错误地还原成 http://192.168.1.107，和浏览器 Origin 不一致。
	host := strings.TrimSpace(ctx.GetHeader("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(ctx.Request.Host)
	}
	if host == "" {
		return ""
	}

	return strings.ToLower((&url.URL{
		Scheme: scheme,
		Host:   host,
	}).String())
}
