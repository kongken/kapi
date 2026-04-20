package main

import (
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	apihttp "github.com/kongken/kapi/internal/http"
)

func main() {
	port := 8080
	if rawPort := os.Getenv("PORT"); rawPort != "" {
		parsedPort, err := strconv.Atoi(rawPort)
		if err != nil {
			log.Fatalf("invalid PORT value %q: %v", rawPort, err)
		}
		port = parsedPort
	}

	router := gin.Default()
	apihttp.RegisterRoutes(router, http.DefaultClient)

	if err := router.Run(":" + strconv.Itoa(port)); err != nil {
		log.Fatalf("run server: %v", err)
	}
}
