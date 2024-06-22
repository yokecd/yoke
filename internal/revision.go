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

type Revisions struct {
	Release string     `json:"release"`
	History []Revision `json:"history"`
}

func (revisions Revisions) Active() Revision {
	var active Revision
	for _, revision := range revisions.History {
		if revision.ActiveAt.After(active.ActiveAt) {
			active = revision
		}
	}
	return active
}

func (revisions Revisions) ActiveIndex() int {
	var active int
	for i, revision := range revisions.History {
		if revision.ActiveAt.After(revisions.History[i].ActiveAt) {
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

func (revisions *Revisions) Add(revision Revision) {
	idx, _ := slices.BinarySearchFunc(revisions.History, revision, func(a, b Revision) int {
		switch {
		case a.CreatedAt.Before(b.CreatedAt):
			return -1
		case a.CreatedAt.After(b.CreatedAt):
			return 1
		default:
			return 0
		}
	})
	revisions.History = slices.Insert(revisions.History, idx, revision)
}

type Revision struct {
	Name      string    `json:"-"`
	Source    Source    `json:"source"`
	CreatedAt time.Time `json:"createdAt"`
	ActiveAt  time.Time `json:"-"`
	Resources int       `json:"resources"`
}

func AddYokeMetadata(resources []*unstructured.Unstructured, release string) {
	for _, resource := range resources {
		labels := resource.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels["app.kubernetes.io/managed-by"] = "yoke"
		labels["app.kubernetes.io/yoke-release"] = release
		resource.SetLabels(labels)
	}
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
		".",
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
