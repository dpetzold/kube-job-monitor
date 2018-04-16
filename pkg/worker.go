package main

import (
	"fmt"

	log "github.com/Sirupsen/logrus"
	batch_v1 "k8s.io/api/batch/v1"
	api_v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

type JobProcessor struct {
	clientset *kubernetes.Clientset
	reaper    chan *batch_v1.Job
}

func NewJobProcessor(
	clientset *kubernetes.Clientset,
	reaper chan *batch_v1.Job,
) *JobProcessor {
	return &JobProcessor{
		clientset: clientset,
		reaper:    reaper,
	}
}

func (jp *JobProcessor) fail(job *batch_v1.Job, condition *batch_v1.JobCondition) {

	log.WithFields(log.Fields{
		"Name":      job.ObjectMeta.GetLabels()["run"],
		"Namespace": job.GetNamespace(),
		"Status":    fmt.Sprintf("%v", condition.Type),
		"Reason":    condition.Reason,
		"Message":   condition.Message,
		"Config":    job.GetAnnotations(),
	}).Error(condition.Message)
}

func getJobCondition(job *batch_v1.Job) (*batch_v1.JobCondition, bool) {
	for _, c := range job.Status.Conditions {
		if (c.Type == batch_v1.JobComplete || c.Type == batch_v1.JobFailed) && c.Status == api_v1.ConditionTrue {
			return &c, true
		}
	}
	return nil, false
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
