TEST? = $$(glide nv)

GIT_COMMIT = $(shell git rev-parse HEAD)
GO_SOURCE_FILES = $(shell find pkg -type f -name "*.go")

build: vendor $(GO_SOURCE_FILES)
	go build -v -i -ldflags "-X main.GitCommit=${GIT_COMMIT} -extldflags '-static'" -o build/job-monitor ./pkg

vendor:
	glide install

docker:
	docker build -t job-monitor .

clean:
	rm -rf build

test: vendor
	go test -v $(TEST)

fmt: $(GO_SOURCE_FILES)
	goimports -w $(GO_SOURCE_FILES)

run: build
	./build/job-monitor --config=./myconfig.yaml --master=localhost:8080 --failures=0

mini:
	minikube ip || $$(pkill -9 -f 'kubectl proxy'; minikube start; kubectl proxy --port=8080 &)

kube_clean: mini
	kubectl delete cronjobs --all
	kubectl delete jobs --all
	kubectl delete pods --all

jobs: kube_clean
	# Success
	kubectl run succeed --schedule="*/5 * * * ?" --image=perl --restart=OnFailure -- perl -Mbignum=bpi -wle 'print bpi(2000)'
	# Always Fail
	kubectl run always-fail --schedule="*/1 * * * ?" --image=busybox --restart=Never -- /bin/sh -c 'sleep 10; exit 5'
	kubectl annotate po always-fail succeed job-monitor.github.sstarcher.io/channel='#listhub-syseng-status'
	# Image pull Error
	#kubectl run image-pull-error --schedule="*/1 * * * ?" --image=buxybox --restart=Never -- /bin/sh
	# RunContainerError
	#kubectl run run-container-error --schedule="*/1 * * * ?" --image=busybox --restart=Never -- /bin/bash

annotate:
	kubectl annotate scheduledjob succeed job-monitor.github.sstarcher.io/channel=listhub-syseng

.PHONY: all docker test run
