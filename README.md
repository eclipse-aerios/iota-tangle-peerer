# iota-tangle-peerer
Initializes IOTA tangle peering between the K8s nodes of an aeriOS K8s domain.

# Building

```bash
make docker-build
```

# Running

## Arguments
* `--main-node-name` - Name of the node against which peering requests will be performed.
* `--iota-hornet-selector` - Label selector for IOTA hornet pods.
* `--iota-hornet-ns` - Namespace where iota hornet pods are.
* `--private-key-file` - Path to file where private key of hornet is stored (is /app/p2pstore/identity.key file of hornet container).
* `--refresh-period` - Peerer will periodically check again if peering refresh is needed. This specifies period between those refreshes. Default 5m
* `--retry-period` - Period between subsequent attempts at peering. Default 5s
* `--hornet-rest-api-port` - Port on which main node's hornet rest API is exposed. Default: 14265
* `--gossip-protocol-port` - Port of hornet gossip protocol. Included in multiaddress. Default: 15600

## Environment variables
* `MY_NODE_NAME` - name of k8s node this is running on.
* `MY_NODE_IP` - IP of k8s node this is running on.

This component needs to be ran as container in Kubernetest cluster to work. 
It needs list pod permissions for namespace specified by `--iota-hornet-namespace`.

Ideally it should be deployed as side car of hornet container.
See [iota-tangle helm chart](https://github.com/eclipse-aerios/iota-tangle/blob/main/helm/templates/hornet/daemonset.yaml)
for reference.

# How iota-tangle-peerer works

1. Builds its own multiaddess based on its node's IP (MY_NODE_IP) and PeerId (constructed based on contents of file under `--private-key-file` path)
2. Calls main node's hornet rest API for a list of its peers.
3. Searches in that list for peer with alias equal to value of MY_NODE_NAME env variable.
    * If that peer does not exist then iota-tangle-peerer calls main node's hornet rest API to create such peer in main node with constructed multiaddress and alias=MY_NODE_NAME.
    * Otherwise, iota-tangle-peerer checks that peers multiaddress (from main node's hornet rest API list response).
        * If multiaddress does not match the one that was constructed, iota-tangle-peerer makes calls to main node's hornet rest API to delete that peer and then recreate it, same as in point above.
        * Otherwise it does nothing.
4. Sleeps for refresh-period and goes to step 2.
