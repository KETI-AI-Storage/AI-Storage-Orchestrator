#!/bin/bash
# 02. Least Loaded 전략 테스트
# 가장 부하가 낮은 노드로 Pod 이동 계획 수립

API_URL="${API_URL:-http://localhost:8080}"

echo "=========================================="
echo " [02] Least Loaded 전략 테스트"
echo "=========================================="

RESPONSE=$(curl -s -X POST "${API_URL}/api/v1/loadbalancing" \
  -H "Content-Type: application/json" \
  -d '{
    "strategy": "least_loaded",
    "cpu_threshold": 80,
    "memory_threshold": 80,
    "max_migrations_per_cycle": 5
  }')

JOB_ID=$(echo "$RESPONSE" | jq -r '.loadbalancing_id')
echo "Job ID: ${JOB_ID}"
echo "실행 중..."

# 완료 대기
sleep 3

# 결과 조회
curl -s "${API_URL}/api/v1/loadbalancing/${JOB_ID}" | jq .
