#!/usr/bin/env bash
set -e

echo ">>> Scanning Terminating namespaces..."
NS_LIST=$(kubectl get ns | awk '$2=="Terminating"{print $1}')

if [ -z "$NS_LIST" ]; then
  echo ">>> No Terminating namespaces found. Nothing to do."
  exit 0
fi

echo ">>> Found Terminating namespaces:"
echo "$NS_LIST"
echo

for ns in $NS_LIST; do
  echo ">>> Forcing finalize namespace: $ns"

  kubectl get namespace "$ns" -o json \
  | jq '.spec.finalizers = []' \
  | kubectl replace --raw "/api/v1/namespaces/$ns/finalize" -f - \
  || echo "    [WARN] failed to finalize $ns"
done

echo ">>> Done. Check again with: kubectl get ns"

