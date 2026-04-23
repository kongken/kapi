package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"butterfly.orx.me/core/app"
	"github.com/gin-gonic/gin"

	"github.com/kongken/kapi/internal/config"
	"github.com/kongken/kapi/internal/flight"
	apihttp "github.com/kongken/kapi/internal/http"
	"github.com/kongken/kapi/internal/szx"
)

func main() {
	svcConfig := &config.ServiceConfig{}

	appConfig := &app.Config{
		Namespace: "auto",
		Service:   "kapi",
		Config:    svcConfig,
		Router: func(r *gin.Engine) {
			apihttp.RegisterRoutes(r, http.DefaultClient)
		},
	}

	application := app.New(appConfig)

	// Start flight sync background task
	intervalStr := svcConfig.SZX.SyncInterval
	if intervalStr == "" {
		intervalStr = "5m"
	}
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		slog.Error("invalid sync_interval, using default 5m", "value", intervalStr, "error", err)
		interval = 5 * time.Minute
	}

	syncer := flight.NewSyncer()
	syncer.Register("szx", szx.NewDefaultClient())
	go syncer.StartSync(context.Background(), interval)

	application.Run()
}
