# KCP Ingress

PoC related to <https://github.com/kcp-dev/kcp/issues/75>

## Getting Started

Clone the repo and run:

```bash
make local-setup
```

This script will:

- build all the binaries
- deploy two kubernetes 1.18 clusters locally.
- deploy and configure the ingress controllers in each cluster.
- start the KCP server.

Once the script is done, open a new terminal, and from the root of the project, you should start the ingress controller:

```bash
./bin/ingress-controller -kubeconfig .kcp/data/admin.kubeconfig
```

Now you can create a new ingress resource from the root of the project:

```bash
export KUBECONFIG=.kcp/data/admin.kubeconfig
kubectl create namespace default
kubectl apply -n default -f samples/ingress.yaml
```

## Overall diagram

```
                    ┌───────────────────────────────────────────────────────────────┐                                                                                                                            
                    │ KCP                                                           │                                                                                                                            
                    │              ┌────────────────────────┐                       │                                                                                                                            
                    │              │                        │                       │                                          ┌───────────────────────────────┐                                                 
                    │      ┌───────│ KCP-Ingress Controller │──────Creates──┐       │                                          │                               │                                                 
                    │      │       │                        │               │       │                                          │            ┌────────────────┐ │                                                 
                    │      │       └────────────────────────┘               │       │                                          │        ┌─▶ │  Leaf Ingress  │ │                                                 
                    │      │                    │                           ▼       │         Sync Object and status           │        │   └────────────────┘ │                                                 
                    │      │                    │              ┌────────────────────┴───┐                                      │        │                   ┌──┴───────┐                                         
                    │      │                    ▼              │                        │                             ┌────────┴─────┐  │                   │          │                                         
┌────────────────┐  │      │       ┌────────────────────────┐  │      Leaf Ingress      │◀───────────────────────────▶│    Syncer    │──┘ k8s cluster┌─────▶│ Gateway  │◀─────────┐                              
│Ingress         │  │      │       │                        │  │                        │                             └────────┬─────┘               │      │          │          │                              
│HTTPRoute       │──┼──────┼──────▶│      Root Ingress      │  ├────────────────────────┤                                      │                     │      └──┬───────┘          │                              
│Route           │  │      │       │                        │  │                        │                                      │                     │         │                  │                              
└────────────────┘  │      │       └────────────────────────┘  │      Leaf Ingress      │◀───────────────────┐                 │       ┌───────────────────────┴──┐               │                              
                    │      │                    ▲              │                        │                    │                 │       │                          │               │                              
                    │      │                    │              ├────────────────────────┤                    │                 │       │  gateway-api controller  │               │                              
                    │   On Ready                │              │                        │                    │                 └───────┤                          │               │                              
                    │   Creates                 │              │      Leaf Ingress      │◀──────────────┐    │                         └──────────────────────────┘               │                              
                    │      │                    │              │                        │               │    │                 ┌────────────────────────────────┐                 │                              
                    │      │                    │              └────────────────────┬───┘               │    │                 │              ┌────────────────┐│                 │                              
                    │      │                    │                           │       │                   │    │                 │          ┌─▶ │  Leaf Ingress  ││                 │           ┌─────────────────┐
                    │      │                    │                           │       │                   │    │          ┌──────┴───────┐  │   └────────────────┘│                 │           │                 │
                    │      │                    └───────────────────────────┘       │                   │    └─────────▶│    Syncer    │──┘                     │                 │           │                 │
                    │      │                              Merge Status              │                   │               └──────┬───────┘                    ┌───┴──────┐          │           │   Global load   │
                    │      │                                                        │                   │                      │                            │          │          │           │    balancer     │
                    │      │                                                        │                   │                      │          k8s cluster ┌────▶│ Gateway  │◀─────────┼───────────│                 │
                    │      │  ┌────────────────────────┐                            │                   │                      │                      │     │          │          │           │   ALB/NLB...    │
                    │      │  │                        │                            │                   │                      │                      │     └───┬──────┘          │           │                 │
                    │      └─▶│ Global Ingress Object  │◀──┐                        │                   │                      │                      │         │                 │           │                 │
                    │         │                        │   │                        │                   │                      │                      │         │                 │           └─────────────────┘
                    │         └────────────────────────┘   │                        │                   │                      │        ┌───────────────────────┴──┐              │                    ▲         
                    │                                      │                        │                   │                      │        │                          │              │                    │         
                    │                                      │                        │                   │                      └────────┤  gateway-api controller  │              │                    │         
                    │                        ┌──────────────────────────┐           │                   │                               │                          │              │                    │         
                    │                        │   Global Load Balancer   │           │                   │                               └──────────────────────────┘              │                    │         
                    └────────────────────────┤        Controller        ├───────────┘                   │                                                                         │                    │         
                                             └──────────────────────────┘                               │                      ┌───────────────────────────────┐                  │                    │         
                                                           │                                            │                      │             ┌────────────────┐│                  │                    │         
                                                           │                                            │                      │          ┌─▶│  Leaf Ingress  ││                  │                    │         
                                                           │                                            │               ┌──────┴───────┐  │  └────────────────┘│                  │                    │         
                                                           │                                            └──────────────▶│    Syncer    │──┘                    │                  │                    │         
                                                           │                                                            └──────┬───────┘                    ┌──┴───────┐          │                    │         
                                                           │                                                                   │                            │          │          │                    │         
                                                           │                                                                   │          k8s cluster  ┌───▶│ Gateway  │◀─────────┘                    │         
                                                           │                                                                   │                       │    │          │                               │         
                                                           │                                                                   │                       │    └──┬───────┘                               │         
                                                           │                                                                   │                       │       │                                       │         
                                                           │                                                                   │                       │       │                                       │         
                                                           │                                                                   │         ┌─────────────────────┴────┐                                  │         
                                                           │                                                                   │         │                          │                                  │         
                                                           │                                                                   └─────────┤  gateway-api controller  │                                  │         
                                                           │                                                                             │                          │                                  │         
                              ┌──────────────────┐         │                                                                             └──────────────────────────┘                                  │         
                              │                  │         │                                                                                                                                           │         
                              │                  │         │                                                                                                                                           │         
                              │       DNS        │◀────────┴───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘         
                              │                  │                                                                                                                                                               
                              │                  │                                                                                                                                                               
                              └──────────────────┘                                                                                                                                                               
```
