#!/bin/bash
# 99. 테스트 리소스 정리

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=========================================="
echo " 테스트 리소스 정리"
echo "=========================================="

kubectl delete -f "${SCRIPT_DIR}/01-test-pods.yaml" --ignore-not-found

echo "정리 완료"
