package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/config"
	"github.com/never/zero-api/internal/handler"
	"github.com/never/zero-api/internal/middleware"
	"github.com/never/zero-api/internal/proxy"
	"github.com/never/zero-api/internal/store"
	"github.com/never/zero-api/internal/upstream"
)

//go:embed web/dist/index.html web/dist/assets/*
var webFS embed.FS

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// 加载配置
	cfg, err := config.LoadDefault()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	log.Printf("配置加载成功: API=%s 代理=%s DB=%s",
		cfg.Server.Addr(), cfg.Proxy.Addr(), cfg.Database.Path)

	// 确保数据目录
	os.MkdirAll(filepath.Dir(cfg.Database.Path), 0755)
	os.MkdirAll("certs", 0755)

	// 初始化数据库
	db, err := store.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer db.Close()

	svc := store.NewService(db)
	// 启动 usage 批量写入协程
	store.InitUsageBuffer(db)
	requestTimeout := time.Duration(cfg.Upstream.RequestTimeoutSeconds) * time.Second

	// 初始化上游同步器（传入配置文件中的模型默认值）
	syncer := upstream.NewSyncer(svc.Channel, svc.Model, cfg.ModelDefaults)

	// 初始化处理器
	channelH := handler.NewChannelHandler(svc.Channel, svc.Model)
	modelH := handler.NewModelHandler(svc.Model)
	usageH := handler.NewUsageHandler(svc.Usage)
	proxyH := handler.NewProxyHandler(svc.Channel, svc.Model, svc.Usage, svc.APIKey, svc.ProxyConfig)
	proxyConfigH := handler.NewProxyConfigHandler(svc.ProxyConfig, "certs")
	// 代理配置更新后通知 ProxyHandler 刷新缓存
	proxyConfigH.SetOnUpdate(proxyH.InvalidateProxyConfig)
	syncH := handler.NewSyncHandler(syncer, svc.Model)
	authH := handler.NewAuthHandler(cfg.Auth.Username, cfg.Auth.Password, cfg.Auth.Secret)
	apiKeyH := handler.NewAPIKeyHandler(svc.APIKey)
	dbH := handler.NewDatabaseHandler(db, cfg.Database.Path)

	// --- API 路由 ---
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), middleware.CORS(), middleware.AuthMiddleware(cfg.Auth.Enabled, cfg.Auth.Secret))

	api := r.Group("/api")
	{
		// 认证
		api.POST("/auth/login", authH.Login)

		// 渠道管理
		api.GET("/channels", channelH.ListChannels)
		api.GET("/channels/:id", channelH.GetChannel)
		api.POST("/channels", channelH.CreateChannel)
		api.PUT("/channels/:id", channelH.UpdateChannel)
		api.DELETE("/channels/:id", channelH.DeleteChannel)
		api.POST("/channels/:id/test", syncH.TestChannel)
		api.POST("/channels/:id/sync", syncH.SyncModels)
		api.POST("/channels/:id/toggle", channelH.ToggleChannel)

		// 模型管理
		api.GET("/models", modelH.ListModels)
		api.GET("/models/:id", modelH.GetModel)
		api.PUT("/models/:id", modelH.UpdateModel)
		api.DELETE("/models/:id", modelH.DeleteModel)
		api.POST("/models/:id/toggle", modelH.ToggleModel)
		api.POST("/models/batch", modelH.BatchAction)

		// 使用统计
		api.GET("/stats/overview", usageH.GetOverview)
		api.GET("/stats/daily", usageH.GetDailyStats)
		api.GET("/stats/by-model", usageH.GetModelStats)
		api.GET("/usage/records", usageH.GetRecentRecords)

		// 代理配置
		api.GET("/proxy/config", proxyConfigH.GetConfig)
		api.PUT("/proxy/config", proxyConfigH.UpdateConfig)
		api.GET("/proxy/cert/download", proxyConfigH.DownloadCert)

		// API 密钥管理
		api.GET("/api-keys", apiKeyH.List)
		api.POST("/api-keys", apiKeyH.Create)
		api.POST("/api-keys/:id/toggle", apiKeyH.Toggle)
		api.DELETE("/api-keys/:id", apiKeyH.Delete)

		// 数据库管理
		api.GET("/database/backup", dbH.Backup)
		api.POST("/database/restore", dbH.Restore)
	}

	// OpenAI 兼容 API
	v1 := r.Group("/v1")
	{
		v1.GET("/models", proxyH.ListLocalModels)
		v1.POST("/chat/completions", proxyH.ChatCompletion)
		v1.POST("/completions", proxyH.ChatCompletion)
	}

	// 前端静态文件（SPA 路由兜底）
	webSubFS, err := fs.Sub(webFS, "web/dist")
	if err == nil {
		// 读取 index.html 到内存（SPA fallback 用）
		indexHTML, err := fs.ReadFile(webSubFS, "index.html")
		if err == nil {
			// 根路径
			r.GET("/", func(c *gin.Context) {
				c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
			})
			// 静态资源
			r.GET("/assets/*filepath", func(c *gin.Context) {
				fp := c.Param("filepath")
				data, err := fs.ReadFile(webSubFS, path.Join("assets", fp))
				if err != nil {
					// SPA 降级
					c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
					return
				}
				// 根据扩展名推断 Content-Type
				ext := path.Ext(fp)
				ct := "text/plain"
				switch ext {
				case ".js":
					ct = "application/javascript"
				case ".css":
					ct = "text/css"
				case ".html":
					ct = "text/html"
				case ".svg":
					ct = "image/svg+xml"
				case ".png":
					ct = "image/png"
				case ".json":
					ct = "application/json"
				}
				c.Data(http.StatusOK, ct, data)
			})
			// SPA 降级
			r.NoRoute(func(c *gin.Context) {
				c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
			})
		}
	}

	// 启动 API 服务（使用 http.Server 设置超时，防止慢速连接耗尽资源）
	apiServer := &http.Server{
		Addr:              cfg.Server.Addr(),
		Handler:           r,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	go func() {
		log.Printf("API 服务启动于 http://%s", cfg.Server.Addr())
		if err := apiServer.ListenAndServe(); err != nil {
			log.Fatalf("API 服务启动失败: %v", err)
		}
	}()

	// 启动代理服务（MITM）
	if cfg.Proxy.Enabled {
		go func() {
			// 初始化证书管理器
			certMgr := proxy.NewCertManager("certs")
			if err := certMgr.Init(); err != nil {
				log.Fatalf("证书初始化失败: %v", err)
			}
			log.Printf("根 CA 证书: %s", certMgr.GetRootCAPath())

			// 加载代理配置
			proxyCfg, err := svc.ProxyConfig.Get()
			interceptDomains := cfg.Proxy.InterceptDomains
			smartDomains := cfg.Proxy.SmartInterceptDomains
			if err == nil && len(proxyCfg.InterceptDomains) > 0 {
				interceptDomains = proxyCfg.InterceptDomains
				smartDomains = proxyCfg.SmartInterceptDomains
			}

			// 从 DB 配置决定 MitmAll 和认证信息
			// ★ DB 配置存在时，认证信息完全以 DB 为准（包括空=不启用）
			//    config.yaml 的 auth 仅在 DB 无记录时作为默认值
			mitmAll := false
			proxyUser := cfg.Auth.Username
			proxyPass := cfg.Auth.Password
			if err == nil {
				mitmAll = proxyCfg.MitmAll
				proxyUser = proxyCfg.ProxyUsername // DB 为空表示不启用认证
				proxyPass = proxyCfg.ProxyPassword
			}

			// 初始化请求路由器
			router := proxy.NewRequestRouter(interceptDomains, smartDomains, mitmAll)

			// 初始化代理适配器
			pAdapter := proxy.NewProxyAdapter(svc.Channel, svc.Model, svc.Usage, svc.APIKey, requestTimeout)
			// 加载模型映射配置
			if err == nil && len(proxyCfg.ModelMappings) > 0 {
				mappings := make(map[string]proxy.ModelMappingConfig)
				for src, m := range proxyCfg.ModelMappings {
					mappings[src] = proxy.ModelMappingConfig{
						TargetModel:     m.TargetModel,
						Name:            m.Name,
						ContextWindow:   m.ContextWindow,
						MaxOutputTokens: m.MaxOutputTokens,
						Thinking:        m.Thinking,
						ReasoningEffort: m.ReasoningEffort,
						Vision:          m.Vision,
					}
				}
				pAdapter.SetModelMappings(mappings)
				log.Printf("[代理] 已加载 %d 个模型映射", len(mappings))
			}

			// 打印代理认证状态
			if proxyUser == "" {
				log.Printf("[代理] 代理认证: 未启用")
			} else {
				log.Printf("[代理] 代理认证: 已启用 (%s:***)", proxyUser)
			}

			// 启动代理服务器
			ps := proxy.NewServer(proxy.Config{
				Host:                  cfg.Proxy.Host,
				Port:                  cfg.Proxy.Port,
				InterceptDomains:      interceptDomains,
				SmartInterceptDomains: smartDomains,
				MitmAll:               mitmAll,
				ProxyAuthUser:         proxyUser,
				ProxyAuthPass:         proxyPass,
			}, certMgr, router, pAdapter)

			if err := ps.Start(); err != nil {
				log.Fatalf("代理服务启动失败: %v", err)
			}
		}()
	}

	// 等待退出信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("正在关闭服务...")
}
