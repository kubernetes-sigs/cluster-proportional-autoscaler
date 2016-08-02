# Kubernetes Horizontal Self Scaler Project

The Kubernetes Horizontal Self Scaler Project defines functionality
that provides replication controllers, replica sets and deployments
with the ability to scale themselves horizontally using a generic sidecar container.

The first version provides a sync loop that will change the number of replicas
based on the number of schedulable nodes and cores in the cluster.

## Community, discussion, contribution, and support

Learn how to engage with the Kubernetes community on the [community page](http://kubernetes.io/community/).

You can reach the maintainers of this project at:

- Slack: #kubernetes-dev
- Mailing List: https://groups.google.com/forum/#!forum/kubernetes-dev

### Code of conduct

Participation in the Kubernetes community is governed by the [Kubernetes Code of Conduct](code-of-conduct.md).
