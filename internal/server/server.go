package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"retry-guardian/internal/config"
	"retry-guardian/internal/handler"
	"retry-guardian/internal/rules"
	"retry-guardian/internal/store"
)

// New wires all routes and middleware into an http.Server ready to ListenAndServe.
func New(cfg config.ServerConfig, st *store.Store, table *rules.Table) *http.Server {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(requestLogger())
	router.Use(gin.Recovery())

	evaluate := handler.NewEvaluateHandler(st, table)
	record := handler.NewRecordHandler(st, table)
	health := handler.NewHealthHandler(st)
	rulesH := handler.NewRulesHandler(table)

	v1 := router.Group("/v1/retry-guard")
	{
		v1.POST("/evaluate", evaluate.Handle)
		v1.POST("/record", record.Handle)
	}

	internal := router.Group("/v1/internal/retry-guard")
	{
		internal.GET("/rules", rulesH.Handle)
	}

	router.GET("/health", health.Live)
	router.GET("/ready", health.Ready)

	return &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
		)
	}
}
