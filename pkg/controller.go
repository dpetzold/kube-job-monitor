package main

import (
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	batch_v1 "k8s.io/api/batch/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

type JobController struct {
	clientset *kubernetes.Clientset
	queue     workqueue.RateLimitingInterface
	informer  cache.SharedIndexInformer
	jobs      chan *batch_v1.Job
}

func NewJobController(
	clientset *kubernetes.Clientset,
	jobs chan *batch_v1.Job,
) *JobController {

	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options meta_v1.ListOptions) (runtime.Object, error) {
				return clientset.BatchV1().Jobs(meta_v1.NamespaceAll).List(options)
			},
			WatchFunc: func(options meta_v1.ListOptions) (watch.Interface, error) {
				return clientset.BatchV1().Jobs(meta_v1.NamespaceAll).Watch(options)
			},
		},
		&batch_v1.Job{},
		0, // Skip resync
		cache.Indexers{},
	)

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
		},
		UpdateFunc: func(old interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			if err == nil {
				queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
		},
	})

	return &JobController{
		clientset: clientset,
		informer:  informer,
		queue:     queue,
		jobs:      jobs,
	}
}

func (jc *JobController) handleErr(err error, key interface{}) {
	if err == nil {
		jc.queue.Forget(key)
		return
	}

	if jc.queue.NumRequeues(key) < 5 {
		log.WithFields(log.Fields{
			"key": key,
			"err": err,
		}).Info("Error syncing job retries exceeded")
		jc.queue.AddRateLimited(key)
		return
	}

	jc.queue.Forget(key)
	utilruntime.HandleError(err)
	log.WithFields(log.Fields{
		"key": key,
		"err": err,
	}).Error("Dropping job out of the queue")
}

func (jc *JobController) processNextItem() bool {
	key, quit := jc.queue.Get()
	if quit {
		return false
	}

	defer jc.queue.Done(key)

	obj, exists, err := jc.informer.GetIndexer().GetByKey(key.(string))
	if err != nil {
		log.WithFields(log.Fields{
			"job": key,
			"err": err,
		}).Error("Fetching object with failed")
		return false
	}

	if !exists {
		log.WithFields(log.Fields{
			"job": key,
		}).Debug("Job does not exist anymore")
		return false
	}

	if job, ok := obj.(*batch_v1.Job); ok {
		jc.jobs <- job
	} else {
		log.WithFields(log.Fields{
			"obj": obj,
		}).Error("Unknown object")
	}

	jc.handleErr(err, key)
	return true
}

func (jc *JobController) runWorker() {
	for jc.processNextItem() {
	}
}

func (jc *JobController) Run(threadiness int, stopCh chan struct{}) {
	defer utilruntime.HandleCrash()

	defer jc.queue.ShutDown()

	log.Info("Starting job controller")

	go jc.informer.Run(stopCh)

	if !cache.WaitForCacheSync(stopCh, jc.informer.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}

	for i := 0; i < threadiness; i++ {
		go wait.Until(jc.runWorker, time.Second, stopCh)
	}

	<-stopCh
	log.Info("Stopping job controller")
}
