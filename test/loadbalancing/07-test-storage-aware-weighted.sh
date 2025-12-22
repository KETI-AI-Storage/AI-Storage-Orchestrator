#!/bin/bash
# 07. Storage Aware Weighted 전략 테스트
# CPU(25%) + Memory(25%) + GPU(20%) + Storage I/O(30%) 가중치 적용

API_URL="${API_URL:-http://localhost:8080}"

echo "=========================================="
echo " [07] Storage Aware Weighted 전략 테스트"
echo "=========================================="

RESPONSE=$(curl -s -X POST "${API_URL}/api/v1/loadbalancing" \
  -H "Content-Type: application/json" \
  -d '{
    "strategy": "storage_aware_weighted",
    "cpu_threshold": 75,
    "memory_threshold": 75,
    "gpu_threshold": 70,
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
