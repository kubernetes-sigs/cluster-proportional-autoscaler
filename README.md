# Horizontal cluster-proportional-autoscaler container

[![Build Status](https://travis-ci.org/kubernetes-incubator/cluster-proportional-autoscaler.png)](https://travis-ci.org/kubernetes-incubator/cluster-proportional-autoscaler)

This container image watches over the number of schedulable nodes and cores of the cluster and resizes
the number of replicas for the required resource. This functionality may be desirable for applications
that need to be autoscaled with the size of the cluster, such as DNS and other services that scale
with the number of nodes/pods in the cluster.

Usage of cluster-proportional-autoscaler:

```
      --alsologtostderr[=false]: log to standard error as well as files
      --configmap="": ConfigMap containing our scaling parameters.
      --default-params="": Default parameters(JSON format) for auto-scaling. Will create/re-create a ConfigMap with this default params if not present.
      --log-backtrace-at=:0: when logging hits line file:N, emit a stack trace
      --log-dir="": If non-empty, write log files in this directory
      --logtostderr[=false]: log to standard error instead of files
      --mode="linear": Control mode --- linear/ladder. Default is the linear mode.
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

The desired number of replicas is computed by using the number of cores and nodes as input of the chosen controller.

This may be later extended to more complex interpolation or exponential scaling schemes
but it currently supports `linear` and `ladder` modes(default to `linear`).

# Control patterns and ConfigMap formats

The ConfigMap provides the configuration parameters, allowing on-the-fly changes without rebuilding or
restarting the scaler containers/pods.

Currently the two supported ConfigMap key value is: `ladder` and `linear`, which corresponding to two supported control mode.

## Linear Mode

Parameters in ConfigMap must be JSON and use `linear` as key. The sub-keys as below indicates:

```
data:
  linear: |-
    {
      "coresPerReplica": 2,
      "nodesPerReplica": 1,
      "min": 1,
      "max": 100
    }
```

The equation of linear control mode as below:
```
replicas = max( ceil( cores * 1/coresPerReplica ) , ceil( nodes * 1/nodesPerReplica ) )
```

Notice that both `coresPerReplica` and `nodesPerReplica` are float.

For instance, given a cluster has 4 nodes and 13 cores. With above parameters, each replica could take care of 1 node.
So we need `4 / 1 = 4` replicas to take care of all 4 nodes. And each replica could take care of 2 cores. We need `ceil(13 / 2) = 7`
replicas to take care of all 13 cores. Controller will choose the greater one, which is `7` here, as the result.

Either one of the `coresPerReplica` or `nodesPerReplica` could be omitted. Both `min` and `max ` could be omitted.
If not set, `min` would be default to 1.

The lowest number of replicas is set to 1.

## Ladder Mode

Parameters in ConfigMap must be JSON and use `ladder` as key. The sub-keys as below indicates:

```
data:
  ladder: |-
    {
      "coresToReplicas":
      [
        [ 1, 1 ],
        [ 64, 3 ],
        [ 512, 5 ],
        [ 1024, 7 ],
        [ 2048, 10 ],
        [ 4096, 15 ]
      ],
      "nodesToReplicas":
      [
        [ 1, 1 ],
        [ 2, 2 ]
      ]
    }
```

The ladder controller gives out the desired replicas count by using a step function.
The step ladder function uses the datapoint for core and node scaling from the ConfigMap.
The lookup which yields the higher number of replicas will be used as the target scaling number.

For instance, given a cluster comes with `100` nodes and `400` cores and it is using above ConfigMap.  
The replicas derived from "cores_to_replicas_map" would be `3` (because `64` < `400` < `512`).  
The replicas derived from "nodes_to_replicas_map" would be `2` (because `100` > `2`).   
And we would choose the larger one `3`.

Either one of the `coresToReplicas` or `nodesToReplicas` could be omitted. All elements in them should
be int.

The lowest number of replicas is set to 1.

## Example deployment files

There are several example yaml files in the example folder, each of them will deploy an autoscaler pod watch and resizes the Deployment replicas of the nginx server.
They are using different control modes. The autoscaler accepts default parameters passed by `--default-params` flag. Default ConfigMap will be created/re-created if default params present. Also see the example yaml files for example.

Use below command to create / delete one of the example:
```
kubectl create -f linear.yaml
...
kubectl delete -f linear.yaml
```

# Comparisons to the Horizontal Pod Autoscaler feature

The [Horizontal Pod Autoscaler](http://kubernetes.io/docs/user-guide/horizontal-pod-autoscaling/) is a top-level Kubernetes API resource. It is a closed feedback loop autoscaler which monitors CPU utilization of the pods and scales the number of replicas automatically. It requires the CPU resources to be defined for all containers in the target pods and also requires heapster to be running to provide CPU utilization metrics.

This horizontal cluster proportional autoscaler is a DIY container (because it is not a Kubernetes API resource) that provides a simple control loop that watches the cluster size and scales the target controller. The actual CPU or memory utilization of the target controller pods is not an input to the control loop, the sole inputs are number of schedulable cores and nodes in the cluster.
There is no requirement to run heapster and/or provide CPU resource limits as in HPAs.

The ConfigMap provides the operator with the ability to tune the replica scaling explicitly.
