package main

import (
	"flag"
	"os"

	"github.com/evalphobia/logrus_sentry"
	log "github.com/sirupsen/logrus"
	batch_v1 "k8s.io/api/batch/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// The git commit that was compiled. This will be filled in by the compiler.
var GitCommit string

var (
	retentionPeriod = flag.Duration("retention-period", 0, "minimum age before a completed job can be deleted")
	failures        = flag.Int("failures", -1, "threshold of allowable failures for a job")
	interval        = flag.Int("interval", 30, "interval in seconds to wait between looking for jobs to reap")
	logLevel        = flag.String("log", "info", "log level - debug, info, warn, error, fatal, panic")
)

func main() {

	flag.Parse()

	log.SetFormatter(&log.JSONFormatter{})
	value, err := log.ParseLevel(*logLevel)
	if err != nil {
		log.Panic(err.Error())
	}
	log.SetLevel(value)

	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	sentry := log.New()
	hook, err := logrus_sentry.NewAsyncSentryHook(os.Getenv("SENTRY_DSN"), []log.Level{
		log.PanicLevel,
		log.FatalLevel,
		log.ErrorLevel,
	})

	if err == nil {
		sentry.Hooks.Add(hook)
	} else {
		log.WithError(err).Error("Error from sentry")
	}

	stopCh := make(chan struct{})
	jobCh := make(chan *batch_v1.Job, 5)
	reapCh := make(chan *batch_v1.Job, 5)
	defer close(stopCh)
	defer close(jobCh)
	defer close(reapCh)

	reaper := NewJobReaper(clientset, *failures, *retentionPeriod, sentry)
	processor := NewJobProcessor(clientset, reapCh, sentry)
	controller := NewJobController(clientset, jobCh)

	log.Infof("job-monitor running (%s)", GitCommit)

	go processor.Run(jobCh)
	go reaper.Run(reapCh)
	go controller.Run(1, stopCh)

	// Wait forever
	select {}
}
