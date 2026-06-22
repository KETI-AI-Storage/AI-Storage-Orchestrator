package main

import (
	"log"
	"os"

	"ai-storage-orchestrator/pkg/etri"

	"github.com/gin-gonic/gin"
)

func main() {
	// 로그에 시간 prefix 제거
	log.SetFlags(0)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Println("[ETRI] ETRI integration server starting")

	// In-memory repository + nil K8s client (사용하지 않으므로 nil 허용)
	repo := etri.NewInMemoryRepository()
	svc := etri.NewService(repo, nil)
	httpHandler := etri.NewHTTPHandler(svc)

	router := gin.Default()
	router.Use(gin.Logger())
	router.Use(gin.Recovery())

	v1 := router.Group("/api/v1")
	etriGroup := v1.Group("/etri")
	httpHandler.RegisterRoutes(etriGroup)

	log.Printf("[ETRI] HTTP server listening on :%s", port)
	log.Println("[ETRI] Available endpoints:")
	log.Println("  POST /api/v1/etri/workloads/metadata")
	log.Println("  GET  /api/v1/etri/workloads/{namespace}/{name}/status")
	log.Println("  POST /api/v1/etri/workloads/result")

	if err := router.Run(":" + port); err != nil {
		log.Fatalf("[ETRI] failed to start HTTP server: %v", err)
	}
}

