# k8s-gentoken

This project will generate a kubeadm command to join a k8s cluster

## How does it works

It is a http server. It will reply a request with a valid kubeadm join command to include a node to an existing cluster.

## Input

```bash
curl --fail -s -XPOST --header "format: text" "http://localhost:8000/join"
```

## Output


