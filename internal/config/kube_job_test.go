package config_test

import (
	"strings"
	"testing"

	"workflowfiesta-runner/internal/config"
)

func TestLoadKubeJob_Defaults(t *testing.T) {
	j, err := config.LoadKubeJob()
	if err != nil {
		t.Fatal(err)
	}
	if j.AutomountToken {
		t.Error("token must not be mounted by default")
	}
	if j.SecurityProfile != config.KubeJobProfileNone || j.ReadOnlyRootFS || !j.EnableServiceLinks {
		t.Errorf("unexpected defaults: %+v", j)
	}
}

func TestLoadKubeJob_RestrictedFlipsSecureDefaults(t *testing.T) {
	t.Setenv("KUBERNETES_JOB_SECURITY_PROFILE", "Restricted")
	t.Setenv("KUBERNETES_JOB_SERVICE_ACCOUNT", "wf-script-job")
	t.Setenv("KUBERNETES_JOB_LABELS", "team=qa, env=sandbox")
	t.Setenv("KUBERNETES_JOB_ALLOWED_IMAGES", "a/b@sha256:, c/d:1")
	t.Setenv("KUBERNETES_JOB_MAX_TIMEOUT_SECONDS", "300")
	j, err := config.LoadKubeJob()
	if err != nil {
		t.Fatal(err)
	}
	if j.SecurityProfile != config.KubeJobProfileRestricted || !j.ReadOnlyRootFS || j.EnableServiceLinks {
		t.Errorf("restricted defaults not applied: %+v", j)
	}
	if j.RunAsUser != config.DefaultKubeJobRunAsUser {
		t.Errorf("RunAsUser = %d", j.RunAsUser)
	}
	if j.Labels["env"] != "sandbox" || len(j.AllowedImages) != 2 || j.MaxTimeoutSeconds != 300 || j.ServiceAccountName != "wf-script-job" {
		t.Errorf("parsed = %+v", j)
	}
}

func TestLoadKubeJob_RejectsBadValues(t *testing.T) {
	t.Setenv("KUBERNETES_JOB_SECURITY_PROFILE", "paranoid")
	t.Setenv("KUBERNETES_JOB_RUN_AS_USER", "0")
	t.Setenv("KUBERNETES_JOB_NODE_SELECTOR", "novalue")
	t.Setenv("KUBERNETES_JOB_AUTOMOUNT_SERVICE_ACCOUNT_TOKEN", "maybe")
	_, err := config.LoadKubeJob()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"SECURITY_PROFILE", "RUN_AS_USER", "NODE_SELECTOR", "AUTOMOUNT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}
