#!/bin/bash
# 00. 전체 Loadbalancing 전략 테스트 실행
# 모든 전략을 순차적으로 테스트

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
API_URL="${API_URL:-http://localhost:8080}"

echo "============================================"
echo " Loadbalancing Operator 전략 테스트"
echo "============================================"
echo "API: ${API_URL}"
echo ""

# 1. 테스트 Pod 배포
echo "[Step 1] 테스트 Pod 배포"
kubectl apply -f "${SCRIPT_DIR}/01-test-pods.yaml"
echo "Pod 안정화 대기 (30초)..."
sleep 30

# 2. 각 전략 테스트
echo ""
echo "[Step 2] 전략별 테스트 실행 (실제 마이그레이션 수행)"

for script in "${SCRIPT_DIR}"/0[2-7]-test-*.sh; do
  echo ""
  bash "$script"
  echo ""
  echo "마이그레이션 완료 대기 (60초)..."
  sleep 60
done

# 3. 결과 조회
echo ""
echo "[Step 3] 전체 작업 목록 조회"
curl -s "${API_URL}/api/v1/loadbalancing" | jq .

# 4. 메트릭 조회
echo ""
echo "[Step 4] 메트릭 조회"
curl -s "${API_URL}/api/v1/loadbalancing/metrics" | jq .

echo ""
echo "============================================"
echo " 테스트 완료"
echo "============================================"
