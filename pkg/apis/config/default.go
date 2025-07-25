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

package config

import (
	"fmt"
	"log"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline/pod"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"
)

const (
	// DefaultTimeoutMinutes is used when no timeout is specified.
	DefaultTimeoutMinutes = 60
	// NoTimeoutDuration is used when a pipeline or task should never time out.
	NoTimeoutDuration = 0 * time.Minute
	// DefaultServiceAccountValue is the SA used when one is not specified.
	DefaultServiceAccountValue = "default"
	// DefaultManagedByLabelValue is the value for the managed-by label that is used by default.
	DefaultManagedByLabelValue = "tekton-pipelines"
	// DefaultCloudEventSinkValue is the default value for cloud event sinks.
	DefaultCloudEventSinkValue = ""
	// DefaultMaxMatrixCombinationsCount is used when no max matrix combinations count is specified.
	DefaultMaxMatrixCombinationsCount = 256
	// DefaultResolverTypeValue is used when no default resolver type is specified
	DefaultResolverTypeValue = ""
	// default resource requirements, will be applied to all the containers, which has empty resource requirements
	ResourceRequirementDefaultContainerKey = "default"

	DefaultImagePullBackOffTimeout = 0 * time.Minute

	// Default maximum resolution timeout used by the resolution controller before timing out when exceeded
	DefaultMaximumResolutionTimeout = 1 * time.Minute

	DefaultSidecarLogPollingInterval = 100 * time.Millisecond

	defaultTimeoutMinutesKey                = "default-timeout-minutes"
	defaultServiceAccountKey                = "default-service-account"
	defaultManagedByLabelValueKey           = "default-managed-by-label-value"
	defaultPodTemplateKey                   = "default-pod-template"
	defaultAAPodTemplateKey                 = "default-affinity-assistant-pod-template"
	defaultCloudEventsSinkKey               = "default-cloud-events-sink"
	defaultTaskRunWorkspaceBinding          = "default-task-run-workspace-binding"
	defaultMaxMatrixCombinationsCountKey    = "default-max-matrix-combinations-count"
	defaultForbiddenEnv                     = "default-forbidden-env"
	defaultResolverTypeKey                  = "default-resolver-type"
	defaultContainerResourceRequirementsKey = "default-container-resource-requirements"
	defaultImagePullBackOffTimeout          = "default-imagepullbackoff-timeout"
	defaultMaximumResolutionTimeout         = "default-maximum-resolution-timeout"
	defaultSidecarLogPollingIntervalKey     = "default-sidecar-log-polling-interval"
)

// DefaultConfig holds all the default configurations for the config.
var DefaultConfig, _ = NewDefaultsFromMap(map[string]string{})

// Defaults holds the default configurations
// +k8s:deepcopy-gen=true
type Defaults struct {
	DefaultTimeoutMinutes                int
	DefaultServiceAccount                string
	DefaultManagedByLabelValue           string
	DefaultPodTemplate                   *pod.Template
	DefaultAAPodTemplate                 *pod.AffinityAssistantTemplate
	DefaultCloudEventsSink               string // Deprecated. Use the events package instead
	DefaultTaskRunWorkspaceBinding       string
	DefaultMaxMatrixCombinationsCount    int
	DefaultForbiddenEnv                  []string
	DefaultResolverType                  string
	DefaultContainerResourceRequirements map[string]corev1.ResourceRequirements
	DefaultImagePullBackOffTimeout       time.Duration
	DefaultMaximumResolutionTimeout      time.Duration
	// DefaultSidecarLogPollingInterval specifies how frequently (as a time.Duration) the Tekton sidecar log results container polls for step completion files.
	// This value is loaded from the 'sidecar-log-polling-interval' key in the config-defaults ConfigMap.
	// It is used to control the responsiveness and resource usage of the sidecar in both production and test environments.
	DefaultSidecarLogPollingInterval time.Duration
}

// GetDefaultsConfigName returns the name of the configmap containing all
// defined defaults.
func GetDefaultsConfigName() string {
	if e := os.Getenv("CONFIG_DEFAULTS_NAME"); e != "" {
		return e
	}
	return "config-defaults"
}

// Equals returns true if two Configs are identical
func (cfg *Defaults) Equals(other *Defaults) bool {
	if cfg == nil && other == nil {
		return true
	}

	if cfg == nil || other == nil {
		return false
	}

	return other.DefaultTimeoutMinutes == cfg.DefaultTimeoutMinutes &&
		other.DefaultServiceAccount == cfg.DefaultServiceAccount &&
		other.DefaultManagedByLabelValue == cfg.DefaultManagedByLabelValue &&
		other.DefaultPodTemplate.Equals(cfg.DefaultPodTemplate) &&
		other.DefaultAAPodTemplate.Equals(cfg.DefaultAAPodTemplate) &&
		other.DefaultCloudEventsSink == cfg.DefaultCloudEventsSink &&
		other.DefaultTaskRunWorkspaceBinding == cfg.DefaultTaskRunWorkspaceBinding &&
		other.DefaultMaxMatrixCombinationsCount == cfg.DefaultMaxMatrixCombinationsCount &&
		other.DefaultResolverType == cfg.DefaultResolverType &&
		other.DefaultImagePullBackOffTimeout == cfg.DefaultImagePullBackOffTimeout &&
		other.DefaultMaximumResolutionTimeout == cfg.DefaultMaximumResolutionTimeout &&
		other.DefaultSidecarLogPollingInterval == cfg.DefaultSidecarLogPollingInterval &&
		reflect.DeepEqual(other.DefaultForbiddenEnv, cfg.DefaultForbiddenEnv)
}

// NewDefaultsFromMap returns a Config given a map corresponding to a ConfigMap
func NewDefaultsFromMap(cfgMap map[string]string) (*Defaults, error) {
	tc := Defaults{
		DefaultTimeoutMinutes:             DefaultTimeoutMinutes,
		DefaultServiceAccount:             DefaultServiceAccountValue,
		DefaultManagedByLabelValue:        DefaultManagedByLabelValue,
		DefaultCloudEventsSink:            DefaultCloudEventSinkValue,
		DefaultMaxMatrixCombinationsCount: DefaultMaxMatrixCombinationsCount,
		DefaultResolverType:               DefaultResolverTypeValue,
		DefaultImagePullBackOffTimeout:    DefaultImagePullBackOffTimeout,
		DefaultMaximumResolutionTimeout:   DefaultMaximumResolutionTimeout,
		DefaultSidecarLogPollingInterval:  DefaultSidecarLogPollingInterval,
	}

	if defaultTimeoutMin, ok := cfgMap[defaultTimeoutMinutesKey]; ok {
		timeout, err := strconv.ParseInt(defaultTimeoutMin, 10, 0)
		if err != nil {
			return nil, fmt.Errorf("failed parsing default config %q", defaultTimeoutMinutesKey)
		}
		tc.DefaultTimeoutMinutes = int(timeout)
	}

	if defaultServiceAccount, ok := cfgMap[defaultServiceAccountKey]; ok {
		tc.DefaultServiceAccount = defaultServiceAccount
	}

	if defaultManagedByLabelValue, ok := cfgMap[defaultManagedByLabelValueKey]; ok {
		tc.DefaultManagedByLabelValue = defaultManagedByLabelValue
	}

	if defaultPodTemplate, ok := cfgMap[defaultPodTemplateKey]; ok {
		var podTemplate pod.Template
		if err := yamlUnmarshal(defaultPodTemplate, defaultPodTemplateKey, &podTemplate); err != nil {
			return nil, fmt.Errorf("failed to unmarshal %v", defaultPodTemplate)
		}
		tc.DefaultPodTemplate = &podTemplate
	}

	if defaultAAPodTemplate, ok := cfgMap[defaultAAPodTemplateKey]; ok {
		var podTemplate pod.AffinityAssistantTemplate
		if err := yamlUnmarshal(defaultAAPodTemplate, defaultAAPodTemplateKey, &podTemplate); err != nil {
			return nil, fmt.Errorf("failed to unmarshal %v", defaultAAPodTemplate)
		}
		tc.DefaultAAPodTemplate = &podTemplate
	}

	if defaultCloudEventsSink, ok := cfgMap[defaultCloudEventsSinkKey]; ok {
		tc.DefaultCloudEventsSink = defaultCloudEventsSink
	}

	if bindingYAML, ok := cfgMap[defaultTaskRunWorkspaceBinding]; ok {
		tc.DefaultTaskRunWorkspaceBinding = bindingYAML
	}

	if defaultMaxMatrixCombinationsCount, ok := cfgMap[defaultMaxMatrixCombinationsCountKey]; ok {
		matrixCombinationsCount, err := strconv.ParseInt(defaultMaxMatrixCombinationsCount, 10, 0)
		if err != nil {
			return nil, fmt.Errorf("failed parsing default config %q", defaultMaxMatrixCombinationsCountKey)
		}
		tc.DefaultMaxMatrixCombinationsCount = int(matrixCombinationsCount)
	}
	if defaultForbiddenEnvString, ok := cfgMap[defaultForbiddenEnv]; ok {
		tmpString := sets.NewString()
		fEnvs := strings.Split(defaultForbiddenEnvString, ",")
		for _, fEnv := range fEnvs {
			tmpString.Insert(strings.TrimSpace(fEnv))
		}
		tc.DefaultForbiddenEnv = tmpString.List()
	}

	if defaultResolverType, ok := cfgMap[defaultResolverTypeKey]; ok {
		tc.DefaultResolverType = defaultResolverType
	}

	if resourceRequirementsStringValue, ok := cfgMap[defaultContainerResourceRequirementsKey]; ok {
		resourceRequirementsValue := make(map[string]corev1.ResourceRequirements)
		if err := yamlUnmarshal(resourceRequirementsStringValue, defaultContainerResourceRequirementsKey, &resourceRequirementsValue); err != nil {
			return nil, fmt.Errorf("failed to unmarshal %v", resourceRequirementsStringValue)
		}
		tc.DefaultContainerResourceRequirements = resourceRequirementsValue
	}

	if defaultImagePullBackOff, ok := cfgMap[defaultImagePullBackOffTimeout]; ok {
		timeout, err := time.ParseDuration(defaultImagePullBackOff)
		if err != nil {
			return nil, fmt.Errorf("failed parsing default config %q", defaultImagePullBackOffTimeout)
		}
		tc.DefaultImagePullBackOffTimeout = timeout
	}

	if defaultMaximumResolutionTimeout, ok := cfgMap[defaultMaximumResolutionTimeout]; ok {
		timeout, err := time.ParseDuration(defaultMaximumResolutionTimeout)
		if err != nil {
			return nil, fmt.Errorf("failed parsing default config %q", defaultMaximumResolutionTimeout)
		}
		tc.DefaultMaximumResolutionTimeout = timeout
	}

	if defaultSidecarPollingInterval, ok := cfgMap[defaultSidecarLogPollingIntervalKey]; ok {
		interval, err := time.ParseDuration(defaultSidecarPollingInterval)
		if err != nil {
			return nil, fmt.Errorf("failed parsing default config %q", defaultSidecarPollingInterval)
		}
		tc.DefaultSidecarLogPollingInterval = interval
	}

	return &tc, nil
}

func yamlUnmarshal(s string, key string, o interface{}) error {
	b := []byte(s)
	if err := yaml.UnmarshalStrict(b, o); err != nil {
		log.Printf("warning: failed to decode %q: %q. Trying decode with non-strict mode", key, err)
		return yaml.Unmarshal(b, o)
	}
	return nil
}

// NewDefaultsFromConfigMap returns a Config for the given configmap
func NewDefaultsFromConfigMap(config *corev1.ConfigMap) (*Defaults, error) {
	return NewDefaultsFromMap(config.Data)
}
