#!/bin/bash
# 03. Load Spreading 전략 테스트
# 전체 노드에 부하를 균등 분산

API_URL="${API_URL:-http://localhost:8080}"

echo "=========================================="
echo " [03] Load Spreading 전략 테스트"
echo "=========================================="

RESPONSE=$(curl -s -X POST "${API_URL}/api/v1/loadbalancing" \
  -H "Content-Type: application/json" \
  -d '{
    "strategy": "load_spreading",
    "cpu_threshold": 80,
    "memory_threshold": 80,
    "max_migrations_per_cycle": 5
  }')

JOB_ID=$(echo "$RESPONSE" | jq -r '.loadbalancing_id')
echo "Job ID: ${JOB_ID}"
echo "실행 중..."

sleep 3

curl -s "${API_URL}/api/v1/loadbalancing/${JOB_ID}" | jq .
