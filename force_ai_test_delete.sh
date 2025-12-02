for ns in $(kubectl get ns | awk '/ai-test-/{print $1}'); do
  echo "Force deleting namespace: $ns"
  kubectl delete ns $ns --force --grace-period=0
done
