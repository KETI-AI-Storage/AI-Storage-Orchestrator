#!/bin/bash
# 05. Weighted 전략 테스트
# 리소스별 가중치 기반 복합 점수 계산

API_URL="${API_URL:-http://localhost:8080}"

echo "=========================================="
echo " [05] Weighted 전략 테스트"
echo "=========================================="

RESPONSE=$(curl -s -X POST "${API_URL}/api/v1/loadbalancing" \
  -H "Content-Type: application/json" \
  -d '{
    "strategy": "weighted",
    "cpu_threshold": 75,
    "memory_threshold": 75,
    "gpu_threshold": 70,
    "max_migrations_per_cycle": 5
  }')

JOB_ID=$(echo "$RESPONSE" | jq -r '.loadbalancing_id')
echo "Job ID: ${JOB_ID}"
echo "실행 중..."

sleep 3

curl -s "${API_URL}/api/v1/loadbalancing/${JOB_ID}" | jq .
