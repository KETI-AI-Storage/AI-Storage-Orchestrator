kubectl get pods -A | grep Terminating | awk '{print $1, $2}' | \
while read ns pod; do
  echo "Force deleting $ns/$pod"
  kubectl patch pod $pod -n $ns -p '{"metadata":{"finalizers":null}}' --type=merge
  kubectl delete pod $pod -n $ns --force --grace-period=0
done

