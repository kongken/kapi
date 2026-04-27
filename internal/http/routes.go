package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kongken/kapi/internal/airports"
	"github.com/kongken/kapi/internal/szx"
)

func RegisterRoutes(r *gin.Engine, httpClient szx.HTTPDoer) {
	szxClient := szx.NewClient(httpClient)
	registry := airports.NewRegistry(airports.NewSZXProvider(httpClient))

	r.Use(corsMiddleware())

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

	api := r.Group("/api")
	v1 := api.Group("/v1")
	v1.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})
	v1.GET("/szx/departures", handleSZXFlightInfo(szxClient, "departure"))
	v1.GET("/szx/arrivals", handleSZXFlightInfo(szxClient, "arrival"))
	v1.GET("/szx/weather", handleSZXWeather(szxClient))

	v2 := api.Group("/v2")
	v2.GET("/airports/:airport/flights", handleAirportFlights(registry))
	v2.GET("/airports/:airport/weather", handleAirportWeather(registry))
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		headers := c.Writer.Header()
		headers.Set("Access-Control-Allow-Origin", "*")
		headers.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		headers.Set("Access-Control-Allow-Headers", "Origin, Content-Type, Content-Length, Accept, Accept-Encoding, Authorization, X-Requested-With")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func handleAirportFlights(registry *airports.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		provider, ok := registry.Get(c.Param("airport"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "airport_not_supported",
				"message": "airport provider not found",
			})
			return
		}

		query := airports.FlightQuery{
			Direction: c.Query("direction"),
			Lang:      c.DefaultQuery("lang", "cn"),
			Date:      c.Query("date"),
			Time:      c.Query("time"),
			FlightNo:  c.Query("flightNo"),
		}
		if err := airports.ValidateFlightQuery(query); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_query",
				"message": err.Error(),
			})
			return
		}

		response, err := provider.GetFlights(c.Request.Context(), query)
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

func handleAirportWeather(registry *airports.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		provider, ok := registry.Get(c.Param("airport"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "airport_not_supported",
				"message": "airport provider not found",
			})
			return
		}

		response, err := provider.GetWeather(c.Request.Context())
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

func handleSZXWeather(client *szx.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		response, err := client.FetchWeather(c.Request.Context())
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
