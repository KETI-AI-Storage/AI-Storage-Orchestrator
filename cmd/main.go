package main

import (
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"ai-storage-orchestrator/pkg/apis"
	"ai-storage-orchestrator/pkg/config"
	"ai-storage-orchestrator/pkg/controller"
	"ai-storage-orchestrator/pkg/etri"
	"ai-storage-orchestrator/pkg/etri/pb"
	"ai-storage-orchestrator/pkg/gluesys"
	"ai-storage-orchestrator/pkg/k8s"

	"google.golang.org/grpc"
)

func main() {
	// Get configuration from environment variables
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	kubeconfig := os.Getenv("KUBECONFIG")

	// ConfigMap(config.yaml) 로드. ConfigMap 이 optional 이라 파일이 없을 수 있는데,
	// 그 경우 Load 가 빌트인 기본값을 돌려주므로 fatal 로 막지 않고 경고만 남기고 진행한다.
	appCfg, cfgErr := config.LoadDefault()
	if cfgErr != nil {
		log.Printf("WARN: failed to load config (using built-in defaults): %v", cfgErr)
		appCfg = &config.Config{Provisioning: config.DefaultProvisioningConfig()}
	}

	log.Println("Starting AI Storage Orchestrator...")
	// Initialize Kubernetes client
	k8sClient, err := k8s.NewClient(kubeconfig)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}
	log.Println("Kubernetes client initialized successfully")

	// Initialize Gluesys integration stub client (optional, best-effort).
	gluesysClient := gluesys.NewStubClient(log.Default())
	stagePlanStore := controller.NewStageExecutionPlanStore()

	// Initialize migration controller
	migrationController := controller.NewMigrationController(k8sClient, gluesysClient, stagePlanStore)
	log.Println("Migration controller initialized")

	// Initialize autoscaling controller
	autoscalingController := controller.NewAutoscalingController(k8sClient)
	log.Println("Autoscaling controller initialized")

	// Initialize loadbalancing controller
	loadbalancingController := controller.NewLoadbalancingController(k8sClient, migrationController)
	log.Println("Loadbalancing controller initialized")

	// Initialize provisioning controller
	provisioningController := controller.NewProvisioningController(k8sClient, gluesysClient, stagePlanStore, &appCfg.Provisioning)
	log.Println("Provisioning controller initialized")

	// Initialize preemption controller
	preemptionController := controller.NewPreemptionController(k8sClient)
	log.Println("Preemption controller initialized")

	// Initialize caching controller (글로벌 캐싱)
	cachingController := controller.NewCachingController(k8sClient, gluesysClient)
	log.Println("Caching controller initialized")

	// Initialize insight controller (워크로드 시그니처 수집)
	insightController := controller.NewInsightController()
	log.Println("Insight controller initialized")

	// Initialize ETRI integration service (in-memory repository + existing k8s client)
	etriRepo := etri.NewInMemoryRepository()
	etriSvc := etri.NewService(etriRepo, k8sClient)
	etriHTTPHandler := etri.NewHTTPHandler(etriSvc)
	log.Println("ETRI integration service initialized")

	// Initialize gRPC server for ETRI integration (port 50051)
	grpcPort := os.Getenv("GRPC_PORT")
	if grpcPort == "" {
		grpcPort = "50051"
	}
	lis, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		log.Fatalf("Failed to listen on gRPC port %s: %v", grpcPort, err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterEtriIntegrationServiceServer(grpcServer, etri.NewGRPCServer(etriSvc))
	log.Printf("ETRI gRPC server starting on port %s", grpcPort)

	// Initialize HTTP API handler
	apiHandler := apis.NewHandler(migrationController, autoscalingController, loadbalancingController, provisioningController, preemptionController, cachingController, insightController, etriHTTPHandler)
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
	log.Println("  POST   /api/v1/insight/report - Receive workload signature report")
	log.Println("  GET    /api/v1/insight/signatures - List all workload signatures")
	log.Println("  GET    /api/v1/insight/signatures/:namespace/:name - Get specific signature")
	log.Println("  GET    /api/v1/insight/metrics - Get insight metrics")
	log.Println("  GET    /health - Health check")

	// Setup graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP server in goroutine
	go func() {
		if err := router.Run(":" + port); err != nil {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()

	// Start gRPC server in goroutine
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("Failed to start gRPC server: %v", err)
		}
	}()

	log.Printf("AI Storage Orchestrator is ready to handle HTTP and gRPC requests")

	// Wait for interrupt signal
	<-quit
	log.Println("Shutting down AI Storage Orchestrator...")
	grpcServer.GracefulStop()
	log.Println("Graceful shutdown completed")
}