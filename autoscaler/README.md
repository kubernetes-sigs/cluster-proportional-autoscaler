# Horizontal cluster-proportional-autoscaler container

This container image watches over the number of schedulable nodes and cores in the cluster and resizes
the number of replicas in the required controller. This functionality may be desirable for applications
that need to be autoscaled with the size of the cluster, such as DNS and other services that scale
with the number of nodes/pods in the cluster.

Usage of pod_nanny:
    --configmap <params>
    --namespace <namespace>
    --rc <replication-controller>
    --rs <replica set>
    --deployment <deployment>
    --verbose
```

# Implementation Details

The code in this module is a Kubernetes Golang API client that, using the default service account credentials
available to Golang clients running inside pods, it connects to the API server and polls for the number of nodes
and cores in the cluster.
The scaling parameters and data points are provided via a ConfigMap to the autoscaler and it refreshes its
parameters table every poll interval to be up to date with the latest desired scaling parameters.

## Calculation of number of replicas

The desired number of replicas is computed by looking up the number of cores and nodes using the step ladder function.
The step ladder functions use the datapoint for core and node scaling from the configmap.
The lookup which yields the higher number of replicas will be used as the target scaling number.
This may be later extended to more complex interpolation or linear/exponential scaling schemes
but it currently supports (and defaults to) to mode=step only.

# Configmap controlling parameters

The ConfigMap provides the configuration parameters, allowing on-the-fly changes without rebuilding or
restarting the scaler containers/pods.

Supported ConfigMap key values are:

## Key: simple-scalar.scale-map

Contents of key must be JSON

```
{ "cores_to_replicas_map":
    [
      [ 1, 1 ],
      [ 64, 3 ],
      [ 512, 5 ],
      [ 1024, 7 ],
      [ 2048, 10 ],
      [ 4096, 15 ],
      [ 8192, 20 ],
      [ 12288, 30 ],
      [ 16384, 40 ],
      [ 20480, 50 ],
      [ 24576, 60 ],
      [ 28672, 70 ],
      [ 32768, 80 ],
      [ 65535, 100 ],
    ],
   "nodes_to_replicas_map":
    [
      [ 1, 1 ],
      [ 2, 2 ],
    ],
}
```

## Example rc file

This [example-rc.yaml](example-rc.yaml) is an example Replication Controller where the nannies in all pods watch and resizes the RC replicas

# Comparisons to the Horizontal Pod Autoscaler feature

The [Horizontal Pod Autoscaler](http://kubernetes.io/docs/user-guide/horizontal-pod-autoscaling/) is a top-level Kubernetes API resource. It is a closed feedback loop autoscaler which monitors CPU utilization of the pods and scales the number of replicas automatically. It requires the CPU resources to be defined for all containers in the target pods and also requires heapster to be running to provide CPU utilization metrics.

This horizontal cluster proportional autoscaler is a DYI container (because it is not a Kubernetes API resource) that provides a simple control loop that watches the cluster size and scales the target controller. The actual CPU or memory utilization of the target controller pods is not an input to the control loop, the sole inputs are number of schedulable cores and nodes in the cluster.
There is no requirement to run heapster and/or provide CPU resource limits as in HPAs.

The configmap provides the operator with the ability to tune the replica scaling explicitly.
