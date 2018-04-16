Job monitor
================

This repo monitors finished or failed kubernetes jobs.

Project: [https://github.com/dpetzold/kube-job-monitor](https://github.com/dpetzold/kube-job-monitor)

Docker image: [https://registry.hub.docker.com/u/dpetzold/kube-job-monitor/](https://registry.hub.docker.com/u/dpetzold/kube-job-monitor/)


## Usage

## Command Line Options

* `-failures` - Threshold of allowable failures for a job:
    - `-failures=0`: The job will be reaped on any failures.
    - `-failures=-1`: The job will never be reaped on failures (default).
* `-log` - Level to log - debug, info, warn, error, fatal, panic (default info)
* `-keep-completed=*duration*` - Duration to keep completed jobs (e.g. `-keep-completed=4h`).
* `-ignore-owned` - Ignore jobs owned by other resources, e.g. by `CronJob`s (which have their own reaping logic).

### Kubernetes Pod Definition

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: job-monitor
spec:
  replicas: 1
  selector:
    matchLabels:
      name: job-monitor
  template:
    metadata:
      labels:
        name: job-monitor
    spec:
      containers:
      - name: job-monitor
        image: dpetzold/job-monitor:latest
```

## Development

The following will get you setup for local development:

```sh
mkdir -p $GOPATH/github/dpetzold
cd $GOPATH/github/dpetzold
git clone https://github.com/dpetzold/kube-job-monitor.git
minikube start
eval $(minikube docker-env)
make docker
kubectl apply -f examples/job-monitor.yaml
```
