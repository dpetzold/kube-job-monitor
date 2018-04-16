package main

import (
	"fmt"
	"time"

	log "github.com/Sirupsen/logrus"
	batch_v1 "k8s.io/api/batch/v1"
	"k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
)

type JobReaper struct {
	clientset       *kubernetes.Clientset
	maxFailures     int
	retentionPeriod time.Duration
}

func NewJobReaper(
	clientset *kubernetes.Clientset,
	maxFailures int,
	retentionPeriod time.Duration,
) *JobReaper {

	return &JobReaper{
		clientset:       clientset,
		maxFailures:     maxFailures,
		retentionPeriod: retentionPeriod,
	}
}

func (jr *JobReaper) shouldReap(job batch_v1.Job) bool {

	// Ignore cronjobs
	if len(job.ObjectMeta.OwnerReferences) > 0 {
		return false
	}

	// Always reap if number of failures has exceed maximum
	if jr.maxFailures >= 0 && int(job.Status.Failed) > jr.maxFailures {
		return true
	}

	// Don't reap anything that hasn't met its completion count
	if int(job.Status.Succeeded) < getJobCompletions(job) {
		return false
	}

	// Don't reap completed jobs that aren't old enough
	if job.Status.CompletionTime != nil &&
		time.Since(job.Status.CompletionTime.Time) < jr.retentionPeriod {
		return false
	}

	return true
}

func (jr *JobReaper) reap(job *batch_v1.Job) {
	alert := log.WithFields(log.Fields{
		"Name":      job.GetName(),
		"Namespace": job.GetNamespace(),
		"Config":    job.GetAnnotations(),
	})

	pods, err := jr.jobPods(job)

	if err != nil {
		if _, ok := err.(*apiErrors.StatusError); ok {
			log.WithError(err).Warnf("Could not fetch jobPods. Skipping for now")
			return
		}
		log.Panic(err.Error())
	}
	pod := jr.oldestPod(pods)

	if scheduledJobName, ok := pod.GetLabels()["run"]; ok {
		alert.WithFields(log.Fields{
			"Name": scheduledJobName,
		})
	}

	if pod.Status.Phase != "" {
		alert.WithField("Status", string(pod.Status.Phase))
	}

	if len(pod.Status.ContainerStatuses) > 0 { //Container has exited
		terminated := pod.Status.ContainerStatuses[0].State.Terminated
		if terminated != nil {
			alert.WithFields(log.Fields{
				"Message":   terminated.Reason, // ERRRRR
				"ExitCode":  int(terminated.ExitCode),
				"StartTime": terminated.StartedAt.Time,
				"EndTime":   terminated.FinishedAt.Time,
			})
		} else {
			log.WithFields(log.Fields{
				"statuses":   pod.Status.ContainerStatuses[0],
				"terminated": terminated,
				"job":        job,
				"conditions": job.Status.Conditions,
				"pod":        pod,
				"events":     jr.podEvents(pod),
			}).Error("Unexpected null for container state")
			return
		}
	} else if len(job.Status.Conditions) > 0 { //TODO naive when more than one condition
		condition := job.Status.Conditions[0]
		alert.Message = fmt.Sprintf("Pod Missing: %s - %s", condition.Reason, condition.Message)
		if condition.Type == batch_v1.JobComplete {
			alert.WithFields(log.Fields{
				"ExitCode": 0,
				"Status":   "Succeeded",
			})
		} else {
			alert.WithFields(log.Fields{
				"ExitCode": 998,
			})
		}

		alert.WithFields(log.Fields{
			"StartTime": job.Status.StartTime.Time,
			"EndTime":   condition.LastTransitionTime.Time,
		})

	} else {
		// Unfinished Containers or missing
		alert.WithFields(log.Fields{
			"ExitCode": 999,
			"EndTime":  time.Now(),
		})
	}

	alert.Info("Job reaped")

	go func() {
		err := jr.clientset.Batch().Jobs(alert.Data["Namespace"].(string)).Delete(job.GetName(), nil)
		if err != nil {
			log.Error(err.Error())
		}

		log.WithFields(log.Fields{
			"pod": alert.Data["Name"].(string),
		}).Debug("Deleting pods")

		numPods := len(pods.Items)
		for _, pod := range pods.Items {
			err := jr.clientset.Core().Pods(alert.Data["Namespace"].(string)).Delete(pod.GetName(), nil)
			if err != nil {
				log.Error(err.Error())
			}
		}

		log.WithFields(log.Fields{
			"pod":   alert.Data["Name"].(string),
			"count": numPods,
		}).Info("Deleted pods")
	}()
}

func (jr *JobReaper) jobPods(job *batch_v1.Job) (*v1.PodList, error) {
	controllerUID := job.Spec.Selector.MatchLabels["controller-uid"]
	selector := labels.NewSelector()
	requirement, err := labels.NewRequirement("controller-uid", selection.Equals, sets.NewString(controllerUID).List())
	if err != nil {
		log.WithError(err).Error("Creating requirements")
	}
	selector = selector.Add(*requirement)
	pods, err := jr.clientset.Core().Pods(job.ObjectMeta.Namespace).List(meta_v1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		log.WithError(err).Error("Listing pods")
	}

	return pods, err
}

func (jr *JobReaper) podEvents(pod v1.Pod) *v1.EventList {
	sel, err := fields.ParseSelector("involvedObject.name=" + pod.ObjectMeta.Name)
	if err != nil {
		log.WithError(err).Panic("ParseSelector")
	}
	events, err := jr.clientset.Core().Events(pod.ObjectMeta.Namespace).List(meta_v1.ListOptions{
		FieldSelector: sel.String(),
	})
	return events
}

func (jr *JobReaper) oldestPod(pods *v1.PodList) v1.Pod {
	time := time.Now()
	var tempPod v1.Pod
	for _, pod := range pods.Items {
		if time.After(pod.ObjectMeta.CreationTimestamp.Time) {
			time = pod.ObjectMeta.CreationTimestamp.Time
			tempPod = pod
		}
	}
	return tempPod
}

func getJobCompletions(job batch_v1.Job) int {
	if job.Spec.Completions != nil {
		return int(*job.Spec.Completions)
	}
	return 1
}

func (jr *JobReaper) Run(jobs chan *batch_v1.Job) {

	log.Info("Starting job reaper")

	for {
		job, ok := <-jobs
		if !ok {
			break
		}

		jr.reap(job)
	}

	close(jobs)
	log.Info("Stopping job reaper")
}
