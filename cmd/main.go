package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"ai-storage-orchestrator/pkg/apis"
	"ai-storage-orchestrator/pkg/controller"
	"ai-storage-orchestrator/pkg/k8s"
)

func main() {
	// Get configuration from environment variables
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	kubeconfig := os.Getenv("KUBECONFIG")

	log.Println("Starting AI Storage Orchestrator...")
	// Initialize Kubernetes client
	k8sClient, err := k8s.NewClient(kubeconfig)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}
	log.Println("Kubernetes client initialized successfully")

	// Initialize migration controller
	migrationController := controller.NewMigrationController(k8sClient)
	log.Println("Migration controller initialized")

	// Initialize autoscaling controller
	autoscalingController := controller.NewAutoscalingController(k8sClient)
	log.Println("Autoscaling controller initialized")

	// Initialize loadbalancing controller
	loadbalancingController := controller.NewLoadbalancingController(k8sClient, migrationController)
	log.Println("Loadbalancing controller initialized")

	// Initialize provisioning controller
	provisioningController := controller.NewProvisioningController(k8sClient)
	log.Println("Provisioning controller initialized")

	// Initialize preemption controller
	preemptionController := controller.NewPreemptionController(k8sClient)
	log.Println("Preemption controller initialized")

	// Initialize caching controller (글로벌 캐싱)
	cachingController := controller.NewCachingController(k8sClient)
	log.Println("Caching controller initialized")

	// Initialize HTTP API handler
	apiHandler := apis.NewHandler(migrationController, autoscalingController, loadbalancingController, provisioningController, preemptionController, cachingController)
	router := apiHandler.SetupRoutes()

	log.Printf("HTTP server starting on port %s", port)
	log.Println("Available endpoints:")
	log.Println("  POST   /api/v1/migrations - Start new pod migration")
	log.Println("  GET    /api/v1/migrations/:id - Get migration details")
	log.Println("  GET    /api/v1/migrations/:id/status - Get migration status")
	log.Println("  GET    /api/v1/metrics - Get migration metrics")
	log.Println("  POST   /api/v1/autoscaling - Create autoscaler")
	log.Println("  GET    /api/v1/autoscaling/:id - Get autoscaler details")
	log.Println("  DELETE /api/v1/autoscaling/:id - Delete autoscaler")
	log.Println("  GET    /api/v1/autoscaling - List all autoscalers")
	log.Println("  GET    /api/v1/autoscaling/metrics - Get autoscaling metrics")
	log.Println("  POST   /api/v1/loadbalancing - Start loadbalancing job")
	log.Println("  GET    /api/v1/loadbalancing/:id - Get loadbalancing details")
	log.Println("  DELETE /api/v1/loadbalancing/:id - Cancel loadbalancing job")
	log.Println("  GET    /api/v1/loadbalancing - List all loadbalancing jobs")
	log.Println("  GET    /api/v1/loadbalancing/metrics - Get loadbalancing metrics")
	log.Println("  POST   /api/v1/provisioning - Create storage provisioning")
	log.Println("  GET    /api/v1/provisioning/:id - Get provisioning details")
	log.Println("  DELETE /api/v1/provisioning/:id - Delete provisioning")
	log.Println("  GET    /api/v1/provisioning - List all provisionings")
	log.Println("  GET    /api/v1/provisioning/recommend/:workload_type - Get storage recommendations")
	log.Println("  GET    /api/v1/provisioning/metrics - Get provisioning metrics")
	log.Println("  POST   /api/v1/preemption - Start pod preemption")
	log.Println("  GET    /api/v1/preemption/:id - Get preemption details")
	log.Println("  GET    /api/v1/preemption - List all preemption jobs")
	log.Println("  GET    /api/v1/preemption/metrics - Get preemption metrics")
	log.Println("  POST   /api/v1/caching - Create cache (글로벌 캐싱)")
	log.Println("  GET    /api/v1/caching/:id - Get cache details")
	log.Println("  DELETE /api/v1/caching/:id - Delete cache")
	log.Println("  GET    /api/v1/caching - List all caches")
	log.Println("  POST   /api/v1/caching/:id/evict - Evict cache data")
	log.Println("  POST   /api/v1/caching/:id/warmup - Warmup cache")
	log.Println("  POST   /api/v1/caching/:id/migrate - Migrate cache tier")
	log.Println("  POST   /api/v1/caching/policy - Apply policy decision")
	log.Println("  GET    /api/v1/caching/metrics - Get caching metrics")
	log.Println("  GET    /health - Health check")

	// Setup graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start server in goroutine
	go func() {
		if err := router.Run(":" + port); err != nil {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()

	log.Printf("AI Storage Orchestrator is ready to handle migration requests")

	// Wait for interrupt signal
	<-quit
	log.Println("Shutting down AI Storage Orchestrator...")
	log.Println("Graceful shutdown completed")
}