/*
Copyright 2016 The Kubernetes Authors All rights reserved.

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
	"errors"
	"log"
	"os"
	"time"

	flag "github.com/spf13/pflag"

	"k8s.io/horizontal-self-scaler/autoscaler/nanny"
)

var (
	configMap         = flag.String("configmap", "", "ConfigMap containing our scaling parameters")
	verbose           = flag.Bool("verbose", false, "Turn on verbose logging to stdout")
	pollPeriodSeconds = flag.Int("poll-period-seconds", 10, "The time, in seconds, to poll the dependent container.")
	namespace         = flag.String("namespace", "", "Namespace for all operations")
	rc                = flag.String("rc", "", "ReplicationController to scale")
	rs                = flag.String("rs", "", "ReplicaSet to scale")
	deployment        = flag.String("deployment", "", "Deployment to scale")
)

func sanityCheckParametersAndEnvironment() error {
	var errorsFound bool
	if *configMap == "" {
		errorsFound = true
		log.Printf("--configmap parameter cannot be empty\n")
	}
	if *pollPeriodSeconds < 1 {
		errorsFound = true
		log.Printf("--poll-period-seconds cannot be less than 1\n")
	}
	var exclusiveFlags int
	if len(*rc) > 0 {
		exclusiveFlags++
	}
	if len(*rs) > 0 {
		exclusiveFlags++
	}
	if len(*deployment) > 0 {
		exclusiveFlags++
	}
	if exclusiveFlags == 0 {
		log.Printf("One of --rc, --rs or --deployment is mandatory")
		errorsFound = true
	} else if exclusiveFlags > 1 {
		log.Printf("Flags --rc, --rs or --deployment are mutually exclusive; specify exactly one of them")
		errorsFound = true
	}
	// Log all sanity check errors before returning a single error string
	if errorsFound {
		return errors.New("Failed to validate all input parameters")
	}
	return nil
}

func main() {
	// First log our starting config, and then set up.
	log.Printf("Invoked by %v\n", os.Args)
	flag.Parse()
	// Perform further validation of flags.
	if err := sanityCheckParametersAndEnvironment(); err != nil {
		log.Fatal(err)
	}
	k8s := nanny.NewKubernetesClient(*namespace, *rc, *rs, *deployment)
	log.Printf("Scaling Namespace: %s RC: %s, RS: %s, Deployment: %s\n", *namespace, *rc, *rs, *deployment)
	scaler := &nanny.Scaler{Verbose: *verbose}
	pollPeriod := time.Second * time.Duration(*pollPeriodSeconds)
	// Begin nannying.
	nanny.PollAPIServer(k8s, scaler, pollPeriod, *configMap, *verbose)
}
