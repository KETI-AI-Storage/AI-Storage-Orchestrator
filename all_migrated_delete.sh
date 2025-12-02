kubectl get pods -A | grep migrated | awk '{print $1, $2}' | \
while read ns pod; do
  kubectl delete pod $pod -n $ns --force --grace-period=0;
done

