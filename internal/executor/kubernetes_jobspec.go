package executor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"workflowfiesta-runner/internal/config"
)

// Labels the runner always puts on script Jobs AND their pod templates, so
// NetworkPolicies, admission policies, quotas and monitoring can select the
// pods. User labels from KUBERNETES_JOB_LABELS cannot override these.
const (
	LabelManagedBy     = "app.kubernetes.io/managed-by"
	LabelManagedByVal  = "workflowfiesta-runner"
	LabelComponent     = "app.kubernetes.io/component"
	LabelComponentVal  = "script-job"
	AnnotationJobID    = "workflowfiesta.com/job-id"
	jobDeadlineGrace   = 60 * time.Second
	jobTmpVolume       = "tmp"
	jobWorkspaceVolume = "workspace"
)

// ValidateKubernetesConfig reports invalid KUBERNETES_JOB_* settings. Call it
// at startup so a typo fails fast instead of on the first job.
func ValidateKubernetesConfig(cfg *config.Config) error {
	if cfg.KubeJobErr != nil {
		return cfg.KubeJobErr
	}
	_, err := jobResources(cfg.KubeJob)
	if err != nil {
		return err
	}
	if _, err := jobTolerations(cfg.KubeJob); err != nil {
		return err
	}
	return nil
}

// effectiveTimeout applies KUBERNETES_JOB_MAX_TIMEOUT_SECONDS to the timeout
// the API asked for.
func effectiveTimeout(spec config.KubeJob, requested time.Duration) time.Duration {
	if spec.MaxTimeoutSeconds > 0 {
		max := time.Duration(spec.MaxTimeoutSeconds) * time.Second
		if requested <= 0 || requested > max {
			return max
		}
	}
	return requested
}

// checkImageAllowed enforces KUBERNETES_JOB_ALLOWED_IMAGES and
// KUBERNETES_JOB_REQUIRE_IMAGE_DIGEST before anything is created.
func checkImageAllowed(spec config.KubeJob, image string) error {
	if spec.RequireImageDigest && !strings.Contains(image, "@sha256:") {
		return fmt.Errorf("image %q is not pinned by digest (KUBERNETES_JOB_REQUIRE_IMAGE_DIGEST=true)", image)
	}
	if len(spec.AllowedImages) == 0 {
		return nil
	}
	for _, prefix := range spec.AllowedImages {
		if image == prefix || strings.HasPrefix(image, prefix) {
			return nil
		}
	}
	return fmt.Errorf("image %q is not in KUBERNETES_JOB_ALLOWED_IMAGES", image)
}

// buildJob returns the batch/v1 Job for one script step. It is pure so the
// whole pod spec can be unit-tested without a cluster.
func buildJob(cfg *config.Config, jobName string, input Input, image string, timeout time.Duration) (*batchv1.Job, error) {
	spec := cfg.KubeJob

	labels := map[string]string{}
	for k, v := range spec.Labels {
		labels[k] = v
	}
	labels[LabelManagedBy] = LabelManagedByVal
	labels[LabelComponent] = LabelComponentVal

	annotations := map[string]string{}
	for k, v := range spec.Annotations {
		annotations[k] = v
	}
	if input.JobID != "" {
		annotations[AnnotationJobID] = input.JobID
	}

	envVars := make([]corev1.EnvVar, 0, len(input.EnvVars)+1)
	for _, key := range config.SortedKeys(input.EnvVars) {
		envVars = append(envVars, corev1.EnvVar{Name: key, Value: input.EnvVars[key]})
	}

	resources, err := jobResources(spec)
	if err != nil {
		return nil, err
	}
	tolerations, err := jobTolerations(spec)
	if err != nil {
		return nil, err
	}

	backoffLimit := int32(0)
	ttl := int32(300) // clean up after 5 minutes
	automount := spec.AutomountToken
	serviceLinks := spec.EnableServiceLinks
	// Kubernetes kills the pod even if the runner dies mid-job.
	deadline := int64((timeout + jobDeadlineGrace) / time.Second)

	container := corev1.Container{
		Name:      "runner",
		Image:     image,
		Command:   []string{"/bin/sh", "-c", input.Script},
		Env:       envVars,
		Resources: resources,
	}
	podSpec := corev1.PodSpec{
		RestartPolicy:                corev1.RestartPolicyNever,
		ServiceAccountName:           spec.ServiceAccountName,
		AutomountServiceAccountToken: &automount,
		EnableServiceLinks:           &serviceLinks,
		NodeSelector:                 spec.NodeSelector,
		Tolerations:                  tolerations,
	}
	if spec.RuntimeClassName != "" {
		rc := spec.RuntimeClassName
		podSpec.RuntimeClassName = &rc
	}
	if cfg.KubeImagePullSecret != "" {
		podSpec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: cfg.KubeImagePullSecret}}
	}

	if spec.SecurityProfile == config.KubeJobProfileRestricted {
		nonRoot := true
		noEscalation := false
		uid := spec.RunAsUser
		podSpec.SecurityContext = &corev1.PodSecurityContext{
			RunAsNonRoot:   &nonRoot,
			RunAsUser:      &uid,
			RunAsGroup:     &uid,
			FSGroup:        &uid,
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		}
		container.SecurityContext = &corev1.SecurityContext{
			RunAsNonRoot:             &nonRoot,
			AllowPrivilegeEscalation: &noEscalation,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		}
	}
	if spec.ReadOnlyRootFS {
		ro := true
		if container.SecurityContext == nil {
			container.SecurityContext = &corev1.SecurityContext{}
		}
		container.SecurityContext.ReadOnlyRootFilesystem = &ro
		// Scripts still get writable scratch space.
		podSpec.Volumes = append(podSpec.Volumes,
			corev1.Volume{Name: jobTmpVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			corev1.Volume{Name: jobWorkspaceVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		)
		container.VolumeMounts = append(container.VolumeMounts,
			corev1.VolumeMount{Name: jobTmpVolume, MountPath: "/tmp"},
			corev1.VolumeMount{Name: jobWorkspaceVolume, MountPath: "/workspace"},
		)
		container.WorkingDir = "/workspace"
		if _, ok := input.EnvVars["HOME"]; !ok {
			container.Env = append(container.Env, corev1.EnvVar{Name: "HOME", Value: "/tmp"})
		}
	}
	podSpec.Containers = []corev1.Container{container}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        jobName,
			Namespace:   cfg.KubeNamespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &deadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: annotations},
				Spec:       podSpec,
			},
		},
	}, nil
}

func jobResources(spec config.KubeJob) (corev1.ResourceRequirements, error) {
	var rr corev1.ResourceRequirements
	for _, q := range []struct {
		env, val string
		name     corev1.ResourceName
		limit    bool
	}{
		{"KUBERNETES_JOB_CPU_REQUEST", spec.CPURequest, corev1.ResourceCPU, false},
		{"KUBERNETES_JOB_CPU_LIMIT", spec.CPULimit, corev1.ResourceCPU, true},
		{"KUBERNETES_JOB_MEMORY_REQUEST", spec.MemoryRequest, corev1.ResourceMemory, false},
		{"KUBERNETES_JOB_MEMORY_LIMIT", spec.MemoryLimit, corev1.ResourceMemory, true},
		{"KUBERNETES_JOB_EPHEMERAL_STORAGE_REQUEST", spec.EphemeralStorageRequest, corev1.ResourceEphemeralStorage, false},
		{"KUBERNETES_JOB_EPHEMERAL_STORAGE_LIMIT", spec.EphemeralStorageLimit, corev1.ResourceEphemeralStorage, true},
	} {
		if q.val == "" {
			continue
		}
		qty, err := resource.ParseQuantity(q.val)
		if err != nil {
			return rr, fmt.Errorf("%s=%q: %v", q.env, q.val, err)
		}
		if q.limit {
			if rr.Limits == nil {
				rr.Limits = corev1.ResourceList{}
			}
			rr.Limits[q.name] = qty
		} else {
			if rr.Requests == nil {
				rr.Requests = corev1.ResourceList{}
			}
			rr.Requests[q.name] = qty
		}
	}
	return rr, nil
}

func jobTolerations(spec config.KubeJob) ([]corev1.Toleration, error) {
	if spec.TolerationsJSON == "" {
		return nil, nil
	}
	var t []corev1.Toleration
	if err := json.Unmarshal([]byte(spec.TolerationsJSON), &t); err != nil {
		return nil, fmt.Errorf("KUBERNETES_JOB_TOLERATIONS: want a JSON array of tolerations: %v", err)
	}
	return t, nil
}
