package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"butterfly.orx.me/core/app"
	"github.com/gin-gonic/gin"

	"github.com/kongken/kapi/internal/can"
	"github.com/kongken/kapi/internal/config"
	"github.com/kongken/kapi/internal/flight"
	apihttp "github.com/kongken/kapi/internal/http"
	"github.com/kongken/kapi/internal/pvg"
	"github.com/kongken/kapi/internal/szx"
)

const szxIngestTokenEnvironmentVariable = "KAPI_SZX_INGEST_TOKEN"

func main() {
	svcConfig := &config.ServiceConfig{}
	szxIngestToken := os.Getenv(szxIngestTokenEnvironmentVariable)

	appConfig := &app.Config{
		Namespace: "auto",
		Service:   "kapi",
		Config:    svcConfig,
		Router: func(r *gin.Engine) {
			apihttp.RegisterAllWithOptions(r, http.DefaultClient, apihttp.RegisterOptions{
				SZXIngestToken: szxIngestToken,
			})
		},
		InitFunc: []func() error{
			startDailyFlightSync(svcConfig, szxIngestToken),
		},
	}

	application := app.New(appConfig)
	application.Run()
}

func startDailyFlightSync(svcConfig *config.ServiceConfig, szxIngestToken string) func() error {
	return func() error {
		collectionMode := svcConfig.SZX.CollectionMode
		if collectionMode == "" {
			collectionMode = "pull"
		}

		syncer := flight.NewSyncer()
		switch collectionMode {
		case "pull":
			syncer.Register("szx", szx.NewDefaultClient())
		case "push":
			if szxIngestToken == "" {
				return fmt.Errorf("%s is required when szx.collection_mode is push", szxIngestTokenEnvironmentVariable)
			}
		default:
			return fmt.Errorf("unsupported szx.collection_mode %q", collectionMode)
		}
		syncer.Register("can", can.NewDefaultClient())
		syncer.Register("pvg", pvg.NewDefaultClient())

		intervalStr := svcConfig.SZX.DailySyncInterval
		if intervalStr == "" {
			intervalStr = "30m"
		}
		interval, err := time.ParseDuration(intervalStr)
		if err != nil {
			slog.Error("invalid daily_sync_interval, using default 30m", "value", intervalStr, "error", err)
			interval = 30 * time.Minute
		}

		slog.Info("configured SZX collection", "mode", collectionMode)
		go syncer.StartDailySync(context.Background(), interval)
		return nil
	}
}
