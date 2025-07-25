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

package taskrunmetrics

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/config"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	faketaskruninformer "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1/taskrun/fake"
	"github.com/tektoncd/pipeline/pkg/names"
	"github.com/tektoncd/pipeline/pkg/pod"
	ttesting "github.com/tektoncd/pipeline/pkg/reconciler/testing"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/metrics/metricstest"
	_ "knative.dev/pkg/metrics/testing"
)

var (
	startTime      = metav1.Now()
	completionTime = metav1.NewTime(startTime.Time.Add(time.Minute))
)

func getConfigContext(countWithReason, throttleWithNamespace bool) context.Context {
	ctx := context.Background()
	cfg := &config.Config{
		Metrics: &config.Metrics{
			TaskrunLevel:            config.TaskrunLevelAtTaskrun,
			PipelinerunLevel:        config.PipelinerunLevelAtPipelinerun,
			DurationTaskrunType:     config.DefaultDurationTaskrunType,
			DurationPipelinerunType: config.DefaultDurationPipelinerunType,
			CountWithReason:         countWithReason,
			ThrottleWithNamespace:   throttleWithNamespace,
		},
	}
	return config.ToContext(ctx, cfg)
}

func TestUninitializedMetrics(t *testing.T) {
	metrics := Recorder{}

	beforeCondition := &apis.Condition{
		Type:   apis.ConditionReady,
		Status: corev1.ConditionUnknown,
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := metrics.DurationAndCount(ctx, &v1.TaskRun{}, beforeCondition); err == nil {
		t.Error("DurationCount recording expected to return error but got nil")
	}
	if err := metrics.RunningTaskRuns(ctx, nil); err == nil {
		t.Error("Current TaskRunsCount recording expected to return error but got nil")
	}
	if err := metrics.RecordPodLatency(ctx, nil, nil); err == nil {
		t.Error("Pod Latency recording expected to return error but got nil")
	}
}

func TestOnStore(t *testing.T) {
	unregisterMetrics()
	log := zap.NewExample().Sugar()

	// 1. Initial state
	initialCfg := &config.Config{Metrics: &config.Metrics{
		TaskrunLevel:        config.TaskrunLevelAtTaskrun,
		PipelinerunLevel:    config.PipelinerunLevelAtPipelinerun,
		DurationTaskrunType: config.DurationTaskrunTypeLastValue,
	}}
	ctx := config.ToContext(t.Context(), initialCfg)
	r, err := NewRecorder(ctx)
	if err != nil {
		t.Fatalf("NewRecorder failed: %v", err)
	}
	onStoreCallback := OnStore(log, r)

	// Check initial state
	if reflect.ValueOf(r.insertTaskTag).Pointer() != reflect.ValueOf(taskrunInsertTag).Pointer() {
		t.Fatalf("Initial insertTaskTag function is incorrect")
	}
	initialHash := r.hash

	// 2. Call with wrong name - should not change anything
	onStoreCallback("wrong-name", &config.Metrics{TaskrunLevel: config.TaskrunLevelAtNS})
	if r.hash != initialHash {
		t.Errorf("Hash changed after call with wrong name")
	}
	if reflect.ValueOf(r.insertTaskTag).Pointer() != reflect.ValueOf(taskrunInsertTag).Pointer() {
		t.Errorf("insertTaskTag changed after call with wrong name")
	}

	// 3. Call with wrong type - should log an error and not change anything
	onStoreCallback(config.GetMetricsConfigName(), &config.Store{})
	if r.hash != initialHash {
		t.Errorf("Hash changed after call with wrong type")
	}
	if reflect.ValueOf(r.insertTaskTag).Pointer() != reflect.ValueOf(taskrunInsertTag).Pointer() {
		t.Errorf("insertTaskTag changed after call with wrong type")
	}

	// 4. Call with a valid new config - should change
	newCfg := &config.Metrics{
		TaskrunLevel:        config.TaskrunLevelAtNS,
		PipelinerunLevel:    config.PipelinerunLevelAtNS,
		DurationTaskrunType: config.DurationTaskrunTypeLastValue,
	}
	onStoreCallback(config.GetMetricsConfigName(), newCfg)
	if r.hash == initialHash {
		t.Errorf("Hash did not change after valid config update")
	}
	if reflect.ValueOf(r.insertTaskTag).Pointer() != reflect.ValueOf(nilInsertTag).Pointer() {
		t.Errorf("insertTaskTag did not change after valid config update")
	}
	newHash := r.hash

	// 5. Call with the same config again - should not change
	onStoreCallback(config.GetMetricsConfigName(), newCfg)
	if r.hash != newHash {
		t.Errorf("Hash changed after second call with same config")
	}
	if reflect.ValueOf(r.insertTaskTag).Pointer() != reflect.ValueOf(nilInsertTag).Pointer() {
		t.Errorf("insertTaskTag changed after second call with same config")
	}

	// 6. Call with an invalid config - should update hash but not insertTag
	invalidCfg := &config.Metrics{TaskrunLevel: "invalid-level"}
	onStoreCallback(config.GetMetricsConfigName(), invalidCfg)
	if r.hash == newHash {
		t.Errorf("Hash did not change after invalid config update")
	}
	// Because viewRegister fails, the insertTag function should not be updated and should remain `nilInsertTag` from the previous step.
	if reflect.ValueOf(r.insertTaskTag).Pointer() != reflect.ValueOf(nilInsertTag).Pointer() {
		t.Errorf("insertTag changed after invalid config update")
	}
}

func TestUpdateConfig(t *testing.T) {
	// Test that the config is updated when it changes, and not when it doesn't.
	ctx := getConfigContext(false, false)
	r, err := NewRecorder(ctx)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	// First, update with a new config.
	newConfig := &config.Metrics{
		TaskrunLevel: config.TaskrunLevelAtTask,
	}
	if !r.updateConfig(newConfig) {
		t.Error("updateConfig should have returned true, but returned false")
	}

	// Then, update with the same config.
	if r.updateConfig(newConfig) {
		t.Error("updateConfig should have returned false, but returned true")
	}

	// Finally, update with a different config.
	differentConfig := &config.Metrics{
		TaskrunLevel: config.TaskrunLevelAtNS,
	}
	if !r.updateConfig(differentConfig) {
		t.Error("updateConfig should have returned true, but returned false")
	}
}

func TestRecordTaskRunDurationCount(t *testing.T) {
	for _, c := range []struct {
		name                 string
		taskRun              *v1.TaskRun
		metricName           string // "taskrun_duration_seconds" or "pipelinerun_taskrun_duration_seconds"
		expectedDurationTags map[string]string
		expectedCountTags    map[string]string
		expectedDuration     float64
		expectedCount        int64
		beforeCondition      *apis.Condition
		countWithReason      bool
	}{{
		name: "for succeeded taskrun",
		taskRun: &v1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{Name: "taskrun-1", Namespace: "ns"},
			Spec: v1.TaskRunSpec{
				TaskRef: &v1.TaskRef{Name: "task-1"},
			},
			Status: v1.TaskRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{{
						Type:   apis.ConditionSucceeded,
						Status: corev1.ConditionTrue,
					}},
				},
				TaskRunStatusFields: v1.TaskRunStatusFields{
					StartTime:      &startTime,
					CompletionTime: &completionTime,
				},
			},
		},
		metricName: "taskrun_duration_seconds",
		expectedDurationTags: map[string]string{
			"task":      "task-1",
			"taskrun":   "taskrun-1",
			"namespace": "ns",
			"status":    "success",
		},
		expectedCountTags: map[string]string{
			"status": "success",
		},
		expectedDuration: 60,
		expectedCount:    1,
		beforeCondition:  nil,
		countWithReason:  false,
	}, {
		name: "for succeeded taskrun ref cluster task",
		taskRun: &v1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{Name: "taskrun-1", Namespace: "ns", Labels: map[string]string{
				pipeline.PipelineTaskLabelKey: "task-1",
			}},
			Spec: v1.TaskRunSpec{
				TaskSpec: &v1.TaskSpec{},
			},
			Status: v1.TaskRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{{
						Type:   apis.ConditionSucceeded,
						Status: corev1.ConditionTrue,
					}},
				},
				TaskRunStatusFields: v1.TaskRunStatusFields{
					StartTime:      &startTime,
					CompletionTime: &completionTime,
				},
			},
		},
		metricName: "taskrun_duration_seconds",
		expectedDurationTags: map[string]string{
			"task":      "task-1",
			"taskrun":   "taskrun-1",
			"namespace": "ns",
			"status":    "success",
		},
		expectedCountTags: map[string]string{
			"status": "success",
		},
		expectedDuration: 60,
		expectedCount:    1,
		beforeCondition:  nil,
		countWithReason:  false,
	}, {
		name: "for succeeded taskrun with before condition",
		taskRun: &v1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{Name: "taskrun-1", Namespace: "ns"},
			Spec: v1.TaskRunSpec{
				TaskRef: &v1.TaskRef{Name: "task-1"},
			},
			Status: v1.TaskRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{{
						Type:   apis.ConditionSucceeded,
						Status: corev1.ConditionTrue,
					}},
				},
				TaskRunStatusFields: v1.TaskRunStatusFields{
					StartTime:      &startTime,
					CompletionTime: &completionTime,
				},
			},
		},
		metricName: "taskrun_duration_seconds",
		expectedDurationTags: map[string]string{
			"task":      "task-1",
			"taskrun":   "taskrun-1",
			"namespace": "ns",
			"status":    "success",
		},
		expectedCountTags: map[string]string{
			"status": "success",
		},
		expectedDuration: 60,
		expectedCount:    1,
		beforeCondition: &apis.Condition{
			Type:   apis.ConditionReady,
			Status: corev1.ConditionUnknown,
		},
		countWithReason: false,
	}, {
		name: "for succeeded taskrun recount",
		taskRun: &v1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{Name: "taskrun-1", Namespace: "ns"},
			Spec: v1.TaskRunSpec{
				TaskRef: &v1.TaskRef{Name: "task-1"},
			},
			Status: v1.TaskRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{{
						Type:   apis.ConditionSucceeded,
						Status: corev1.ConditionTrue,
					}},
				},
				TaskRunStatusFields: v1.TaskRunStatusFields{
					StartTime:      &startTime,
					CompletionTime: &completionTime,
				},
			},
		},
		metricName:           "taskrun_duration_seconds",
		expectedDurationTags: nil,
		expectedCountTags:    nil,
		expectedDuration:     0,
		expectedCount:        0,
		beforeCondition: &apis.Condition{
			Type:   apis.ConditionSucceeded,
			Status: corev1.ConditionTrue,
		},
		countWithReason: false,
	}, {
		name: "for failed taskrun",
		taskRun: &v1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{Name: "taskrun-1", Namespace: "ns"},
			Spec: v1.TaskRunSpec{
				TaskRef: &v1.TaskRef{Name: "task-1"},
			},
			Status: v1.TaskRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{{
						Type:   apis.ConditionSucceeded,
						Status: corev1.ConditionFalse,
					}},
				},
				TaskRunStatusFields: v1.TaskRunStatusFields{
					StartTime:      &startTime,
					CompletionTime: &completionTime,
				},
			},
		},
		metricName: "taskrun_duration_seconds",
		expectedDurationTags: map[string]string{
			"task":      "task-1",
			"taskrun":   "taskrun-1",
			"namespace": "ns",
			"status":    "failed",
		},
		expectedCountTags: map[string]string{
			"status": "failed",
		},
		expectedDuration: 60,
		expectedCount:    1,
		beforeCondition:  nil,
		countWithReason:  false,
	}, {
		name: "for failed taskrun with reference remote task",
		taskRun: &v1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "taskrun-1",
				Namespace: "ns",
				Labels: map[string]string{
					pipeline.TaskLabelKey: "task-remote",
				},
			},
			Spec: v1.TaskRunSpec{
				TaskRef: &v1.TaskRef{
					ResolverRef: v1.ResolverRef{
						Resolver: "git",
					},
				},
			},
			Status: v1.TaskRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{{
						Type:   apis.ConditionSucceeded,
						Status: corev1.ConditionFalse,
					}},
				},
				TaskRunStatusFields: v1.TaskRunStatusFields{
					StartTime:      &startTime,
					CompletionTime: &completionTime,
				},
			},
		},
		metricName: "taskrun_duration_seconds",
		expectedDurationTags: map[string]string{
			"task":      "task-remote",
			"taskrun":   "taskrun-1",
			"namespace": "ns",
			"status":    "failed",
		},
		expectedCountTags: map[string]string{
			"status": "failed",
		},
		expectedDuration: 60,
		expectedCount:    1,
		beforeCondition:  nil,
		countWithReason:  false,
	}, {
		name: "for succeeded taskrun in pipelinerun",
		taskRun: &v1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{
				Name: "taskrun-1", Namespace: "ns",
				Labels: map[string]string{
					pipeline.PipelineLabelKey:    "pipeline-1",
					pipeline.PipelineRunLabelKey: "pipelinerun-1",
				},
			},
			Spec: v1.TaskRunSpec{
				TaskRef: &v1.TaskRef{Name: "task-1"},
			},
			Status: v1.TaskRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{{
						Type:   apis.ConditionSucceeded,
						Status: corev1.ConditionTrue,
					}},
				},
				TaskRunStatusFields: v1.TaskRunStatusFields{
					StartTime:      &startTime,
					CompletionTime: &completionTime,
				},
			},
		},
		metricName: "pipelinerun_taskrun_duration_seconds",
		expectedDurationTags: map[string]string{
			"pipeline":    "pipeline-1",
			"pipelinerun": "pipelinerun-1",
			"task":        "task-1",
			"taskrun":     "taskrun-1",
			"namespace":   "ns",
			"status":      "success",
		},
		expectedCountTags: map[string]string{
			"status": "success",
		},
		expectedDuration: 60,
		expectedCount:    1,
		beforeCondition:  nil,
		countWithReason:  false,
	}, {
		name: "for failed taskrun in pipelinerun",
		taskRun: &v1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{
				Name: "taskrun-1", Namespace: "ns",
				Labels: map[string]string{
					pipeline.PipelineLabelKey:    "pipeline-1",
					pipeline.PipelineRunLabelKey: "pipelinerun-1",
				},
			},
			Spec: v1.TaskRunSpec{
				TaskRef: &v1.TaskRef{Name: "task-1"},
			},
			Status: v1.TaskRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{{
						Type:   apis.ConditionSucceeded,
						Status: corev1.ConditionFalse,
					}},
				},
				TaskRunStatusFields: v1.TaskRunStatusFields{
					StartTime:      &startTime,
					CompletionTime: &completionTime,
				},
			},
		},
		metricName: "pipelinerun_taskrun_duration_seconds",
		expectedDurationTags: map[string]string{
			"pipeline":    "pipeline-1",
			"pipelinerun": "pipelinerun-1",
			"task":        "task-1",
			"taskrun":     "taskrun-1",
			"namespace":   "ns",
			"status":      "failed",
		},
		expectedCountTags: map[string]string{
			"status": "failed",
		},
		expectedDuration: 60,
		expectedCount:    1,
		beforeCondition:  nil,
		countWithReason:  false,
	}, {
		name: "for failed taskrun in pipelinerun with reason",
		taskRun: &v1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{
				Name: "taskrun-1", Namespace: "ns",
				Labels: map[string]string{
					pipeline.PipelineLabelKey:    "pipeline-1",
					pipeline.PipelineRunLabelKey: "pipelinerun-1",
				},
			},
			Spec: v1.TaskRunSpec{
				TaskRef: &v1.TaskRef{Name: "task-1"},
			},
			Status: v1.TaskRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{{
						Type:   apis.ConditionSucceeded,
						Status: corev1.ConditionFalse,
						Reason: "TaskRunImagePullFailed",
					}},
				},
				TaskRunStatusFields: v1.TaskRunStatusFields{
					StartTime:      &startTime,
					CompletionTime: &completionTime,
				},
			},
		},
		metricName: "pipelinerun_taskrun_duration_seconds",
		expectedDurationTags: map[string]string{
			"pipeline":    "pipeline-1",
			"pipelinerun": "pipelinerun-1",
			"task":        "task-1",
			"taskrun":     "taskrun-1",
			"namespace":   "ns",
			"reason":      "TaskRunImagePullFailed",
			"status":      "failed",
		},
		expectedCountTags: map[string]string{
			"status": "failed",
			"reason": "TaskRunImagePullFailed",
		},
		expectedDuration: 60,
		expectedCount:    1,
		beforeCondition:  nil,
		countWithReason:  true,
	}} {
		t.Run(c.name, func(t *testing.T) {
			unregisterMetrics()

			ctx := getConfigContext(c.countWithReason, false)
			metrics, err := NewRecorder(ctx)
			if err != nil {
				t.Fatalf("NewRecorder: %v", err)
			}

			if err := metrics.DurationAndCount(ctx, c.taskRun, c.beforeCondition); err != nil {
				t.Errorf("DurationAndCount: %v", err)
			}
			if c.expectedCountTags != nil {
				delete(c.expectedCountTags, "reason")
				metricstest.CheckCountData(t, "taskrun_total", c.expectedCountTags, c.expectedCount)
			} else {
				metricstest.CheckStatsNotReported(t, "taskrun_total")
			}
			if c.expectedDurationTags != nil {
				metricstest.CheckLastValueData(t, c.metricName, c.expectedDurationTags, c.expectedDuration)
			} else {
				metricstest.CheckStatsNotReported(t, c.metricName)
			}
		})
	}
}

func TestRecordRunningTaskRunsCount(t *testing.T) {
	unregisterMetrics()
	newTaskRun := func(status corev1.ConditionStatus) *v1.TaskRun {
		return &v1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{Name: names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("taskrun-")},
			Status: v1.TaskRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{{
						Type:   apis.ConditionSucceeded,
						Status: status,
					}},
				},
			},
		}
	}

	ctx, _ := ttesting.SetupFakeContext(t)
	informer := faketaskruninformer.Get(ctx)
	// Add N randomly-named TaskRuns with differently-succeeded statuses.
	for _, tr := range []*v1.TaskRun{
		newTaskRun(corev1.ConditionTrue),
		newTaskRun(corev1.ConditionUnknown),
		newTaskRun(corev1.ConditionFalse),
	} {
		if err := informer.Informer().GetIndexer().Add(tr); err != nil {
			t.Fatalf("Adding TaskRun to informer: %v", err)
		}
	}

	ctx = getConfigContext(false, false)
	metrics, err := NewRecorder(ctx)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	if err := metrics.RunningTaskRuns(ctx, informer.Lister()); err != nil {
		t.Errorf("RunningTaskRuns: %v", err)
	}
}

func TestRecordRunningTaskRunsThrottledCounts(t *testing.T) {
	multiplier := 3
	for _, tc := range []struct {
		status     corev1.ConditionStatus
		reason     string
		nodeCount  float64
		quotaCount float64
		waitCount  float64
		addNS      bool
	}{
		{
			status: corev1.ConditionTrue,
			reason: "",
		},
		{
			status: corev1.ConditionTrue,
			reason: pod.ReasonExceededResourceQuota,
		},
		{
			status: corev1.ConditionTrue,
			reason: pod.ReasonExceededResourceQuota,
			addNS:  true,
		},
		{
			status: corev1.ConditionTrue,
			reason: pod.ReasonExceededNodeResources,
		},
		{
			status: corev1.ConditionTrue,
			reason: pod.ReasonExceededNodeResources,
			addNS:  true,
		},
		{
			status: corev1.ConditionTrue,
			reason: v1.TaskRunReasonResolvingTaskRef,
		},
		{
			status: corev1.ConditionFalse,
			reason: "",
		},
		{
			status: corev1.ConditionFalse,
			reason: pod.ReasonExceededResourceQuota,
		},
		{
			status: corev1.ConditionFalse,
			reason: pod.ReasonExceededNodeResources,
		},
		{
			status: corev1.ConditionFalse,
			reason: v1.TaskRunReasonResolvingTaskRef,
		},
		{
			status: corev1.ConditionUnknown,
			reason: "",
		},
		{
			status:     corev1.ConditionUnknown,
			reason:     pod.ReasonExceededResourceQuota,
			quotaCount: 3,
		},
		{
			status:    corev1.ConditionUnknown,
			reason:    pod.ReasonExceededNodeResources,
			nodeCount: 3,
		},
		{
			status:     corev1.ConditionUnknown,
			reason:     pod.ReasonExceededResourceQuota,
			quotaCount: 3,
			addNS:      true,
		},
		{
			status:    corev1.ConditionUnknown,
			reason:    pod.ReasonExceededNodeResources,
			nodeCount: 3,
			addNS:     true,
		},
		{
			status:    corev1.ConditionUnknown,
			reason:    v1.TaskRunReasonResolvingTaskRef,
			waitCount: 3,
		},
	} {
		unregisterMetrics()
		ctx, _ := ttesting.SetupFakeContext(t)
		informer := faketaskruninformer.Get(ctx)
		for range multiplier {
			tr := &v1.TaskRun{
				ObjectMeta: metav1.ObjectMeta{Name: names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("taskrun-"), Namespace: "test"},
				Status: v1.TaskRunStatus{
					Status: duckv1.Status{
						Conditions: duckv1.Conditions{{
							Type:   apis.ConditionSucceeded,
							Status: tc.status,
							Reason: tc.reason,
						}},
					},
				},
			}
			if err := informer.Informer().GetIndexer().Add(tr); err != nil {
				t.Fatalf("Adding TaskRun to informer: %v", err)
			}
		}

		ctx = getConfigContext(false, tc.addNS)
		metrics, err := NewRecorder(ctx)
		if err != nil {
			t.Fatalf("NewRecorder: %v", err)
		}

		if err := metrics.RunningTaskRuns(ctx, informer.Lister()); err != nil {
			t.Errorf("RunningTaskRuns: %v", err)
		}
		nsMap := map[string]string{}
		if tc.addNS {
			nsMap = map[string]string{namespaceTag.Name(): "test"}
		}
		metricstest.CheckLastValueData(t, "running_taskruns_throttled_by_quota", nsMap, tc.quotaCount)
		metricstest.CheckLastValueData(t, "running_taskruns_throttled_by_node", nsMap, tc.nodeCount)
		metricstest.CheckLastValueData(t, "running_taskruns_waiting_on_task_resolution_count", map[string]string{}, tc.waitCount)
	}
}

func TestRecordPodLatency(t *testing.T) {
	creationTime := metav1.Now()

	taskRun := &v1.TaskRun{
		ObjectMeta: metav1.ObjectMeta{Name: "test-taskrun", Namespace: "foo"},
		Spec: v1.TaskRunSpec{
			TaskRef: &v1.TaskRef{Name: "task-1"},
		},
	}
	trFromRemoteTask := &v1.TaskRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-taskrun",
			Namespace: "foo",
			Labels: map[string]string{
				pipeline.TaskLabelKey: "task-remote",
			},
		},
		Spec: v1.TaskRunSpec{
			TaskRef: &v1.TaskRef{
				ResolverRef: v1.ResolverRef{Resolver: "task-remote"},
			},
		},
	}
	emptyLabelTRFromRemoteTask := &v1.TaskRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-taskrun",
			Namespace: "foo",
		},
		Spec: v1.TaskRunSpec{
			TaskRef: &v1.TaskRef{
				ResolverRef: v1.ResolverRef{Resolver: "task-remote"},
			},
		},
	}
	for _, td := range []struct {
		name           string
		pod            *corev1.Pod
		expectedTags   map[string]string
		expectedValue  float64
		expectingError bool
		taskRun        *v1.TaskRun
	}{{
		name: "for scheduled pod",
		pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "test-taskrun-pod-123456",
				Namespace:         "foo",
				CreationTimestamp: creationTime,
			},
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{{
					Type:               corev1.PodScheduled,
					LastTransitionTime: metav1.Time{Time: creationTime.Add(4 * time.Second)},
				}},
			},
		},
		expectedTags: map[string]string{
			"pod":       "test-taskrun-pod-123456",
			"task":      "task-1",
			"taskrun":   "test-taskrun",
			"namespace": "foo",
		},
		expectedValue: 4000,
		taskRun:       taskRun,
	}, {
		name: "for scheduled pod with reference remote task",
		pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "test-taskrun-pod-123456",
				Namespace:         "foo",
				CreationTimestamp: creationTime,
			},
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{{
					Type:               corev1.PodScheduled,
					LastTransitionTime: metav1.Time{Time: creationTime.Add(4 * time.Second)},
				}},
			},
		},
		expectedTags: map[string]string{
			"pod":       "test-taskrun-pod-123456",
			"task":      "task-remote",
			"taskrun":   "test-taskrun",
			"namespace": "foo",
		},
		expectedValue: 4000,
		taskRun:       trFromRemoteTask,
	}, {
		name: "for scheduled pod - empty label tr reference remote task",
		pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "test-taskrun-pod-123456",
				Namespace:         "foo",
				CreationTimestamp: creationTime,
			},
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{{
					Type:               corev1.PodScheduled,
					LastTransitionTime: metav1.Time{Time: creationTime.Add(4 * time.Second)},
				}},
			},
		},
		expectedTags: map[string]string{
			"pod":       "test-taskrun-pod-123456",
			"task":      anonymous,
			"taskrun":   "test-taskrun",
			"namespace": "foo",
		},
		expectedValue: 4000,
		taskRun:       emptyLabelTRFromRemoteTask,
	}, {
		name: "for non scheduled pod",
		pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "test-taskrun-pod-123456",
				Namespace:         "foo",
				CreationTimestamp: creationTime,
			},
			Status: corev1.PodStatus{},
		},
		expectingError: true,
		taskRun:        taskRun,
	}} {
		t.Run(td.name, func(t *testing.T) {
			unregisterMetrics()

			ctx := getConfigContext(false, false)
			metrics, err := NewRecorder(ctx)
			if err != nil {
				t.Fatalf("NewRecorder: %v", err)
			}

			if err := metrics.RecordPodLatency(ctx, td.pod, td.taskRun); td.expectingError && err == nil {
				t.Error("RecordPodLatency wanted error, got nil")
			} else if !td.expectingError {
				if err != nil {
					t.Errorf("RecordPodLatency: %v", err)
				}
				metricstest.CheckLastValueData(t, "taskruns_pod_latency_milliseconds", td.expectedTags, td.expectedValue)
			}
		})
	}
}

func TestTaskRunIsOfPipelinerun(t *testing.T) {
	tests := []struct {
		name                  string
		tr                    *v1.TaskRun
		expectedValue         bool
		expetectedPipeline    string
		expetectedPipelineRun string
	}{{
		name: "yes",
		tr: &v1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					pipeline.PipelineLabelKey:    "pipeline",
					pipeline.PipelineRunLabelKey: "pipelinerun",
				},
			},
		},
		expectedValue:         true,
		expetectedPipeline:    "pipeline",
		expetectedPipelineRun: "pipelinerun",
	}, {
		name:          "no",
		tr:            &v1.TaskRun{},
		expectedValue: false,
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, pipeline, pipelineRun := IsPartOfPipeline(test.tr)
			if value != test.expectedValue {
				t.Fatalf("Expecting %v got %v", test.expectedValue, value)
			}

			if pipeline != test.expetectedPipeline {
				t.Fatalf("Mismatch in pipeline: got %s expected %s", pipeline, test.expetectedPipeline)
			}

			if pipelineRun != test.expetectedPipelineRun {
				t.Fatalf("Mismatch in pipelinerun: got %s expected %s", pipelineRun, test.expetectedPipelineRun)
			}
		})
	}
}

func unregisterMetrics() {
	metricstest.Unregister("taskrun_duration_seconds", "pipelinerun_taskrun_duration_seconds", "running_taskruns_waiting_on_task_resolution_count", "taskruns_pod_latency_milliseconds", "taskrun_total", "running_taskruns", "running_taskruns_throttled_by_quota", "running_taskruns_throttled_by_node")

	// Allow the recorder singleton to be recreated.
	once = sync.Once{}
	r = nil
	errRegistering = nil
}
