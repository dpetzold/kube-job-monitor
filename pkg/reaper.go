package main

import (
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
	batch_v1 "k8s.io/api/batch/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
)

type JobReaper struct {
	clientset       *kubernetes.Clientset
	maxFailures     int
	retentionPeriod time.Duration
	sentry          *log.Logger
}

func NewJobReaper(
	clientset *kubernetes.Clientset,
	maxFailures int,
	retentionPeriod time.Duration,
	sentry *log.Logger,
) *JobReaper {

	return &JobReaper{
		clientset:       clientset,
		maxFailures:     maxFailures,
		retentionPeriod: retentionPeriod,
		sentry:          sentry,
	}
}

func (jr *JobReaper) shouldReap(job *batch_v1.Job) bool {

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

	pods, err := jobPods(jr.clientset, job)
	if err != nil {
		if _, ok := err.(*apiErrors.StatusError); ok {
			log.WithError(err).Warnf("Could not fetch jobPods. Skipping for now")
			return
		}
		log.Panic(err.Error())
	}
	pod := oldestPod(pods)

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
				"Statuses":   pod.Status.ContainerStatuses[0],
				"Terminated": terminated,
				"Job":        job,
				"Conditions": job.Status.Conditions,
				"Pod":        pod,
				"Events":     podEvents(jr.clientset, pod),
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
				log.WithFields(log.Fields{
					"pod": pod.GetName(),
				}).WithError(err).Error("Deleteing pod")
			}
		}

		log.WithFields(log.Fields{
			"pod":   alert.Data["Name"].(string),
			"count": numPods,
		}).Info("Deleted pods")
	}()
}

func (jr *JobReaper) Run(jobs chan *batch_v1.Job) {

	log.Info("Starting job reaper")

	for {
		job, ok := <-jobs
		if !ok {
			break
		}

		if jr.shouldReap(job) {
			jr.reap(job)
		}
	}

	close(jobs)
	log.Info("Stopping job reaper")
}
