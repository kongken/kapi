package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"butterfly-starter/internal/szx"
)

func RegisterRoutes(r *gin.Engine, httpClient szx.HTTPDoer) {
	szxClient := szx.NewClient(httpClient)

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "healthy",
		})
	})

	r.GET("/ready", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ready",
		})
	})

	api := r.Group("/api/v1")
	api.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})
	api.GET("/szx/departures", handleSZXFlightInfo(szxClient, "departure"))
	api.GET("/szx/arrivals", handleSZXFlightInfo(szxClient, "arrival"))
}

func handleSZXFlightInfo(client *szx.Client, direction string) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := szx.DefaultQuery(szx.Query{
			Type:        c.Query("type"),
			CurrentDate: c.Query("currentDate"),
			CurrentTime: c.Query("currentTime"),
			FlightNo:    c.Query("flightNo"),
		})

		if err := szx.ValidateQuery(query); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_query",
				"message": err.Error(),
			})
			return
		}

		response, err := client.Fetch(c.Request.Context(), direction, query)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{
				"error":   "upstream_error",
				"message": err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, response)
	}
}
