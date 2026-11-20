package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Job security profiles for the Kubernetes executor.
const (
	// KubeJobProfileNone keeps the historical pod spec: no pod or container
	// securityContext is set, so the image's defaults apply.
	KubeJobProfileNone = "none"
	// KubeJobProfileRestricted produces pods that pass the Kubernetes Pod
	// Security Standards "restricted" level.
	KubeJobProfileRestricted = "restricted"
)

// DefaultKubeJobRunAsUser is the UID used by the restricted profile when
// KUBERNETES_JOB_RUN_AS_USER is unset.
const DefaultKubeJobRunAsUser int64 = 65532

// KubeJob holds the pod spec controls for the script Jobs created by the
// Kubernetes executor. Every field is optional. The zero value reproduces the
// historical Job spec except for the service account token, which is no longer
// mounted unless KUBERNETES_JOB_AUTOMOUNT_SERVICE_ACCOUNT_TOKEN=true.
type KubeJob struct {
	ServiceAccountName string            // KUBERNETES_JOB_SERVICE_ACCOUNT
	AutomountToken     bool              // KUBERNETES_JOB_AUTOMOUNT_SERVICE_ACCOUNT_TOKEN (default false)
	Labels             map[string]string // KUBERNETES_JOB_LABELS, "k=v,k2=v2", added to Job and pod
	Annotations        map[string]string // KUBERNETES_JOB_ANNOTATIONS, "k=v,k2=v2", added to Job and pod
	NodeSelector       map[string]string // KUBERNETES_JOB_NODE_SELECTOR, "k=v,k2=v2"
	TolerationsJSON    string            // KUBERNETES_JOB_TOLERATIONS, JSON array of core/v1 Toleration
	RuntimeClassName   string            // KUBERNETES_JOB_RUNTIME_CLASS (e.g. gvisor, kata)

	CPURequest              string // KUBERNETES_JOB_CPU_REQUEST
	CPULimit                string // KUBERNETES_JOB_CPU_LIMIT
	MemoryRequest           string // KUBERNETES_JOB_MEMORY_REQUEST
	MemoryLimit             string // KUBERNETES_JOB_MEMORY_LIMIT
	EphemeralStorageRequest string // KUBERNETES_JOB_EPHEMERAL_STORAGE_REQUEST
	EphemeralStorageLimit   string // KUBERNETES_JOB_EPHEMERAL_STORAGE_LIMIT

	SecurityProfile    string // KUBERNETES_JOB_SECURITY_PROFILE: "none" (default) or "restricted"
	RunAsUser          int64  // KUBERNETES_JOB_RUN_AS_USER (restricted profile; default 65532)
	ReadOnlyRootFS     bool   // KUBERNETES_JOB_READ_ONLY_ROOT_FILESYSTEM (default true when restricted)
	EnableServiceLinks bool   // KUBERNETES_JOB_ENABLE_SERVICE_LINKS (default true, false when restricted)

	MaxTimeoutSeconds  int      // KUBERNETES_JOB_MAX_TIMEOUT_SECONDS, caps the server-requested timeout (0 = no cap)
	AllowedImages      []string // KUBERNETES_JOB_ALLOWED_IMAGES, comma-separated image prefixes
	RequireImageDigest bool     // KUBERNETES_JOB_REQUIRE_IMAGE_DIGEST, reject images not pinned by @sha256:
}

// LoadKubeJob reads the KUBERNETES_JOB_* environment variables.
func LoadKubeJob() (KubeJob, error) {
	var errs []string
	j := KubeJob{
		ServiceAccountName: strings.TrimSpace(os.Getenv("KUBERNETES_JOB_SERVICE_ACCOUNT")),
		TolerationsJSON:    strings.TrimSpace(os.Getenv("KUBERNETES_JOB_TOLERATIONS")),
		RuntimeClassName:   strings.TrimSpace(os.Getenv("KUBERNETES_JOB_RUNTIME_CLASS")),

		CPURequest:              strings.TrimSpace(os.Getenv("KUBERNETES_JOB_CPU_REQUEST")),
		CPULimit:                strings.TrimSpace(os.Getenv("KUBERNETES_JOB_CPU_LIMIT")),
		MemoryRequest:           strings.TrimSpace(os.Getenv("KUBERNETES_JOB_MEMORY_REQUEST")),
		MemoryLimit:             strings.TrimSpace(os.Getenv("KUBERNETES_JOB_MEMORY_LIMIT")),
		EphemeralStorageRequest: strings.TrimSpace(os.Getenv("KUBERNETES_JOB_EPHEMERAL_STORAGE_REQUEST")),
		EphemeralStorageLimit:   strings.TrimSpace(os.Getenv("KUBERNETES_JOB_EPHEMERAL_STORAGE_LIMIT")),

		SecurityProfile: strings.ToLower(strings.TrimSpace(os.Getenv("KUBERNETES_JOB_SECURITY_PROFILE"))),
	}
	if j.SecurityProfile == "" {
		j.SecurityProfile = KubeJobProfileNone
	}
	if j.SecurityProfile != KubeJobProfileNone && j.SecurityProfile != KubeJobProfileRestricted {
		errs = append(errs, fmt.Sprintf("KUBERNETES_JOB_SECURITY_PROFILE=%q: want %q or %q", j.SecurityProfile, KubeJobProfileNone, KubeJobProfileRestricted))
	}
	restricted := j.SecurityProfile == KubeJobProfileRestricted

	var err error
	if j.AutomountToken, err = envBool("KUBERNETES_JOB_AUTOMOUNT_SERVICE_ACCOUNT_TOKEN", false); err != nil {
		errs = append(errs, err.Error())
	}
	if j.ReadOnlyRootFS, err = envBool("KUBERNETES_JOB_READ_ONLY_ROOT_FILESYSTEM", restricted); err != nil {
		errs = append(errs, err.Error())
	}
	if j.EnableServiceLinks, err = envBool("KUBERNETES_JOB_ENABLE_SERVICE_LINKS", !restricted); err != nil {
		errs = append(errs, err.Error())
	}
	if j.RequireImageDigest, err = envBool("KUBERNETES_JOB_REQUIRE_IMAGE_DIGEST", false); err != nil {
		errs = append(errs, err.Error())
	}
	for _, kv := range []struct {
		name string
		dst  *map[string]string
	}{
		{"KUBERNETES_JOB_LABELS", &j.Labels},
		{"KUBERNETES_JOB_ANNOTATIONS", &j.Annotations},
		{"KUBERNETES_JOB_NODE_SELECTOR", &j.NodeSelector},
	} {
		m, perr := ParseKeyValueList(os.Getenv(kv.name))
		if perr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", kv.name, perr))
		}
		*kv.dst = m
	}

	j.RunAsUser = DefaultKubeJobRunAsUser
	if raw := strings.TrimSpace(os.Getenv("KUBERNETES_JOB_RUN_AS_USER")); raw != "" {
		n, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil || n <= 0 {
			errs = append(errs, fmt.Sprintf("KUBERNETES_JOB_RUN_AS_USER=%q: want a positive integer (the restricted profile forbids UID 0)", raw))
		} else {
			j.RunAsUser = n
		}
	}
	if raw := strings.TrimSpace(os.Getenv("KUBERNETES_JOB_MAX_TIMEOUT_SECONDS")); raw != "" {
		n, perr := strconv.Atoi(raw)
		if perr != nil || n < 0 {
			errs = append(errs, fmt.Sprintf("KUBERNETES_JOB_MAX_TIMEOUT_SECONDS=%q: want a non-negative integer", raw))
		} else {
			j.MaxTimeoutSeconds = n
		}
	}
	for _, img := range strings.Split(os.Getenv("KUBERNETES_JOB_ALLOWED_IMAGES"), ",") {
		if img = strings.TrimSpace(img); img != "" {
			j.AllowedImages = append(j.AllowedImages, img)
		}
	}

	if len(errs) > 0 {
		return j, fmt.Errorf("invalid Kubernetes job settings: %s", strings.Join(errs, "; "))
	}
	return j, nil
}

// ParseKeyValueList parses "k=v,k2=v2" into a map. Empty input yields nil.
func ParseKeyValueList(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("entry %q: want key=value", part)
		}
		out[k] = strings.TrimSpace(v)
	}
	return out, nil
}

// SortedKeys returns the map's keys in order, for stable logging.
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func envBool(name string, def bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return def, fmt.Errorf("%s=%q: want true or false", name, raw)
	}
	return b, nil
}
