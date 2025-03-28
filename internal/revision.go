package internal

import (
	"cmp"
	"crypto/rand"
	"crypto/sha1"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Release struct {
	Name      string     `json:"release"`
	Namespace string     `json:"namespace"`
	History   []Revision `json:"history"`
}

func (release Release) ActiveRevision() Revision {
	var active Revision
	for _, revision := range release.History {
		if revision.ActiveAt.After(active.ActiveAt) {
			active = revision
		}
	}
	return active
}

func (release Release) ActiveIndex() int {
	var active int
	for i, revision := range release.History {
		if revision.ActiveAt.After(release.History[active].ActiveAt) {
			active = i
		}
	}
	return active
}

type Source struct {
	Ref      string `json:"ref"`
	Checksum string `json:"checksum"`
}

func SourceFrom(ref string, wasm []byte) (src Source) {
	if len(wasm) > 0 {
		src.Checksum = fmt.Sprintf("%x", sha1.Sum(wasm))
	}

	if ref != "" {
		u, _ := url.Parse(ref)
		if u.Scheme != "" {
			src.Ref = u.String()
		} else {
			src.Ref = "file://" + path.Clean(ref)
		}
	}

	return
}

func (release *Release) Add(revision Revision) {
	idx, _ := slices.BinarySearchFunc(release.History, revision, func(a, b Revision) int {
		switch {
		case a.CreatedAt.Before(b.CreatedAt):
			return -1
		case a.CreatedAt.After(b.CreatedAt):
			return 1
		default:
			return 0
		}
	})
	release.History = slices.Insert(release.History, idx, revision)
}

type Revision struct {
	Name      string    `json:"-"`
	Namespace string    `json:"-"`
	Source    Source    `json:"source"`
	CreatedAt time.Time `json:"createdAt"`
	ActiveAt  time.Time `json:"-"`
	Resources int       `json:"resources"`
}

const (
	LabelManagedBy     = "app.kubernetes.io/managed-by"
	LabelYokeRelease   = "app.kubernetes.io/yoke-release"
	LabelYokeReleaseNS = "app.kubernetes.io/yoke-release-namespace"
)

func AddYokeMetadata(resources []*unstructured.Unstructured, release, ns string) {
	for _, resource := range resources {
		labels := resource.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[LabelManagedBy] = "yoke"
		labels[LabelYokeRelease] = release
		labels[LabelYokeReleaseNS] = ns
		resource.SetLabels(labels)
	}
}

func GetOwner(resource unstructured.Unstructured) string {
	labels := resource.GetLabels()
	if labels == nil {
		return ""
	}

	release := labels[LabelYokeRelease]
	if release == "" {
		return ""
	}

	return labels[LabelYokeReleaseNS] + "/" + release
}

func Canonical(resource *unstructured.Unstructured) string {
	gvk := resource.GetObjectKind().GroupVersionKind()

	return strings.ToLower(strings.Join(
		[]string{
			Namespace(resource),
			cmp.Or(gvk.Group, "core"),
			gvk.Version,
			resource.GetKind(),
			resource.GetName(),
		},
		"/",
	))
}

func CanonicalWithoutVersion(resource *unstructured.Unstructured) string {
	gvk := resource.GetObjectKind().GroupVersionKind()

	return strings.ToLower(strings.Join(
		[]string{
			Namespace(resource),
			cmp.Or(gvk.Group, "core"),
			resource.GetKind(),
			resource.GetName(),
		},
		"/",
	))
}

func Namespace(resource *unstructured.Unstructured) string {
	return cmp.Or(resource.GetNamespace(), "_")
}

func CanonicalNameList(resources []*unstructured.Unstructured) []string {
	result := make([]string, len(resources))
	for i, resource := range resources {
		result[i] = Canonical(resource)
	}
	return result
}

func CanonicalMap(resources []*unstructured.Unstructured) map[string]*unstructured.Unstructured {
	result := make(map[string]*unstructured.Unstructured, len(resources))
	for _, resource := range resources {
		result[Canonical(resource)] = resource
	}
	return result
}

func CanonicalObjectMap(resources []*unstructured.Unstructured) map[string]any {
	result := make(map[string]any, len(resources))
	for _, resource := range resources {
		result[Canonical(resource)] = resource.Object
	}
	return result
}

const (
	LabelKind                = "internal.yoke/kind"
	LabelRelease             = "internal.yoke/release"
	AnnotationSourceURL      = "internal.yoke/source-url"
	AnnotationSourceChecksum = "internal.yoke/source-checksum"
	AnnotationCreatedAt      = "internal.yoke/created-at"
	AnnotationActiveAt       = "internal.yoke/active-at"
	AnnotationResourceCount  = "internal.yoke/resources"
	KeyResources             = "resources"
)

func MustParseTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}

func MustParseInt(value string) int {
	i, _ := strconv.Atoi(value)
	return i
}

func RandomString() string {
	buf := make([]byte, 6)
	rand.Read(buf)
	return fmt.Sprintf("%x", buf)
}
