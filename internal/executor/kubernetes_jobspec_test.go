package executor_test

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"workflowfiesta-runner/internal/config"
	"workflowfiesta-runner/internal/executor"
)

func buildTestJob(t *testing.T, kj config.KubeJob, input executor.Input) *corev1.PodSpec {
	t.Helper()
	if kj.SecurityProfile == "" {
		kj.SecurityProfile = config.KubeJobProfileNone
	}
	cfg := &config.Config{KubeNamespace: "wf-jobs", KubeJob: kj}
	job, err := executor.BuildJob(cfg, "wf-abc123", input, "alpine:3.20", 30*time.Second)
	if err != nil {
		t.Fatalf("BuildJob: %v", err)
	}
	if job.Namespace != "wf-jobs" {
		t.Errorf("namespace = %q", job.Namespace)
	}
	return &job.Spec.Template.Spec
}

func TestBuildJob_UsesBash(t *testing.T) {
	pod := buildTestJob(t, config.KubeJob{}, executor.Input{Script: "echo hi"})
	cmd := pod.Containers[0].Command
	if len(cmd) != 3 || cmd[0] != "/bin/bash" || cmd[1] != "-c" || cmd[2] != "echo hi" {
		t.Fatalf("command = %#v", cmd)
	}
}

func TestBuildJob_PodTemplateCarriesSelectableLabels(t *testing.T) {
	cfg := &config.Config{KubeNamespace: "ns", KubeJob: config.KubeJob{
		SecurityProfile: config.KubeJobProfileNone,
		Labels:          map[string]string{"team": "qa", executor.LabelManagedBy: "spoofed"},
	}}
	job, err := executor.BuildJob(cfg, "wf-1", executor.Input{JobID: "job-42", Script: "true"}, "alpine:3.20", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for name, labels := range map[string]map[string]string{"job": job.Labels, "pod": job.Spec.Template.Labels} {
		if labels[executor.LabelManagedBy] != executor.LabelManagedByVal {
			t.Errorf("%s managed-by = %q, want %q", name, labels[executor.LabelManagedBy], executor.LabelManagedByVal)
		}
		if labels[executor.LabelComponent] != executor.LabelComponentVal {
			t.Errorf("%s component label missing", name)
		}
		if labels["team"] != "qa" {
			t.Errorf("%s user label missing", name)
		}
	}
	if job.Spec.Template.Annotations[executor.AnnotationJobID] != "job-42" {
		t.Errorf("pod job-id annotation = %q", job.Spec.Template.Annotations[executor.AnnotationJobID])
	}
}

func TestBuildJob_DefaultsDoNotMountServiceAccountToken(t *testing.T) {
	pod := buildTestJob(t, config.KubeJob{}, executor.Input{Script: "true"})
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Fatal("service account token must not be mounted by default")
	}
	if pod.ServiceAccountName != "" {
		t.Errorf("ServiceAccountName = %q, want empty", pod.ServiceAccountName)
	}
	if pod.SecurityContext != nil || pod.Containers[0].SecurityContext != nil {
		t.Error("profile none must not set a securityContext")
	}
}

func TestBuildJob_DedicatedServiceAccountAndScheduling(t *testing.T) {
	pod := buildTestJob(t, config.KubeJob{
		ServiceAccountName: "wf-script-job",
		AutomountToken:     true,
		NodeSelector:       map[string]string{"kubernetes.io/arch": "amd64"},
		TolerationsJSON:    `[{"key":"dedicated","operator":"Equal","value":"wff","effect":"NoSchedule"}]`,
		RuntimeClassName:   "gvisor",
	}, executor.Input{Script: "true"})
	if pod.ServiceAccountName != "wf-script-job" {
		t.Errorf("ServiceAccountName = %q", pod.ServiceAccountName)
	}
	if !*pod.AutomountServiceAccountToken {
		t.Error("explicit opt-in should mount the token")
	}
	if pod.NodeSelector["kubernetes.io/arch"] != "amd64" {
		t.Error("nodeSelector not applied")
	}
	if len(pod.Tolerations) != 1 || pod.Tolerations[0].Key != "dedicated" {
		t.Errorf("tolerations = %+v", pod.Tolerations)
	}
	if pod.RuntimeClassName == nil || *pod.RuntimeClassName != "gvisor" {
		t.Error("runtimeClassName not applied")
	}
}

func TestBuildJob_ResourcesAndDeadline(t *testing.T) {
	cfg := &config.Config{KubeNamespace: "ns", KubeJob: config.KubeJob{
		SecurityProfile: config.KubeJobProfileNone,
		CPURequest:      "250m", CPULimit: "1",
		MemoryRequest: "512Mi", MemoryLimit: "2Gi",
		EphemeralStorageLimit: "1Gi",
	}}
	job, err := executor.BuildJob(cfg, "wf-1", executor.Input{Script: "true"}, "alpine:3.20", 120*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	r := job.Spec.Template.Spec.Containers[0].Resources
	if r.Requests.Cpu().String() != "250m" || r.Limits.Cpu().String() != "1" {
		t.Errorf("cpu = %v / %v", r.Requests.Cpu(), r.Limits.Cpu())
	}
	if r.Requests.Memory().String() != "512Mi" || r.Limits.Memory().String() != "2Gi" {
		t.Errorf("memory = %v / %v", r.Requests.Memory(), r.Limits.Memory())
	}
	if r.Limits.StorageEphemeral().String() != "1Gi" {
		t.Errorf("ephemeral = %v", r.Limits.StorageEphemeral())
	}
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != 180 {
		t.Errorf("activeDeadlineSeconds = %v, want 180 (timeout + 60s grace)", job.Spec.ActiveDeadlineSeconds)
	}
}

func TestBuildJob_InvalidQuantityFails(t *testing.T) {
	cfg := &config.Config{KubeJob: config.KubeJob{SecurityProfile: config.KubeJobProfileNone, MemoryLimit: "lots"}}
	if _, err := executor.BuildJob(cfg, "wf-1", executor.Input{}, "alpine", time.Minute); err == nil {
		t.Fatal("expected error for invalid memory quantity")
	}
}

func TestBuildJob_RestrictedProfilePassesPSSRestricted(t *testing.T) {
	pod := buildTestJob(t, config.KubeJob{
		SecurityProfile: config.KubeJobProfileRestricted,
		RunAsUser:       65532,
		ReadOnlyRootFS:  true,
	}, executor.Input{Script: "true", EnvVars: map[string]string{"A": "1"}})

	psc := pod.SecurityContext
	if psc == nil || psc.RunAsNonRoot == nil || !*psc.RunAsNonRoot || *psc.RunAsUser != 65532 {
		t.Fatalf("pod securityContext = %+v", psc)
	}
	if psc.SeccompProfile == nil || psc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Error("seccomp RuntimeDefault missing")
	}
	c := pod.Containers[0]
	sc := c.SecurityContext
	if sc == nil || sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Fatal("allowPrivilegeEscalation must be false")
	}
	if sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
		t.Error("capabilities must drop ALL")
	}
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Error("readOnlyRootFilesystem must be true")
	}
	mounts := map[string]bool{}
	for _, m := range c.VolumeMounts {
		mounts[m.MountPath] = true
	}
	if !mounts["/tmp"] || !mounts["/workspace"] {
		t.Errorf("writable scratch mounts missing: %+v", c.VolumeMounts)
	}
	home := ""
	for _, e := range c.Env {
		if e.Name == "HOME" {
			home = e.Value
		}
	}
	if home != "/tmp" {
		t.Errorf("HOME = %q, want /tmp", home)
	}
	for _, v := range pod.Volumes {
		if v.HostPath != nil {
			t.Error("hostPath volume must never be added")
		}
	}
}

func TestCheckImageAllowed(t *testing.T) {
	spec := config.KubeJob{AllowedImages: []string{"123.dkr.ecr.us-west-2.amazonaws.com/wf-job@sha256:", "public.ecr.aws/docker/library/debian:bookworm-slim"}}
	ok := []string{
		"123.dkr.ecr.us-west-2.amazonaws.com/wf-job@sha256:abc",
		"public.ecr.aws/docker/library/debian:bookworm-slim",
	}
	for _, img := range ok {
		if err := executor.CheckImageAllowed(spec, img); err != nil {
			t.Errorf("%s: unexpected error %v", img, err)
		}
	}
	for _, img := range []string{"alpine:3.20", "evil.example/debian:bookworm-slim", ""} {
		if err := executor.CheckImageAllowed(spec, img); err == nil {
			t.Errorf("%q: expected rejection", img)
		}
	}
	if err := executor.CheckImageAllowed(config.KubeJob{RequireImageDigest: true}, "alpine:3.20"); err == nil {
		t.Error("tag-only image must be rejected when a digest is required")
	}
	if err := executor.CheckImageAllowed(config.KubeJob{}, "anything"); err != nil {
		t.Errorf("no policy should allow everything: %v", err)
	}
}

func TestEffectiveTimeout(t *testing.T) {
	capped := config.KubeJob{MaxTimeoutSeconds: 60}
	if got := executor.EffectiveTimeout(capped, 5*time.Minute); got != time.Minute {
		t.Errorf("capped = %v", got)
	}
	if got := executor.EffectiveTimeout(capped, 10*time.Second); got != 10*time.Second {
		t.Errorf("under cap = %v", got)
	}
	if got := executor.EffectiveTimeout(config.KubeJob{}, 5*time.Minute); got != 5*time.Minute {
		t.Errorf("no cap = %v", got)
	}
}
