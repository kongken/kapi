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

	syncer := flight.NewSyncer()
	syncer.Register("szx", szx.NewDefaultClient())

	appConfig := &app.Config{
		Namespace: "auto",
		Service:   "kapi",
		Config:    svcConfig,
		Router: func(r *gin.Engine) {
			apihttp.RegisterRoutes(r, http.DefaultClient)
		},
		InitFunc: []func() error{
			startFlightSync(svcConfig, syncer),
		},
	}

	application := app.New(appConfig)
	application.Run()
}

func startFlightSync(svcConfig *config.ServiceConfig, syncer *flight.Syncer) func() error {
	return func() error {
		intervalStr := svcConfig.SZX.SyncInterval
		if intervalStr == "" {
			intervalStr = "5m"
		}
		interval, err := time.ParseDuration(intervalStr)
		if err != nil {
			slog.Error("invalid sync_interval, using default 5m", "value", intervalStr, "error", err)
			interval = 5 * time.Minute
		}

		go syncer.StartSync(context.Background(), interval)
		return nil
	}
}
