/*
Copyright 2019 The Tekton Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"github.com/tektoncd/pipeline/pkg/reconciler/pipelinerun"
	"github.com/tektoncd/pipeline/pkg/reconciler/resolutionrequest"
	"github.com/tektoncd/pipeline/pkg/reconciler/taskrun"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/utils/clock"
	filteredinformerfactory "knative.dev/pkg/client/injection/kube/informers/factory/filtered"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/injection"
	"knative.dev/pkg/injection/sharedmain"
	"knative.dev/pkg/signals"
)

const (
	// ControllerLogKey is the name of the logger for the controller cmd
	ControllerLogKey = "tekton-pipelines-controller"
)

func main() {
	if val, ok := os.LookupEnv("THREADS_PER_CONTROLLER"); ok {
		threadsPerController, err := strconv.Atoi(val)
		if err != nil {
			log.Fatalf("failed to parse value %q of THREADS_PER_CONTROLLER: %v\n", val, err)
		}
		controller.DefaultThreadsPerController = threadsPerController
	}
	flag.IntVar(&controller.DefaultThreadsPerController, "threads-per-controller", controller.DefaultThreadsPerController, "Threads (goroutines) to create per controller")
	namespace := flag.String("namespace", corev1.NamespaceAll, "Namespace to restrict informer to. Optional, defaults to all namespaces.")
	disableHighAvailability := flag.Bool("disable-ha", false, "Whether to disable high-availability functionality for this component.  This flag will be deprecated "+
		"and removed when we have promoted this feature to stable, so do not pass it without filing an "+
		"issue upstream!")

	opts := &pipeline.Options{}
	flag.StringVar(&opts.Images.EntrypointImage, "entrypoint-image", "", "The container image containing our entrypoint binary.")
	flag.StringVar(&opts.Images.SidecarLogResultsImage, "sidecarlogresults-image", "", "The container image containing the binary for accessing results.")
	flag.StringVar(&opts.Images.NopImage, "nop-image", "", "The container image used to stop sidecars")
	flag.StringVar(&opts.Images.ShellImage, "shell-image", "", "The container image containing a shell")
	flag.StringVar(&opts.Images.ShellImageWin, "shell-image-win", "", "The container image containing a windows shell")
	flag.StringVar(&opts.Images.WorkingDirInitImage, "workingdirinit-image", "", "The container image containing our working dir init binary.")
	flag.DurationVar(&opts.ResyncPeriod, "resync-period", controller.DefaultResyncPeriod, "The period between two resync run (going through all objects)")

	// This parses flags.
	cfg := injection.ParseAndGetRESTConfigOrDie()

	if err := opts.Images.Validate(); err != nil {
		log.Fatal(err)
	}
	if cfg.QPS == 0 {
		cfg.QPS = 2 * rest.DefaultQPS
	}
	if cfg.Burst == 0 {
		cfg.Burst = rest.DefaultBurst
	}
	// FIXME(vdemeester): this is here to not break current behavior
	// multiply by 2, no of controllers being created
	cfg.QPS = 2 * cfg.QPS
	cfg.Burst = 2 * cfg.Burst

	ctx := injection.WithNamespaceScope(signals.NewContext(), *namespace)
	if *disableHighAvailability {
		ctx = sharedmain.WithHADisabled(ctx)
	}

	// sets up liveness and readiness probes.
	mux := http.NewServeMux()

	mux.HandleFunc("/", handler)
	mux.HandleFunc("/health", handler)
	mux.HandleFunc("/readiness", handler)

	port := os.Getenv("PROBES_PORT")
	if port == "" {
		port = "8080"
	}

	go func() {
		// start the web server on port and accept requests
		log.Printf("Readiness and health check server listening on port %s", port)
		log.Fatal(http.ListenAndServe(":"+port, mux)) // #nosec G114 -- see https://github.com/securego/gosec#available-rules
	}()

	ctx = filteredinformerfactory.WithSelectors(ctx, v1beta1.ManagedByLabelKey)
	ctx = controller.WithResyncPeriod(ctx, opts.ResyncPeriod)

	sharedmain.MainWithConfig(ctx, ControllerLogKey, cfg,
		taskrun.NewController(opts, clock.RealClock{}),
		pipelinerun.NewController(opts, clock.RealClock{}),
		resolutionrequest.NewController(clock.RealClock{}),
	)
}

func handler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
