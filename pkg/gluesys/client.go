package gluesys

import (
	"context"
	"log"
)

// StubClient 는 실제 Gluesys MantaFS API 를 호출하지 않고,
// log.Printf 만으로 호출 내용을 상세하게 출력하는 stub 구현이다.
// 발표·데모 환경에서 end-to-end 흐름을 눈으로 확인하는 용도로 사용한다.
type StubClient struct {
	logger *log.Logger
}

// NewStubClient 는 발표/데모용 stub 클라이언트를 생성한다.
// logger 가 nil 이면 log.Default() 를 사용한다.
func NewStubClient(logger *log.Logger) *StubClient {
	if logger == nil {
		logger = log.Default()
	}
	return &StubClient{logger: logger}
}

func (c *StubClient) PrepareDataset(ctx context.Context, dctx DatasetContext) error {
	c.logger.Printf(
		"[Gluesys][PrepareDataset] 데이터 준비 요청\n"+
			"  workload_name = %q\n"+
			"  dataset_name  = %q\n"+
			"  namespace     = %q\n"+
			"  node_name     = %q\n"+
			"  pvc_name      = %q\n"+
			"  pv_name       = %q\n",
		orNA(dctx.WorkloadName),
		orNA(dctx.DatasetName),
		orNA(dctx.Namespace),
		orNA(dctx.NodeName),
		orNA(dctx.PVCName),
		orNA(dctx.PVName),
	)
	return nil
}

func (c *StubClient) ReleaseDatasetHint(ctx context.Context, dctx DatasetContext) error {
	c.logger.Printf(
		"[Gluesys][ReleaseDatasetHint] 데이터 사용 종료 힌트\n"+
			"  workload_name = %q\n"+
			"  dataset_name  = %q\n"+
			"  namespace     = %q\n"+
			"  node_name     = %q\n"+
			"  pvc_name      = %q\n"+
			"  pv_name       = %q\n",
		orNA(dctx.WorkloadName),
		orNA(dctx.DatasetName),
		orNA(dctx.Namespace),
		orNA(dctx.NodeName),
		orNA(dctx.PVCName),
		orNA(dctx.PVName),
	)
	return nil
}

func (c *StubClient) ReportPodPlacement(ctx context.Context, dctx DatasetContext) error {
	c.logger.Printf(
		"[Gluesys][ReportPodPlacement] Pod 배치 결과 보고\n"+
			"  workload_name = %q\n"+
			"  dataset_name  = %q\n"+
			"  namespace     = %q\n"+
			"  node_name     = %q (실제 배치 노드)\n"+
			"  pvc_name      = %q\n"+
			"  pv_name       = %q\n",
		orNA(dctx.WorkloadName),
		orNA(dctx.DatasetName),
		orNA(dctx.Namespace),
		orNA(dctx.NodeName),
		orNA(dctx.PVCName),
		orNA(dctx.PVName),
	)
	return nil
}

func (c *StubClient) ReportDatasetUsage(ctx context.Context, dctx DatasetContext, usage DatasetUsage) error {
	c.logger.Printf(
		"[Gluesys][ReportDatasetUsage] 데이터 사용 정보 보고\n"+
			"  workload_name       = %q\n"+
			"  dataset_name        = %q\n"+
			"  namespace           = %q\n"+
			"  node_name           = %q\n"+
			"  pvc_name            = %q\n"+
			"  pv_name             = %q\n"+
			"  total_bytes_read    = %d\n"+
			"  total_bytes_written = %d\n"+
			"  access_pattern      = %q\n"+
			"  note                = %q\n",
		orNA(dctx.WorkloadName),
		orNA(dctx.DatasetName),
		orNA(dctx.Namespace),
		orNA(dctx.NodeName),
		orNA(dctx.PVCName),
		orNA(dctx.PVName),
		usage.TotalBytesRead,
		usage.TotalBytesWritten,
		orNA(usage.AccessPattern),
		orNA(usage.Note),
	)
	return nil
}

// 빈 문자열인 경우 "n/a" 로 치환하여 로그 가독성을 높인다.
func orNA(s string) string {
	if s == "" {
		return "n/a"
	}
	return s
}

