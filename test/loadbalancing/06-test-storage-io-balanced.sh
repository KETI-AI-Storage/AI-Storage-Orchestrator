#!/bin/bash
# 06. Storage I/O Balanced 전략 테스트
# Storage I/O 메트릭 기반 균형 유지 (AI/ML 최적화)

API_URL="${API_URL:-http://localhost:8080}"

echo "=========================================="
echo " [06] Storage I/O Balanced 전략 테스트"
echo "=========================================="

RESPONSE=$(curl -s -X POST "${API_URL}/api/v1/loadbalancing" \
  -H "Content-Type: application/json" \
  -d '{
    "strategy": "storage_io_balanced",
    "storage_read_threshold": 500,
    "storage_write_threshold": 200,
    "storage_iops_threshold": 5000,
    "max_migrations_per_cycle": 5
  }')

JOB_ID=$(echo "$RESPONSE" | jq -r '.loadbalancing_id')
echo "Job ID: ${JOB_ID}"
echo "실행 중..."

sleep 3

curl -s "${API_URL}/api/v1/loadbalancing/${JOB_ID}" | jq .
