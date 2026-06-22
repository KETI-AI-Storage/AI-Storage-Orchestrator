package main

import (
	"context"
	"log"
	"os"
	"time"

	"ai-storage-orchestrator/pkg/gluesys"
)

func main() {
	// 날짜/시간 prefix 없이 깔끔한 로그를 위해 플래그 제거
	logger := log.New(os.Stdout, "", 0)
	logger.Println("===== Gluesys StubClient 데모 시작 =====")

	// StubClient 생성
	client := gluesys.NewStubClient(logger)

	// 공통 DatasetContext
	dctx := gluesys.DatasetContext{
		WorkloadName: "etri-demo-workload",
		DatasetName:  "demo-dataset",
		Namespace:    "default",
		NodeName:     "ai-storage-worker-01",
		PVCName:      "demo-pvc",
		PVName:       "demo-pv",
	}

	// 1) PrepareDataset
	logger.Println("----- 1) PrepareDataset 호출 -----")
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.PrepareDataset(ctx, dctx); err != nil {
			logger.Printf("PrepareDataset error: %v", err)
		}
	}

	// 2) ReportPodPlacement
	logger.Println("----- 2) ReportPodPlacement 호출 -----")
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.ReportPodPlacement(ctx, dctx); err != nil {
			logger.Printf("ReportPodPlacement error: %v", err)
		}
	}

	// 3) ReportDatasetUsage
	logger.Println("----- 3) ReportDatasetUsage 호출 -----")
	{
		usage := gluesys.DatasetUsage{
			TotalBytesRead:    1048576,
			TotalBytesWritten: 524288,
			AccessPattern:     "sequential",
			Note:              "demo run",
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.ReportDatasetUsage(ctx, dctx, usage); err != nil {
			logger.Printf("ReportDatasetUsage error: %v", err)
		}
	}

	// 4) ReleaseDatasetHint
	logger.Println("----- 4) ReleaseDatasetHint 호출 -----")
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.ReleaseDatasetHint(ctx, dctx); err != nil {
			logger.Printf("ReleaseDatasetHint error: %v", err)
		}
	}

	logger.Println("===== Gluesys StubClient 데모 종료 =====")
}

