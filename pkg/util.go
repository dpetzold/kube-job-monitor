package main

import (
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
	batch_v1 "k8s.io/api/batch/v1"
	api_v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
)

func jobPods(clientset *kubernetes.Clientset, job *batch_v1.Job) (*api_v1.PodList, error) {
	controllerUID := job.Spec.Selector.MatchLabels["controller-uid"]
	selector := labels.NewSelector()
	requirement, err := labels.NewRequirement("controller-uid", selection.Equals, sets.NewString(controllerUID).List())
	if err != nil {
		log.WithError(err).Error("Creating requirements")
	}
	selector = selector.Add(*requirement)
	pods, err := clientset.Core().Pods(job.ObjectMeta.Namespace).List(meta_v1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		log.WithError(err).Error("Listing pods")
	}

	return pods, err
}

func podEvents(clientset *kubernetes.Clientset, pod api_v1.Pod) *api_v1.EventList {
	sel, err := fields.ParseSelector("involvedObject.name=" + pod.ObjectMeta.Name)
	if err != nil {
		log.WithError(err).Panic("ParseSelector")
	}
	events, err := clientset.Core().Events(pod.ObjectMeta.Namespace).List(meta_v1.ListOptions{
		FieldSelector: sel.String(),
	})
	return events
}

func podStatus(pod api_v1.Pod) string {
	if pod.Status.Phase == api_v1.PodFailed {
		return fmt.Sprintf("%s - %s", pod.Status.Reason, pod.Status.Message)
	}
	return ""
}

func getJobCondition(job *batch_v1.Job) (*batch_v1.JobCondition, bool) {
	for _, c := range job.Status.Conditions {
		if (c.Type == batch_v1.JobComplete || c.Type == batch_v1.JobFailed) && c.Status == api_v1.ConditionTrue {
			return &c, true
		}
	}
	return nil, false
}

func failedPods(pods *api_v1.PodList) []api_v1.Pod {

	var failed []api_v1.Pod
	for _, pod := range pods.Items {
		if pod.Status.Phase == api_v1.PodFailed {
			failed = append(failed, pod)
		}
	}
	return failed
}

func oldestPod(pods *api_v1.PodList) api_v1.Pod {

	time := time.Now()
	var pod api_v1.Pod
	for _, p := range pods.Items {
		if time.After(p.ObjectMeta.CreationTimestamp.Time) {
			time = p.ObjectMeta.CreationTimestamp.Time
			pod = p
		}
	}
	return pod
}

func getJobCompletions(job *batch_v1.Job) int {
	if job.Spec.Completions != nil {
		return int(*job.Spec.Completions)
	}
	return 1
}
