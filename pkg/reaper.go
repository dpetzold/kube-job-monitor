package main

import (
	"time"

	log "github.com/sirupsen/logrus"
	batch_v1 "k8s.io/api/batch/v1"
	api_v1 "k8s.io/api/core/v1"
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

	logger := log.WithFields(log.Fields{
		"job": job.GetName(),
	})

	// Ignore cronjobs
	if len(job.ObjectMeta.OwnerReferences) > 0 {
		logger.Info("Ignoring job with owner cound > 0")
		return false
	}

	// Always reap if number of failures has exceed maximum
	if jr.maxFailures >= 0 && int(job.Status.Failed) > jr.maxFailures {
		logger.Info("Reaping job max failures exceeded")
		return true
	}

	// Don't reap anything that hasn't met its completion count
	if int(job.Status.Succeeded) < getJobCompletions(job) {
		logger.Info("Ignoring job completion count not met")
		return false
	}

	// Don't reap completed jobs that aren't old enough
	if job.Status.CompletionTime != nil &&
		time.Since(job.Status.CompletionTime.Time) < jr.retentionPeriod {
		logger.Info("Ignoring job within retention period")
		return false
	}

	logger.Info("Job met reap criteria")
	return true
}

func (jr *JobReaper) reap(job *batch_v1.Job) {
	fields := log.Fields{
		"Name":      job.GetName(),
		"Namespace": job.GetNamespace(),
		"Config":    job.GetAnnotations(),
	}

	pods, err := jobPods(jr.clientset, job)
	if err != nil {
		jr.sentry.WithFields(fields).WithError(err).Panic("Could not fetch job pods")
	}

	if jr.shouldReap(job) {
		jr.deletePods(job, pods)
		log.WithFields(fields).Info("Job reaped")
	}
}

func (jr *JobReaper) deletePods(job *batch_v1.Job, pods *api_v1.PodList) {

	err := jr.clientset.Batch().Jobs(job.GetNamespace()).Delete(job.GetName(), nil)
	if err != nil {
		log.Error(err.Error())
	}

	log.WithFields(log.Fields{
		"job": job.GetName(),
	}).Debug("Deleting pods")

	numPods := len(pods.Items)
	for _, pod := range pods.Items {
		err := jr.clientset.Core().Pods(job.GetNamespace()).Delete(pod.GetName(), nil)
		if err != nil {
			log.WithFields(log.Fields{
				"pod": pod.GetName(),
			}).WithError(err).Error("Deleteing pod")
		}
	}

	log.WithFields(log.Fields{
		"job":   job.GetName(),
		"count": numPods,
	}).Info("Deleted pods")
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
