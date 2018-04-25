package main

import (
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	batch_v1 "k8s.io/api/batch/v1"
	api_v1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
)

type JobProcessor struct {
	clientset *kubernetes.Clientset
	reaper    chan *batch_v1.Job
	sentry    *log.Logger
}

func NewJobProcessor(
	clientset *kubernetes.Clientset,
	reaper chan *batch_v1.Job,
	sentry *log.Logger,
) *JobProcessor {

	return &JobProcessor{
		clientset: clientset,
		reaper:    reaper,
		sentry:    sentry,
	}
}

func (jp *JobProcessor) fail(job *batch_v1.Job, condition *batch_v1.JobCondition) {

	jobName := job.ObjectMeta.GetLabels()["run"]

	pods, err := jobPods(jp.clientset, job)
	if err != nil {
		if _, ok := err.(*apiErrors.StatusError); ok {
			log.WithError(err).Warnf("Could not fetch jobPods. Skipping for now")
			return
		}
		log.Panic(err.Error())
	}
	pod := oldestPod(pods)

	var warnings []string
	for _, e := range podEvents(jp.clientset, pod).Items {
		if e.Type == api_v1.EventTypeWarning {
			warnings = append(warnings, fmt.Sprintf("%s - %s", e.Reason, e.Message))
		}
	}

	jp.sentry.WithFields(log.Fields{
		"Name":      jobName,
		"Namespace": job.GetNamespace(),
		"Reason":    condition.Reason,
		"Message":   condition.Message,
		"Config":    job.GetAnnotations(),
		"Events":    strings.Join(warnings[:], "\n"),
	}).Errorf("%s failed - %s", jobName, condition.Message)
}

func (jp *JobProcessor) processJob(job *batch_v1.Job) error {

	if c, ok := getJobCondition(job); ok {
		switch c.Type {
		case batch_v1.JobComplete:
			jp.reaper <- job
		case batch_v1.JobFailed:
			jp.fail(job, c)
		}
	}

	return nil
}

func (jp *JobProcessor) Run(jobs chan *batch_v1.Job) {

	log.Info("Starting job processor")

	for {
		job, ok := <-jobs
		if !ok {
			break
		}

		jp.processJob(job)
	}

	close(jobs)
	log.Info("Stopping job processor")
}
