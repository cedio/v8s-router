# v8s-router
A highly available Kubernetes operator built to simplify service exposing. `Service` is a resource on Kubernetes cluster which defines policies to reach `Pod`. Although there are multiple methods (i.e. `ClusterIP`, `NodePort`, `LoadBalancer`, `ExternalName`) for exposing `Service` to external applications, technical know-how is required for familiar usage. **v8s-router** delivers `Route` as a higher level wrapper to `Service` and encapsulates those methods, while respecting [`Ingress`](https://kubernetes.io/docs/concepts/services-networking/ingress/) from the ever-growing Kubernetes community.

## Versions
| Name | Version |
|-|-|
| [Golang](https://golang.org/) | 1.15.2 |
| [Operator-sdk](https://github.com/operator-framework/operator-sdk) | 1.0.1 |
| [Helm](https://helm.sh/) | 3.3.3 |
| [bitnami/metallb](https://bitnami.com/stack/metallb-controller/helm) | 0.1.21 |
| [haproxytech/kubernetes-ingress](https://github.com/haproxytech/kubernetes-ingress) | 1.6.0 |
| [ingress-nginx/ingress-nginx](https://github.com/kubernetes/ingress-nginx) | 3.3.0 |

## Development
This project is a Kubernetes native application (Operator) running on [Operator Framework](https://sdk.operatorframework.io/).

To install Custom Resource into cluster
```bash
make install
```

To run controller locally and reconcile `Route` on cluster
```bash
make run
```

## API
**v8s-router** offers a [Custom Resource](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/) as `Route` to expose its functionalities. The documentations of `Route` are presented as inline API docs inside [route_types.go](api/v1beta1/route_types.go).

## Deploying to Kubernetes
Despite **v8s-router** is an infrastructural component equipped on v8s cluster, it can be deployed to vanilla Kubernetes with prerequisites setup as followings.

[MetalLB](https://metallb.universe.tf) should be installed to provide high level loadbalancing for on-premises cluster. Installation guide for *MetalLB* can be found [here](https://metallb.universe.tf/installation/).

As **v8s-router** uses [Ingress](https://kubernetes.io/docs/concepts/services-networking/ingress/) to support CNI traffic routing, it is deemed to use ingress controller for actual implementation on ingress mode. Currently [HAProxy](https://github.com/haproxytech/kubernetes-ingress) and [Nginx](https://github.com/kubernetes/ingress-nginx) are supported as backing ingress controller, and these two ingress controllers should be deployed for **v8s-router** to adopt. 

[HAProxy Ingress Controller](https://github.com/haproxytech/kubernetes-ingress) can be installed following [here](https://www.haproxy.com/documentation/kubernetes/latest/installation/kubernetes/). 

[Nginx Ingress Controller](https://github.com/kubernetes/ingress-nginx) can be installed following [here](https://kubernetes.github.io/ingress-nginx/deploy/).

After installing listed prerequisites, configurations are required for all the components to integrate with **v8s-router**. *MetalLB* should provide specific address pools and ingress controllers (i.e. *HAProxy* and *Nginx*) should be annotated with regarding `ingress.class`. You can refer to [route_types.go](api/v1beta1/route_types.go) and [route_controller.go](controllers/route_controller.go) for detailed configurations.

To get **v8s-router**, you can first add the chart repository
```bash
helm repo add v8s-router https://cedio.github.io/v8s-router
```

You can verify the charts within the added chart repository
```bash
helm search repo v8s-router
```

After that you can install the chart using helm
```bash
helm install my-router v8s-router/v8s-router \
  --namespace my-namespace \
  --set controller.kind=Deployment \
  --set controller.extraArgs."cluster-domain"="example.com"
```

Detailed helm chart values can be found [here](https://github.com/cedio/v8s-router/blob/master/charts/v8s-router/values.yaml).

## Delivery
### Helm Chart
1. Update [Chart.yaml](charts/v8s-router/Chart.yaml) with regarding `appVersion` and `version`. Set `appVersion` for controller version on [Docker Hub](https://hub.docker.com/r/cedio/v8s-router) and `version` for chart version

2. Commit and Pull Request to `master` branch

3. Github Action pipeline will publish latest chart on [cedio.github.io](https://cedio.github.io/v8s-router/index.yaml)

### Container Image
1. Commit and Pull Request to `master` branch

2. Create [Release](https://github.com/cedio/v8s-router/releases) with Tag `/^controller-([0-9.]+)$/`

3. [Docker Hub](https://hub.docker.com/r/cedio/v8s-router) pipeline will build and publish latest image based on tagged Release

## Known Issues
- Both HAProxy Ingress Controller and Nginx Ingress Controller hangs sometime during backend update (i.e. Ingress creation/deletion), use `kubectl rollout restart daemonsets/ingress-controller` to restart

- TLS Re-encryption only supports certificate changing, but not backend certificate verification. Thus pseudo re-encryption

- Blue/Green not supported

## Example
### Loadbalancer External
```yaml
kind: Route
apiVersion: router.v8s.cedio.dev/v1beta1
metadata:
  name: nginx-route
  namespace: route-test
spec:
  serviceName: nginx
  type: loadbalancer
  loadbalancer:
    addressPool: external
```

### HAProxy Ingress Without TLS
```yaml
kind: Route
apiVersion: router.v8s.cedio.dev/v1beta1
metadata:
  name: nginx-route
  namespace: route-test
spec:
  serviceName: nginx
  type: ingress
  ingress:
    class: haproxy
    servicePort: 80
    host: nginx.apps1.v8s.lab
```