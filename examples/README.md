# Example files

There are several example yaml files in this folder, each of them will create
an autoscaler Deployment watches and resizes the replicas of the nginx server.
They are using different control modes.

Use below commands to create / delete one of the example:
```
kubectl create -f linear.yaml
...
kubectl delete -f linear.yaml
```
P.S. You need to delete the created configMap explicitly when using
*-defaultparams.yaml.

# RBAC configurations

RBAC authentication has been enabled by default in Kubernetes 1.6+. You will need
to create the following RBAC resources to give the controller the permissions to
function correctly.

Use below commands to create / delete the RBAC resources:
```
kubectl create -f RBAC-configs.yaml
...
kubectl delete -f RBAC-configs.yaml
```

RBAC documentation: http://kubernetes.io/docs/admin/authorization/
