# Horizontal cluster-proportional-autoscaler container

This container image watches over the number of schedulable nodes and cores of the cluster and resizes
the number of replicas in the required controller. This functionality may be desirable for applications
that need to be autoscaled with the size of the cluster, such as DNS and other services that scale
with the number of nodes/pods in the cluster.

Usage of cluster-proportional-autoscaler:

```
      --alsologtostderr[=false]: log to standard error as well as files
      --configmap="": ConfigMap containing our scaling parameters.
      --log-backtrace-at=:0: when logging hits line file:N, emit a stack trace
      --log-dir="": If non-empty, write log files in this directory
      --logtostderr[=false]: log to standard error instead of files
      --mode="ladder": Control mode. Default is the ladder mode, which is the only one currently.
      --namespace="": Namespace for all operations, fallback to the namespace of this autoscaler(through MY_POD_NAMESPACE env) if not specified.
      --poll-period-seconds=10: The time, in seconds, to check cluster status and perform autoscale.
      --stderrthreshold=2: logs at or above this threshold go to stderr
      --target="": Target to scale. In format: deployment/*, replicationcontroller/* or replicaset/* (not case sensitive).
      --v=0: log level for V logs
      --version[=false]: Print the version and exit.
      --vmodule=: comma-separated list of pattern=N settings for file-filtered logging
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
but it currently supports (and defaults to) to this step mode only.

# Configmap controlling parameters

The ConfigMap provides the configuration parameters, allowing on-the-fly changes without rebuilding or
restarting the scaler containers/pods.

Currently the only supported ConfigMap key value is: ladder, which is also the only supported controll mode.

Contents of key must be JSON and use "cores_to_replicas_entries" and "nodes_to_replicas_entries" as keys:

```
{ 
  "cores_to_replicas_entries":
  [
    [ 1, 1 ],
    [ 64, 3 ],
    [ 512, 5 ],
    [ 1024, 7 ],
    [ 2048, 10 ],
    [ 4096, 15 ]
  ],
  "nodes_to_replicas_entries":
  [
    [ 1, 1 ],
    [ 2, 2 ]
  ]
}
```
For instance, given a cluster comes with `100` nodes and `400` cores and it is using above configmap.  
The replicas derived from "cores_to_replicas_map" would be `3` (because `64` < `400` < `512`).  
The replicas derived from "nodes_to_replicas_map" would be `2` (because `100` > `2`).   
And we would choose the larger one `3`.

Either one of the cores_to_replicas_entries or nodes_to_replicas_entries could be omitted.

The lowest number of replicas is set to 1 in the program.

## Example deployment file

This [autoscaler-example.yaml](autoscaler-example.yaml) is an example yaml file where an autoscaler pod watch and resizes the Deployment replicas of the nginx server

# Comparisons to the Horizontal Pod Autoscaler feature

The [Horizontal Pod Autoscaler](http://kubernetes.io/docs/user-guide/horizontal-pod-autoscaling/) is a top-level Kubernetes API resource. It is a closed feedback loop autoscaler which monitors CPU utilization of the pods and scales the number of replicas automatically. It requires the CPU resources to be defined for all containers in the target pods and also requires heapster to be running to provide CPU utilization metrics.

This horizontal cluster proportional autoscaler is a DYI container (because it is not a Kubernetes API resource) that provides a simple control loop that watches the cluster size and scales the target controller. The actual CPU or memory utilization of the target controller pods is not an input to the control loop, the sole inputs are number of schedulable cores and nodes in the cluster.
There is no requirement to run heapster and/or provide CPU resource limits as in HPAs.

The configmap provides the operator with the ability to tune the replica scaling explicitly.
